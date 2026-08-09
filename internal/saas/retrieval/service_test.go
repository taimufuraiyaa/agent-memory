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
)

type retrievalRepositoryFixture struct {
	lexical  []Candidate
	hydrated []Candidate
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

type vectorSearcherFixture struct{ hits []search.VectorHit }

func (v vectorSearcherFixture) SearchVectors(context.Context, string, []string, []float32, int) ([]search.VectorHit, error) {
	return append([]search.VectorHit(nil), v.hits...), nil
}

type retrievalModelFixture struct {
	embedErr      error
	generateErr   error
	generateCalls int
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
		{Evidence: Evidence{SourceID: "allowed", SourceVersion: 1, PassageID: "best", Text: "bounded source evidence", Breakdown: Breakdown{Exact: 1, FullText: 0.8}}, UsefulCount: 2, LastHelpfulAt: &helpfulAt},
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
	candidate := Candidate{Evidence: Evidence{SourceID: "source", SourceVersion: 1, PassageID: "passage", Text: "citable evidence", Breakdown: Breakdown{Exact: 1, FullText: 0.7}}}
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
