package application

import (
	"context"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/locator"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type TermOperator = sqlite.TermOperator

const (
	TermOperatorAND = sqlite.TermOperatorAND
	TermOperatorOR  = sqlite.TermOperatorOR
)

type TermSearchOptions struct {
	Workspace string
	Query     string
	Operator  TermOperator
	TopK      int
	Filters   engine.RetrievalFilters
}

type TermSearchHit struct {
	Memory       core.MemoryEntry `json:"memory"`
	MatchedTerms []string         `json:"matched_terms"`
	MatchCount   int              `json:"match_count"`
	SourceWeight int              `json:"source_weight"`
}

type TermPrefilter struct {
	Consulted        bool   `json:"consulted"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason,omitempty"`
	Shadow           bool   `json:"shadow"`
	ShortCircuited   bool   `json:"short_circuited,omitempty"`
	ShadowMismatch   bool   `json:"shadow_mismatch,omitempty"`
	Mode             string `json:"mode"`
	CorpusGeneration int64  `json:"corpus_generation,omitempty"`
	FilterGeneration int64  `json:"filter_generation,omitempty"`
}

type TermSearchResult struct {
	Workspace string          `json:"workspace"`
	Terms     []string        `json:"terms"`
	Operator  TermOperator    `json:"operator"`
	Strategy  string          `json:"strategy"`
	Prefilter TermPrefilter   `json:"prefilter"`
	Hits      []TermSearchHit `json:"hits"`
}

// SearchTerms executes project-local exact term lookup without semantic retrieval.
func (s *MemoryService) SearchTerms(ctx context.Context, options TermSearchOptions) (*TermSearchResult, error) {
	terms, err := locator.NormalizeQuery(options.Query)
	if err != nil {
		return nil, err
	}
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	operator := options.Operator
	if operator == "" {
		operator = TermOperatorAND
	}
	if operator != TermOperatorAND && operator != TermOperatorOR {
		return nil, errors.New("term operator must be and or or")
	}
	if s == nil || s.store == nil {
		return nil, errors.New("term search store is not available")
	}
	prefilter := TermPrefilter{Decision: "bypassed", Reason: "bloom_not_configured"}
	if s.termIndex != nil {
		prefilter = s.termIndex.Probe(ctx, s.store, workspace, terms, operator)
	}
	if prefilter.Consulted && prefilter.Mode == string(engine.TermBloomGate) && prefilter.Decision == "negative" {
		prefilter.ShortCircuited = true
		prefilter.Reason = "definite_miss"
		metrics := observability.GetRegistry()
		metrics.TermBloomProbes.WithLabelValues(workspace, prefilter.Mode, prefilter.Decision, prefilter.Reason).Inc()
		metrics.TermBloomShortCircuits.WithLabelValues(workspace, string(operator)).Inc()
		return &TermSearchResult{
			Workspace: workspace,
			Terms:     terms,
			Operator:  operator,
			Strategy:  "exact_terms",
			Prefilter: prefilter,
			Hits:      []TermSearchHit{},
		}, nil
	}
	topK := options.TopK
	if topK <= 0 {
		topK = 10
	}
	if topK > 200 {
		topK = 200
	}
	candidateLimit := topK * 5
	if candidateLimit < 30 {
		candidateLimit = 30
	}
	if candidateLimit > 200 {
		candidateLimit = 200
	}

	matches, err := s.store.SearchMemoryTerms(ctx, sqlite.TermSearchQuery{
		Workspace:            workspace,
		Terms:                terms,
		Operator:             operator,
		NormalizationVersion: locator.NormalizationVersion,
		Limit:                candidateLimit,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		ids = append(ids, match.MemoryID)
	}
	memories, err := s.store.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	hits := make([]TermSearchHit, 0, min(topK, len(matches)))
	for _, match := range matches {
		memory, ok := memories[match.MemoryID]
		if !ok || !matchesTermFilters(memory, options.Filters) {
			continue
		}
		keywords, err := s.store.ListMemoryTerms(ctx, workspace, memory.ID)
		if err != nil {
			return nil, err
		}
		memory.Keywords = keywords
		hits = append(hits, TermSearchHit{
			Memory:       memory,
			MatchedTerms: match.MatchedTerms,
			MatchCount:   match.MatchCount,
			SourceWeight: match.SourceWeight,
		})
		if len(hits) == topK {
			break
		}
	}
	if prefilter.Decision == "negative" && len(hits) > 0 {
		prefilter.ShadowMismatch = true
		prefilter.Reason = "shadow_negative_with_match"
		observability.GetRegistry().TermBloomMismatch.WithLabelValues(workspace, string(operator)).Inc()
	}
	if prefilter.Decision == "maybe" && len(hits) == 0 {
		prefilter.Reason = "observed_false_positive"
	}
	observability.GetRegistry().TermBloomProbes.WithLabelValues(workspace, prefilter.Mode, prefilter.Decision, prefilter.Reason).Inc()
	return &TermSearchResult{
		Workspace: workspace,
		Terms:     terms,
		Operator:  operator,
		Strategy:  "exact_terms",
		Prefilter: prefilter,
		Hits:      hits,
	}, nil
}

func matchesTermFilters(memory core.MemoryEntry, filters engine.RetrievalFilters) bool {
	if len(filters.Types) > 0 && !containsMemoryType(filters.Types, memory.Type) {
		return false
	}
	if len(filters.Tiers) > 0 && !containsStorageTier(filters.Tiers, memory.StorageTier) {
		return false
	}
	if filters.MinConfidence != nil && memory.Confidence < *filters.MinConfidence {
		return false
	}
	if filters.MinDecayScore != nil && memory.DecayScore < *filters.MinDecayScore {
		return false
	}
	if filters.OutcomeResult != nil && (memory.Outcome == nil || memory.Outcome.Result != *filters.OutcomeResult) {
		return false
	}
	if filters.DateFrom != nil && memory.UpdatedAt.Before(*filters.DateFrom) {
		return false
	}
	if filters.DateTo != nil && memory.UpdatedAt.After(*filters.DateTo) {
		return false
	}
	for _, wanted := range filters.Entities {
		found := false
		for _, entity := range memory.Entities {
			if strings.EqualFold(strings.TrimSpace(entity), strings.TrimSpace(wanted)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func containsMemoryType(values []core.MemoryType, target core.MemoryType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsStorageTier(values []core.StorageTier, target core.StorageTier) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
