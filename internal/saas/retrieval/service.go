package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/semantic"
)

type Service struct {
	repository       Repository
	vectors          VectorSearcher
	models           ModelGateway
	planner          semantic.Planner
	reranker         semantic.Reranker
	minimumRelevance float64
	now              func() time.Time
}

type Option func(*Service) error

func WithQueryPlanner(planner semantic.Planner) Option {
	return func(service *Service) error {
		if planner == nil {
			return errors.New("query planner is required")
		}
		service.planner = planner
		return nil
	}
}

func WithWindowReranker(reranker semantic.Reranker, minimumRelevance float64) Option {
	return func(service *Service) error {
		if reranker == nil || minimumRelevance <= 0 || minimumRelevance >= 1 {
			return errors.New("window reranker and a relevance threshold between zero and one are required")
		}
		service.reranker = reranker
		service.minimumRelevance = minimumRelevance
		return nil
	}
}

func NewService(repository Repository, vectors VectorSearcher, models ModelGateway, now func() time.Time, options ...Option) (*Service, error) {
	if repository == nil || vectors == nil || models == nil {
		return nil, errors.New("retrieval repository, vector searcher, and model gateway are required")
	}
	if now == nil {
		now = time.Now
	}
	service := &Service{repository: repository, vectors: vectors, models: models, now: now}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("retrieval option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) Query(ctx context.Context, request Query) (Result, error) {
	caller, ok := auth.FromContext(ctx)
	if !ok || !caller.Can("source:read") {
		return Result{}, errors.New("source:read capability is required")
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.TenantID == "" || caller.TenantID != request.TenantID || len(request.AuthorizedSourceIDs) == 0 || request.Text == "" || request.Provider == "" || request.Model == "" {
		return Result{}, errors.New("tenant, authorized sources, query, provider, and model are required")
	}
	if request.Limit <= 0 {
		request.Limit = 10
	}
	if request.Limit > 50 {
		return Result{}, errors.New("retrieval limit exceeds 50")
	}
	if request.Offset < 0 || request.Offset >= 50 || request.Offset+request.Limit > 50 {
		return Result{}, errors.New("retrieval offset exceeds the bounded result window")
	}
	retrievalLimit := request.Offset + request.Limit + 1
	if retrievalLimit > 50 {
		retrievalLimit = 50
	}
	if request.ContextTokenBudget <= 0 {
		request.ContextTokenBudget = 1200
	}
	authorized, err := s.repository.AuthorizedSourceIDs(ctx, request.TenantID, request.AuthorizedSourceIDs)
	if err != nil {
		return Result{}, err
	}
	if len(authorized) != len(request.AuthorizedSourceIDs) {
		return Result{}, errors.New("one or more requested sources are not authorized")
	}
	request.AuthorizedSourceIDs = authorized
	semanticMetadata := SemanticMetadata{}
	semanticQuery := request.Text
	cue := retrievalCue(request.Text)
	var queryPlan *semantic.QueryPlan
	if s.planner != nil {
		plan, planErr := s.planner.Plan(ctx, request.Text)
		if planErr != nil {
			semanticMetadata.Fallbacks = append(semanticMetadata.Fallbacks, "planner_unavailable")
		} else {
			semanticMetadata.PlannerUsed = true
			semanticMetadata.PlanVersion = plan.Version
			semanticMetadata.Language = plan.Language
			semanticMetadata.Intent = string(plan.Intent)
			semanticMetadata.Subject = plan.Subject
			cue = strings.Join(plan.RetrievalTerms, " ")
			semanticQuery = plannedQuestion(plan, request.Text)
			queryPlan = &plan
		}
	}
	candidateLimit := retrievalLimit * 10
	if candidateLimit > 150 {
		candidateLimit = 150
	}
	lexical, err := s.repository.LexicalCandidates(ctx, request.TenantID, request.AuthorizedSourceIDs, cue, candidateLimit)
	if err != nil {
		return Result{}, err
	}
	merged := make(map[string]Candidate, len(lexical))
	for _, candidate := range lexical {
		merged[evidenceKey(candidate.SourceID, candidate.PassageID)] = candidate
	}
	embedding, embedErr := s.models.Embed(ctx, modelgateway.EmbedRequest{TenantID: request.TenantID, Provider: request.Provider, Model: request.Model, Texts: []string{cue}})
	if embedErr == nil && len(embedding.Vectors) == 1 {
		vectorHits, vectorErr := s.vectors.SearchVectors(ctx, request.TenantID, request.AuthorizedSourceIDs, embedding.Vectors[0], candidateLimit)
		if vectorErr != nil {
			return Result{}, vectorErr
		}
		missing := []EvidenceKey{}
		for _, hit := range vectorHits {
			key := evidenceKey(hit.SourceID, hit.PassageID)
			candidate, ok := merged[key]
			if !ok {
				missing = append(missing, EvidenceKey{SourceID: hit.SourceID, PassageID: hit.PassageID})
				continue
			}
			candidate.Breakdown.Vector = hit.Score
			merged[key] = candidate
		}
		if len(missing) > 0 {
			hydrated, err := s.repository.EvidenceByPassageIDs(ctx, request.TenantID, request.AuthorizedSourceIDs, missing)
			if err != nil {
				return Result{}, err
			}
			byID := make(map[string]Candidate, len(hydrated))
			for _, candidate := range hydrated {
				byID[evidenceKey(candidate.SourceID, candidate.PassageID)] = candidate
			}
			for _, hit := range vectorHits {
				key := evidenceKey(hit.SourceID, hit.PassageID)
				if _, exists := merged[key]; exists {
					continue
				}
				candidate, ok := byID[key]
				if !ok {
					continue
				}
				candidate.Breakdown.Vector = hit.Score
				merged[key] = candidate
			}
		}
	}
	allowed := make(map[string]struct{}, len(request.AuthorizedSourceIDs))
	for _, sourceID := range request.AuthorizedSourceIDs {
		allowed[sourceID] = struct{}{}
	}
	now := s.now().UTC()
	ranked := make([]Evidence, 0, len(merged))
	for _, candidate := range merged {
		if _, authorized := allowed[candidate.SourceID]; !authorized {
			continue
		}
		candidate.Breakdown = score(candidate, now)
		candidate.Score = candidate.Breakdown.Total
		if candidate.Breakdown.Suppression >= core.DefaultAdaptiveSignalTuning().SuppressionBandThreshold || candidate.SuppressionUntil != nil && candidate.SuppressionUntil.After(now) {
			continue
		}
		ranked = append(ranked, candidate.Evidence)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].PassageID < ranked[j].PassageID
		}
		return ranked[i].Score > ranked[j].Score
	})
	reconstructed, err := reconstructEvidence(ctx, s.repository, request.TenantID, request.AuthorizedSourceIDs, ranked, semanticQuery, retrievalLimit)
	if err != nil {
		return Result{}, err
	}
	if queryPlan != nil {
		reconstructed = applyPlanExclusions(reconstructed, *queryPlan)
	}
	if s.reranker != nil && len(reconstructed) > 0 {
		documents := make([]string, len(reconstructed))
		for index := range reconstructed {
			documents[index] = reconstructed[index].Text
		}
		scores, rerankErr := s.reranker.Rerank(ctx, request.Text, documents)
		if rerankErr != nil || len(scores) != len(reconstructed) {
			semanticMetadata.Fallbacks = append(semanticMetadata.Fallbacks, "reranker_unavailable")
		} else {
			semanticMetadata.RerankerUsed = true
			reconstructed = applyRelevance(reconstructed, scores, s.minimumRelevance)
		}
	}
	reconstructed = supportedWindows(reconstructed)
	page, pagination := paginateEvidence(reconstructed, request.Offset, request.Limit)
	bounded, metadata, prompt := compileContext(page, request.ContextTokenBudget, request.Text)
	returnedEnd := request.Offset + len(bounded)
	pagination.HasMore = len(bounded) > 0 && returnedEnd < len(reconstructed)
	pagination.NextOffset = nil
	if pagination.HasMore {
		pagination.NextOffset = &returnedEnd
	}
	metadata.Strategy = contextStrategy
	metadata.CandidateCount = len(ranked)
	metadata.ReconstructedWindows = len(reconstructed)
	metadata.Semantic = semanticMetadata
	answerable := false
	for _, evidence := range bounded {
		answerable = answerable || evidence.AnswerSupport
	}
	result := Result{Answerable: answerable, EvidenceAvailable: len(bounded) > 0, Evidence: bounded, Context: metadata, Pagination: pagination}
	if !result.Answerable || !request.Generate {
		return result, nil
	}
	gatewayEvidence := make([]modelgateway.Evidence, len(bounded))
	for index, evidence := range bounded {
		gatewayEvidence[index] = modelgateway.Evidence{SourceID: evidence.SourceID, PassageID: evidence.PassageID, Text: evidence.Text}
	}
	generated, err := s.models.Generate(ctx, modelgateway.GenerateRequest{TenantID: request.TenantID, Provider: request.Provider, Model: request.Model, Prompt: prompt, Evidence: gatewayEvidence})
	if err != nil {
		return Result{}, err
	}
	result.Generated = generated.Generated
	result.Synthesis = generated.Text
	result.FailureCode = generated.FailureCode
	return result, nil
}

func paginateEvidence(evidence []Evidence, offset, limit int) ([]Evidence, Pagination) {
	page := Pagination{Offset: offset, Limit: limit}
	if offset >= len(evidence) {
		return []Evidence{}, page
	}
	end := offset + limit
	if end > len(evidence) {
		end = len(evidence)
	}
	page.HasMore = end < len(evidence)
	if page.HasMore {
		next := end
		page.NextOffset = &next
	}
	return evidence[offset:end], page
}

func applyPlanExclusions(evidence []Evidence, plan semantic.QueryPlan) []Evidence {
	subject := strings.ToLower(plan.Subject)
	if plan.Intent == semantic.IntentDefinition {
		subjectMatches := make([]Evidence, 0, len(evidence))
		for _, item := range evidence {
			if strings.Contains(strings.ToLower(item.Text), subject) {
				subjectMatches = append(subjectMatches, item)
			}
		}
		if len(subjectMatches) > 0 {
			evidence = subjectMatches
		}
	}
	if len(plan.Exclusions) == 0 {
		return evidence
	}
	filtered := make([]Evidence, 0, len(evidence))
	for _, item := range evidence {
		text := strings.ToLower(item.Text)
		excluded := false
		for _, exclusion := range plan.Exclusions {
			if strings.Contains(text, strings.ToLower(exclusion)) && !strings.Contains(text, subject) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func plannedQuestion(plan semantic.QueryPlan, original string) string {
	if plan.Intent == semantic.IntentDefinition {
		return "What is " + plan.Subject + "?"
	}
	return original
}

func applyRelevance(evidence []Evidence, scores []float64, minimum float64) []Evidence {
	filtered := make([]Evidence, 0, len(evidence))
	for index := range evidence {
		if scores[index] < minimum {
			continue
		}
		evidence[index].RelevanceScore = scores[index]
		evidence[index].Score += scores[index]
		evidence[index].Breakdown.Total = evidence[index].Score
		filtered = append(filtered, evidence[index])
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].RelevanceScore == filtered[j].RelevanceScore {
			return filtered[i].PassageID < filtered[j].PassageID
		}
		return filtered[i].RelevanceScore > filtered[j].RelevanceScore
	})
	return filtered
}

func score(candidate Candidate, now time.Time) Breakdown {
	tuning := core.DefaultAdaptiveSignalTuning()
	breakdown := candidate.Breakdown
	breakdown.Vector = math.Max(0, clamp(breakdown.Vector, -1, 1))
	breakdown.FullText = clamp(breakdown.FullText, 0, 1)
	breakdown.Exact = clamp(breakdown.Exact, 0, 1)
	breakdown.Decay = math.Max(0, 1-clamp(candidate.DecayScore, 0, 1))
	breakdown.Salience = clamp(candidate.SalienceScore, 0, 1) * tuning.SalienceScoreFactor
	breakdown.Feedback = math.Min(float64(candidate.UsefulCount), tuning.UsefulCountCap) * tuning.UsefulCountStep
	if candidate.LastHelpfulAt != nil {
		breakdown.Feedback += recency(now, *candidate.LastHelpfulAt) * tuning.LastHelpfulRecencyWeight
	}
	breakdown.Suppression = clamp(candidate.SuppressionScore, 0, 1)*tuning.SuppressionScoreFactor + math.Min(float64(candidate.RejectedCount), tuning.RejectedCountCap)*tuning.RejectedCountStep + math.Min(float64(candidate.HarmfulCount), tuning.HarmfulCountCap)*tuning.HarmfulCountStep
	if candidate.LastRejectedAt != nil {
		breakdown.Suppression += recency(now, *candidate.LastRejectedAt) * tuning.LastRejectedRecencyWeight
	}
	if candidate.SuppressionUntil != nil && candidate.SuppressionUntil.After(now) {
		breakdown.Suppression += tuning.ActiveSuppressionBoost
	}
	breakdown.Activation = 0.20*breakdown.Exact + 0.25*breakdown.FullText + 0.45*breakdown.Vector + 0.05*breakdown.Decay + 0.05 + breakdown.Salience + breakdown.Feedback
	breakdown.Total = breakdown.Activation - breakdown.Suppression
	return breakdown
}

func compileContext(evidence []Evidence, budget int, query string) ([]Evidence, ContextMetadata, string) {
	metadata := ContextMetadata{Budget: budget, IncludedIDs: []string{}, ClippedIDs: []string{}}
	included := make([]Evidence, 0, len(evidence))
	var contextBuilder strings.Builder
	for _, item := range evidence {
		tokens := len(strings.Fields(item.Text))
		if tokens > budget || metadata.UsedTokens+tokens > budget {
			metadata.ClippedIDs = append(metadata.ClippedIDs, item.PassageID)
			continue
		}
		included = append(included, item)
		metadata.UsedTokens += tokens
		metadata.IncludedIDs = append(metadata.IncludedIDs, item.PassageID)
		fmt.Fprintf(&contextBuilder, "[%s:%s] %s\n", item.SourceID, item.PassageID, item.Text)
	}
	prompt := "Answer only from the citation-labelled evidence. If it is insufficient, say so.\nQuestion: " + query + "\nEvidence:\n" + contextBuilder.String()
	return included, metadata, prompt
}

func recency(now, at time.Time) float64 {
	hours := now.Sub(at).Hours()
	if hours <= 0 {
		return 1
	}
	return 1 / (1 + hours/24)
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func evidenceKey(sourceID, passageID string) string { return sourceID + "\x00" + passageID }
