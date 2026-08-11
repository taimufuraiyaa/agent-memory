package retrieval

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/search"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/semantic"
)

type retrievalRepositoryFixture struct {
	lexical  []Candidate
	hydrated []Candidate
	expanded []Candidate
}

func (r retrievalRepositoryFixture) AuthorizedSourceIDs(_ context.Context, _ string, requested []string) ([]string, error) {
	return append([]string(nil), requested...), nil
}

func (r retrievalRepositoryFixture) LexicalCandidates(context.Context, string, []string, string, int) ([]Candidate, error) {
	return append([]Candidate(nil), r.lexical...), nil
}
func (r retrievalRepositoryFixture) EvidenceByPassageIDs(context.Context, string, []string, []EvidenceKey) ([]Candidate, error) {
	return append([]Candidate(nil), r.hydrated...), nil
}
func (r retrievalRepositoryFixture) ContextByAnchors(context.Context, string, []string, []ContextAnchor) ([]Candidate, error) {
	if r.expanded != nil {
		return append([]Candidate(nil), r.expanded...), nil
	}
	return append([]Candidate(nil), r.lexical...), nil
}

type vectorSearcherFixture struct{ hits []search.VectorHit }

func (v vectorSearcherFixture) SearchVectors(context.Context, string, []string, []float32, int) ([]search.VectorHit, error) {
	return append([]search.VectorHit(nil), v.hits...), nil
}

type retrievalModelFixture struct {
	embedErr      error
	generateErr   error
	generateCalls int
}

type queryPlannerFixture struct {
	plan semantic.QueryPlan
	err  error
}

func (p queryPlannerFixture) Plan(context.Context, string) (semantic.QueryPlan, error) {
	return p.plan, p.err
}

type rerankerFixture struct {
	scores []float64
	err    error
}

func (r rerankerFixture) Rerank(context.Context, string, []string) ([]float64, error) {
	return append([]float64(nil), r.scores...), r.err
}

func (m *retrievalModelFixture) Embed(context.Context, modelgateway.EmbedRequest) (modelgateway.EmbedResponse, error) {
	if m.embedErr != nil {
		return modelgateway.EmbedResponse{}, m.embedErr
	}
	return modelgateway.EmbedResponse{Vectors: [][]float32{make([]float32, search.VectorDimensions)}, Dimensions: search.VectorDimensions}, nil
}
func (m *retrievalModelFixture) Generate(_ context.Context, request modelgateway.GenerateRequest) (modelgateway.GenerateResponse, error) {
	m.generateCalls++
	if m.generateErr != nil {
		return modelgateway.GenerateResponse{Evidence: request.Evidence, FailureCode: "generation_unavailable"}, nil
	}
	return modelgateway.GenerateResponse{Generated: true, Text: "grounded answer", Evidence: request.Evidence}, nil
}

func TestServiceMixesSignalsClampsNegativeVectorAndReauthorizesSerialization(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	helpfulAt := now.Add(-time.Hour)
	repository := retrievalRepositoryFixture{lexical: []Candidate{
		{Evidence: Evidence{SourceID: "allowed", SourceVersion: 1, PassageID: "best", Text: "This question is supported by bounded source evidence.", Breakdown: Breakdown{Exact: 1, FullText: 0.8}}, UsefulCount: 2, LastHelpfulAt: &helpfulAt},
		{Evidence: Evidence{SourceID: "forged", SourceVersion: 1, PassageID: "leak", Text: "must not serialize", Breakdown: Breakdown{Exact: 1, FullText: 1}}},
	}}
	models := &retrievalModelFixture{}
	service, err := NewService(repository, vectorSearcherFixture{hits: []search.VectorHit{{SourceID: "allowed", SourceVersion: 1, PassageID: "best", Score: -0.9}}}, models, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"allowed"}, Text: "question", Limit: 5, ContextTokenBudget: 10, Generate: true, Provider: "private", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Answerable || !result.Generated || result.Synthesis != "grounded answer" || len(result.Evidence) != 1 || result.Evidence[0].PassageID != "best" {
		t.Fatalf("result=%+v", result)
	}
	if result.Evidence[0].Breakdown.Vector != 0 {
		t.Fatalf("negative vector contributed to score: %+v", result.Evidence[0].Breakdown)
	}
	if strings.Contains(result.Synthesis, "must not serialize") || models.generateCalls != 1 {
		t.Fatalf("authorization or generation calls failed: %+v calls=%d", result, models.generateCalls)
	}
}

func TestServiceReturnsEvidenceWhenEmbeddingOrGenerationFails(t *testing.T) {
	candidate := Candidate{Evidence: Evidence{SourceID: "source", SourceVersion: 1, PassageID: "passage", Text: "This question is answered by this citable source evidence.", Breakdown: Breakdown{Exact: 1, FullText: 0.7}}}
	models := &retrievalModelFixture{embedErr: errors.New("embedding unavailable"), generateErr: errors.New("generation unavailable")}
	service, err := NewService(retrievalRepositoryFixture{lexical: []Candidate{candidate}}, vectorSearcherFixture{}, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "question", Generate: true, Provider: "private", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Answerable || result.Generated || result.FailureCode != "generation_unavailable" || len(result.Evidence) != 1 {
		t.Fatalf("fallback result=%+v", result)
	}
}

func TestServiceKeepsNoEvidenceUnanswerableAndDoesNotGenerate(t *testing.T) {
	models := &retrievalModelFixture{}
	service, err := NewService(retrievalRepositoryFixture{}, vectorSearcherFixture{}, models, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "unknown", Generate: true, Provider: "private", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answerable || result.Generated || len(result.Evidence) != 0 || models.generateCalls != 0 {
		t.Fatalf("no-evidence result=%+v calls=%d", result, models.generateCalls)
	}
}

func TestContextBudgetSkipsOversizedEvidenceWithoutExceedingLimit(t *testing.T) {
	evidence := []Evidence{{PassageID: "large", Text: "one two three four five"}, {PassageID: "small", Text: "one two"}}
	included, metadata, _ := compileContext(evidence, 3, "question")
	if len(included) != 1 || included[0].PassageID != "small" || metadata.UsedTokens != 2 || len(metadata.ClippedIDs) != 1 || metadata.ClippedIDs[0] != "large" {
		t.Fatalf("included=%+v metadata=%+v", included, metadata)
	}
}

func TestHostedAdaptiveSignalsUseLocalRetrievalTuning(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	helpful, rejected, until := now.Add(-time.Hour), now.Add(-2*time.Hour), now.Add(time.Hour)
	candidate := Candidate{DecayScore: 0.25, SalienceScore: 0.5, SuppressionScore: 0.4, UsefulCount: 2, RejectedCount: 1, HarmfulCount: 1, LastHelpfulAt: &helpful, LastRejectedAt: &rejected, SuppressionUntil: &until}
	got := score(candidate, now)
	tuning := core.DefaultAdaptiveSignalTuning()
	wantSalience := .5 * tuning.SalienceScoreFactor
	wantFeedback := 2*tuning.UsefulCountStep + recency(now, helpful)*tuning.LastHelpfulRecencyWeight
	wantSuppression := .4*tuning.SuppressionScoreFactor + tuning.RejectedCountStep + tuning.HarmfulCountStep + recency(now, rejected)*tuning.LastRejectedRecencyWeight + tuning.ActiveSuppressionBoost
	if math.Abs(got.Salience-wantSalience) > 1e-9 || math.Abs(got.Feedback-wantFeedback) > 1e-9 || math.Abs(got.Suppression-wantSuppression) > 1e-9 {
		t.Fatalf("hosted signals=%+v want salience=%f feedback=%f suppression=%f", got, wantSalience, wantFeedback, wantSuppression)
	}
}

func TestServiceReconstructsDefinitionFromAdjacentPassagesAndDemotesTableOfContents(t *testing.T) {
	definition := []Candidate{
		contextCandidate("source", "definition", "definition-heading", "citation-definition-heading", "1. Definition of latency"),
		contextCandidate("source", "definition", "definition-1", "citation-definition-1", "In system design, latency is"),
		contextCandidate("source", "definition", "definition-2", "citation-definition-2", "the total time a request spends passing through the system."),
		contextCandidate("source", "definition", "definition-3", "citation-definition-3", "It includes travel, waiting, and service time."),
	}
	tableOfContents := []Candidate{
		contextCandidate("source", "contents", "contents-definition", "citation-contents-definition", "1. Definition of latency"),
		contextCandidate("source", "contents", "contents-mean", "citation-contents-mean", "2.1. Mean latency"),
		contextCandidate("source", "contents", "contents-hide", "citation-contents-hide", "4.4. Hide latency"),
	}
	repository := retrievalRepositoryFixture{
		lexical: []Candidate{
			{Evidence: Evidence{SourceID: "source", SourceVersion: 1, PassageID: "contents-definition", CitationID: "citation-contents-definition", StructuralNodeID: "contents", Text: "1. Definition of latency", Breakdown: Breakdown{Exact: 1, FullText: 1}}},
			{Evidence: Evidence{SourceID: "source", SourceVersion: 1, PassageID: "definition-heading", CitationID: "citation-definition-heading", StructuralNodeID: "definition", Text: "1. Definition of latency", Breakdown: Breakdown{Exact: 1, FullText: .8}}},
		},
		expanded: append(tableOfContents, definition...),
	}
	models := &retrievalModelFixture{embedErr: errors.New("embedding unavailable")}
	service, err := NewService(repository, vectorSearcherFixture{}, models, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "What is latency?", Limit: 10, ContextTokenBudget: 1200, Provider: "local", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Answerable || !result.EvidenceAvailable || len(result.Evidence) != 1 {
		t.Fatalf("expected answerable reconstructed evidence: %+v", result)
	}
	first := result.Evidence[0]
	if first.StructuralNodeID != "definition" || !strings.Contains(first.Text, "total time a request") || !strings.Contains(first.Text, "travel, waiting, and service") {
		t.Fatalf("leading context was not the definition: %+v", first)
	}
	if first.ReconstructionStrategy != "structural-neighbors-v1" || len(first.IncludedPassageIDs) != 4 || len(first.IncludedCitationIDs) != 4 {
		t.Fatalf("missing reconstruction provenance: %+v", first)
	}
	if result.Context.Strategy != "reconstructive-structural-v1" || result.Context.ReconstructedWindows < 1 {
		t.Fatalf("missing context strategy: %+v", result.Context)
	}
}

func TestServiceKeepsHeadingOnlyContextInspectableButUnanswerable(t *testing.T) {
	heading := contextCandidate("source", "contents", "heading", "citation-heading", "1. Definition of latency")
	repository := retrievalRepositoryFixture{
		lexical:  []Candidate{{Evidence: heading.Evidence}},
		expanded: []Candidate{heading},
	}
	models := &retrievalModelFixture{embedErr: errors.New("embedding unavailable")}
	service, err := NewService(repository, vectorSearcherFixture{}, models, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "What is latency?", Generate: true, Provider: "local", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answerable || !result.EvidenceAvailable || len(result.Evidence) != 1 || models.generateCalls != 0 {
		t.Fatalf("heading-only result crossed sufficiency boundary: %+v calls=%d", result, models.generateCalls)
	}
}

func TestServiceCollapsesOverlappingAnchorsIntoOneStructuralWindow(t *testing.T) {
	contextMembers := []Candidate{
		contextCandidate("source", "definition", "line-1", "citation-1", "Latency is total elapsed time."),
		contextCandidate("source", "definition", "line-2", "citation-2", "It includes queueing and service time."),
	}
	repository := retrievalRepositoryFixture{
		lexical: []Candidate{
			{Evidence: contextMembers[0].Evidence},
			{Evidence: contextMembers[1].Evidence},
		},
		expanded: contextMembers,
	}
	models := &retrievalModelFixture{embedErr: errors.New("embedding unavailable")}
	service, err := NewService(repository, vectorSearcherFixture{}, models, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "What is latency?", Provider: "local", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence) != 1 || len(result.Evidence[0].IncludedPassageIDs) != 2 {
		t.Fatalf("overlapping anchors were not collapsed: %+v", result.Evidence)
	}
}

func contextCandidate(source, node, passage, citation, text string) Candidate {
	return Candidate{Evidence: Evidence{SourceID: source, SourceVersion: 1, PassageID: passage, CitationID: citation, StructuralNodeID: node, Text: text, Locator: map[string]any{"display": "Page 4"}}}
}

func TestDefinitionRelationRequiresTheSubjectToParticipateInTheExplanation(t *testing.T) {
	terms := meaningfulTerms("What is latency?")
	if !definitionRelation("In system design, latency is the total elapsed time.", terms) {
		t.Fatal("direct subject definition was rejected")
	}
	if !definitionRelation("Trong System Design, Latency là tổng thời gian của request.", terms) {
		t.Fatal("Vietnamese subject definition was rejected")
	}
	if definitionRelation("The database may fail, or tệ hơn là sập. Latency", terms) {
		t.Fatal("unrelated explanatory marker made a trailing subject look defined")
	}
	if definitionRelation("Hide latency is a technique for making waits less visible.", terms) {
		t.Fatal("a modified concept was treated as the queried subject definition")
	}
}

func TestRetrievalCueRemovesQuestionScaffolding(t *testing.T) {
	if got := retrievalCue("What is latency?"); got != "latency" {
		t.Fatalf("retrieval cue=%q", got)
	}
	if got := retrievalCue("What does this source say about queueing latency?"); got != "queueing latency" {
		t.Fatalf("multi-term retrieval cue=%q", got)
	}
}

func TestSemanticWindowEndCompletesTheCurrentSentenceAcrossPDFBlocks(t *testing.T) {
	members := []Candidate{
		contextCandidate("source", "page", "1", "c1", "Latency is total elapsed time."),
		contextCandidate("source", "page", "2", "c2", "Propagation is limited by physics."),
		contextCandidate("source", "page", "3", "c3", "Light travels through fiber."),
		contextCandidate("source", "page", "4", "c4", "The route may cross oceans."),
		contextCandidate("source", "page", "5", "c5", "Round trips add delay."),
		contextCandidate("source", "page", "6", "c6", "Networks also queue packets."),
		contextCandidate("source", "page", "7", "c7", "Service time adds more delay."),
		contextCandidate("source", "page", "8", "c8", "The total remains latency."),
		contextCandidate("source", "page", "9", "c9", "For example"),
		contextCandidate("source", "page", "10", "c10", "a packet"),
		contextCandidate("source", "page", "11", "c11", "travelling across the Atlantic"),
		contextCandidate("source", "page", "12", "c12", "takes at least 50ms."),
		contextCandidate("source", "page", "13", "c13", "A new paragraph begins."),
	}
	end, complete := semanticWindowEnd(members, 10)
	if end != 12 || !complete {
		t.Fatalf("semantic end=%d complete=%t", end, complete)
	}
}

func TestSemanticWindowEndRemainsBoundedWithoutTerminalPunctuation(t *testing.T) {
	members := make([]Candidate, 20)
	for index := range members {
		members[index] = contextCandidate("source", "page", string(rune('a'+index)), "citation", "continued fragment")
	}
	end, complete := semanticWindowEnd(members, 10)
	if end != 14 || complete {
		t.Fatalf("unbounded semantic end=%d complete=%t", end, complete)
	}
}

func TestDefinitionRelationRecognizesParentheticalColonDefinition(t *testing.T) {
	if !definitionRelation("λ (Throughput): Tốc độ request đi vào (requests/sec).", []string{"throughput"}) {
		t.Fatal("parenthetical glossary definition was not recognized")
	}
}

func TestServiceUsesVietnameseQueryPlanAndReranksCompleteWindows(t *testing.T) {
	throughput := contextCandidate("source", "throughput", "throughput-definition", "citation-throughput", "Throughput là lượng công việc hệ thống hoàn thành trong một đơn vị thời gian.")
	garbageCollection := contextCandidate("source", "gc", "gc-pause", "citation-gc", "Garbage collection pauses an application while memory is reclaimed.")
	repository := retrievalRepositoryFixture{
		lexical: []Candidate{
			{Evidence: throughput.Evidence},
			{Evidence: garbageCollection.Evidence},
		},
		expanded: []Candidate{throughput, garbageCollection},
	}
	planner := queryPlannerFixture{plan: semantic.QueryPlan{
		Version: "query-plan-v1", Language: "vi", Intent: semantic.IntentDefinition,
		Subject: "throughput", RetrievalTerms: []string{"throughput", "requests per second"},
		Exclusions: []string{"garbage collection"}, AnswerForm: semantic.AnswerConciseDefinition,
	}}
	service, err := NewService(repository, vectorSearcherFixture{}, &retrievalModelFixture{embedErr: errors.New("embedding unavailable")}, nil,
		WithQueryPlanner(planner), WithWindowReranker(rerankerFixture{scores: []float64{0.98}}, 0.5))
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "Throughput là gì?", Limit: 10, Provider: "local", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Answerable || len(result.Evidence) != 1 || result.Evidence[0].StructuralNodeID != "throughput" {
		t.Fatalf("semantic result=%+v", result)
	}
	if !result.Context.Semantic.PlannerUsed || !result.Context.Semantic.RerankerUsed || result.Context.Semantic.Language != "vi" || result.Context.Semantic.Intent != "definition" {
		t.Fatalf("semantic metadata=%+v", result.Context.Semantic)
	}
	if result.Evidence[0].RelevanceScore != 0.98 {
		t.Fatalf("reranked evidence=%+v", result.Evidence[0])
	}
}

func TestServiceGroundsDefinitionPlanToSubjectBeforeOptionalReranking(t *testing.T) {
	throughput := contextCandidate("source", "throughput", "throughput-definition", "citation-throughput", "Throughput là lượng công việc hệ thống hoàn thành trong một đơn vị thời gian.")
	garbageCollection := contextCandidate("source", "gc", "gc-pause", "citation-gc", "Garbage collection pauses an application while memory is reclaimed.")
	planner := queryPlannerFixture{plan: semantic.QueryPlan{
		Version: "query-plan-v1", Language: "vi", Intent: semantic.IntentDefinition,
		Subject: "throughput", RetrievalTerms: []string{"throughput", "requests per second"},
		Exclusions: []string{}, AnswerForm: semantic.AnswerConciseDefinition,
	}}
	service, err := NewService(
		retrievalRepositoryFixture{
			lexical:  []Candidate{{Evidence: throughput.Evidence}, {Evidence: garbageCollection.Evidence}},
			expanded: []Candidate{throughput, garbageCollection},
		},
		vectorSearcherFixture{}, &retrievalModelFixture{embedErr: errors.New("embedding unavailable")}, nil,
		WithQueryPlanner(planner),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "Throughput là gì?", Limit: 10, Provider: "local", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence) != 1 || result.Evidence[0].StructuralNodeID != "throughput" || !result.Context.Semantic.PlannerUsed {
		t.Fatalf("subject-grounded result=%+v", result)
	}
}

func TestServiceFallsBackWhenLocalSemanticRolesFail(t *testing.T) {
	candidate := contextCandidate("source", "definition", "definition", "citation", "Latency is total elapsed time through a system.")
	service, err := NewService(
		retrievalRepositoryFixture{lexical: []Candidate{candidate}, expanded: []Candidate{candidate}},
		vectorSearcherFixture{}, &retrievalModelFixture{embedErr: errors.New("embedding unavailable")}, nil,
		WithQueryPlanner(queryPlannerFixture{err: errors.New("planner timeout")}),
		WithWindowReranker(rerankerFixture{err: errors.New("reranker timeout")}, 0.5),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Query(context.Background(), Query{TenantID: "tenant", AuthorizedSourceIDs: []string{"source"}, Text: "What is latency?", Provider: "local", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Evidence) != 1 || result.Context.Semantic.PlannerUsed || result.Context.Semantic.RerankerUsed {
		t.Fatalf("fallback result=%+v", result)
	}
	if !containsString(result.Context.Semantic.Fallbacks, "planner_unavailable") || !containsString(result.Context.Semantic.Fallbacks, "reranker_unavailable") {
		t.Fatalf("fallback metadata=%+v", result.Context.Semantic)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
