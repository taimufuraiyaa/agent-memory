package engine

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestRetrievalEngineExplainAndModes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.OutcomeMemory,
		Content:   "Retry with exponential backoff fixed payment timeout",
		Source:    core.MemorySource{Type: core.SourceAgentObservation},
		Outcome:   &core.Outcome{Result: core.OutcomeSuccess},
	})
	_, _ = pipe.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "Orders service publishes order.created event",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	})

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	searcher := NewVectorSearcher(store, provider)
	engine := NewRetrievalEngine(searcher)
	engine.clock = func() time.Time { return time.Now().UTC() }

	outcomesRes, err := engine.Retrieve(context.Background(), RetrievalOptions{
		Workspace: "ws",
		Query:     "payment timeout workaround",
		TopK:      2,
		Mode:      ModeOutcomes,
	})
	if err != nil {
		t.Fatalf("retrieve outcomes: %v", err)
	}
	if len(outcomesRes.Hits) == 0 {
		t.Fatalf("expected hits")
	}
	if outcomesRes.Hits[0].Breakdown.Total == 0 {
		t.Fatalf("expected explainable total score")
	}
	if outcomesRes.Weights.Outcome <= outcomesRes.Weights.Recency {
		t.Fatalf("expected outcome mode to prioritize outcome weight")
	}
	if outcomesRes.Policy.MinSemanticScore <= 0 {
		t.Fatalf("expected retrieval policy snapshot")
	}
}

func TestRetrievalEngineBandsAndCutoffs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "banded.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	strong := &core.MemoryEntry{
		ID:            "strong",
		Type:          core.ProceduralMemory,
		Content:       "run migrations before deploying the orders API",
		Workspace:     "ws",
		Source:        core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier:   core.TierMarkdown,
		Confidence:    0.95,
		CreatedAt:     now.Add(-2 * time.Hour),
		UpdatedAt:     now.Add(-2 * time.Hour),
		SalienceScore: 0.9,
		UsefulCount:   3,
	}
	weak := &core.MemoryEntry{
		ID:                  "weak",
		Type:                core.SemanticMemory,
		Content:             "deployment checklist mentions smoke tests and rollback plans",
		Workspace:           "ws",
		Source:              core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier:         core.TierVector,
		Confidence:          0.7,
		CreatedAt:           now.Add(-24 * time.Hour),
		UpdatedAt:           now.Add(-24 * time.Hour),
		FamiliarityBandLast: "weak_familiarity",
	}
	suppressedUntil := now.Add(12 * time.Hour)
	suppressed := &core.MemoryEntry{
		ID:               "suppressed",
		Type:             core.SemanticMemory,
		Content:          "rollback notes with wrong deploy order",
		Workspace:        "ws",
		Source:           core.MemorySource{Type: core.SourceAgentObservation},
		StorageTier:      core.TierVector,
		Confidence:       0.8,
		CreatedAt:        now.Add(-6 * time.Hour),
		UpdatedAt:        now.Add(-6 * time.Hour),
		SuppressionScore: 0.9,
		RejectedCount:    3,
		LastRejectedAt:   now.Add(-1 * time.Hour),
		SuppressionUntil: &suppressedUntil,
	}
	for _, m := range []*core.MemoryEntry{strong, weak, suppressed} {
		if err := store.UpsertMemory(context.Background(), m); err != nil {
			t.Fatalf("upsert %s: %v", m.ID, err)
		}
	}

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	searcher := NewVectorSearcher(store, provider)
	engine := NewRetrievalEngine(searcher)
	engine.clock = func() time.Time { return now }

	res, err := engine.Retrieve(context.Background(), RetrievalOptions{
		Workspace: "ws",
		Query:     "how should I deploy the orders API safely",
		TopK:      5,
		Mode:      ModeRecall,
		Policy: RetrievalPolicy{
			MinSemanticScore:    floatPtr(0.01),
			MinTotalScore:       floatPtr(0.01),
			RelativeScoreCutoff: floatPtr(0.95),
			WeakSemanticScore:   floatPtr(0.01),
			WeakTotalScore:      floatPtr(0.01),
			WeakRelativeCutoff:  floatPtr(0.01),
		},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("expected strong recall hits")
	}
	if res.Hits[0].Band != BandStrongRecall {
		t.Fatalf("expected strong recall band, got %s", res.Hits[0].Band)
	}
	if len(res.WeakHits) == 0 {
		t.Fatalf("expected weak familiarity hits")
	}
	if res.WeakHits[0].Band != BandWeakFamiliarity {
		t.Fatalf("expected weak familiarity band, got %s", res.WeakHits[0].Band)
	}
	if len(res.SuppressedHits) == 0 {
		t.Fatalf("expected suppressed hits")
	}
	if res.SuppressedHits[0].Band != BandSuppressed {
		t.Fatalf("expected suppressed band, got %s", res.SuppressedHits[0].Band)
	}
	if len(res.SuppressedHits[0].ExclusionReasons) == 0 {
		t.Fatalf("expected suppression reasons")
	}
}

func TestRetrievalEngineRecallCanReturnEmptyWithStrictFloors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "strict.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)
	_, _ = pipe.Write(context.Background(), WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "frontend uses react query for caching",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	})

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := ensureEmbeddingsDir(modelDir); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	searcher := NewVectorSearcher(store, provider)
	engine := NewRetrievalEngine(searcher)
	res, err := engine.Retrieve(context.Background(), RetrievalOptions{
		Workspace: "ws",
		Query:     "investigate payment rollback playbook",
		TopK:      5,
		Mode:      ModeRecall,
		Policy: RetrievalPolicy{
			MinSemanticScore:    floatPtr(0.9),
			MinTotalScore:       floatPtr(0.9),
			RelativeScoreCutoff: floatPtr(0.95),
			WeakSemanticScore:   floatPtr(0.95),
			WeakTotalScore:      floatPtr(0.95),
			WeakRelativeCutoff:  floatPtr(0.95),
		},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("expected strict recall to return no strong hits, got %d", len(res.Hits))
	}
	if len(res.WeakHits) != 0 {
		t.Fatalf("expected strict recall to return no weak hits, got %d", len(res.WeakHits))
	}
	if len(res.SuppressedHits) != 0 {
		t.Fatalf("expected hard semantic floor to exclude suppressed candidates too, got %d", len(res.SuppressedHits))
	}
}

func TestPolicyForModeRuntimeAndRequestOverridePrecedence(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ADAPTIVE_POLICY_RECALL", `{"min_semantic_score":0.44,"min_total_score":0.33}`)

	runtimePolicy := policyForMode(ModeRecall, RetrievalPolicy{})
	if runtimePolicy.MinSemanticScore != 0.44 {
		t.Fatalf("expected runtime min semantic override, got %f", runtimePolicy.MinSemanticScore)
	}
	if runtimePolicy.MinTotalScore != 0.33 {
		t.Fatalf("expected runtime min total override, got %f", runtimePolicy.MinTotalScore)
	}

	explicit := policyForMode(ModeRecall, RetrievalPolicy{
		MinSemanticScore: floatPtr(0.9),
	})
	if explicit.MinSemanticScore != 0.9 {
		t.Fatalf("expected explicit request override to win, got %f", explicit.MinSemanticScore)
	}
	if explicit.MinTotalScore != 0.33 {
		t.Fatalf("expected untouched runtime field to remain, got %f", explicit.MinTotalScore)
	}
}

func TestPolicyForModeFinalDefaultSemanticFloors(t *testing.T) {
	cases := []struct {
		mode RetrievalMode
		want float64
	}{
		{mode: ModeSearch, want: 0.30},
		{mode: ModeRecall, want: 0.25},
		{mode: ModeRelate, want: 0.35},
		{mode: ModeOutcomes, want: 0.15},
	}
	for _, tc := range cases {
		got := policyForMode(tc.mode, RetrievalPolicy{}).MinSemanticScore
		if got != tc.want {
			t.Fatalf("mode %s: expected min semantic floor %.2f, got %.2f", tc.mode, tc.want, got)
		}
	}
}

func floatPtr(v float64) *float64 {
	return &v
}

type negativeVectorProvider struct{}

func (negativeVectorProvider) Name() string         { return "stub-provider" }
func (negativeVectorProvider) ModelVersion() string { return "test-v1" }
func (negativeVectorProvider) Dimension() int       { return 4 }
func (negativeVectorProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{-1, 0, 0, 0}, nil
}
func (negativeVectorProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{-1, 0, 0, 0}
	}
	return out, nil
}

// TestRetrievalClampsNegativeSemanticContribution proves the semantic term is
// clamped to [0,1] before the weighted mix: a negative-cosine hit must not
// drag the total below the sum of the non-semantic signals.
func TestRetrievalClampsNegativeSemanticContribution(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "clamp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	stub := negativeVectorProvider{}
	m := &core.MemoryEntry{
		ID:          "m-neg",
		Type:        core.SemanticMemory,
		Content:     "payment timeout workaround",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}
	if err := store.InsertMemoryByHashWithVector(ctx, m, "hash-neg", "stub-provider", "test-v1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("insert memory: %v", err)
	}

	searcher := &VectorSearcher{store: store, provider: stub, cache: nil}
	eng := NewRetrievalEngine(searcher)
	eng.clock = func() time.Time { return time.Now().UTC() }

	// Loosen the semantic gate so the negative-cosine hit reaches the mixer.
	res, err := eng.Retrieve(ctx, RetrievalOptions{
		Workspace: "ws",
		Query:     "payment timeout workaround",
		TopK:      2,
		Mode:      ModeSearch,
		Policy:    RetrievalPolicy{MinSemanticScore: floatPtr(-1.0)},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatalf("expected at least one hit")
	}
	hit := res.Hits[0]
	if hit.Breakdown.Semantic >= 0 {
		t.Fatalf("expected negative cosine to reach the mixer, got %v", hit.Breakdown.Semantic)
	}
	expected := res.Weights.Recency*hit.Breakdown.Recency +
		res.Weights.Outcome*hit.Breakdown.Outcome +
		res.Weights.Decay*hit.Breakdown.Decay +
		res.Weights.TierBias*hit.Breakdown.TierBias +
		hit.Breakdown.Salience - hit.Breakdown.Suppression
	if math.Abs(hit.Breakdown.Total-expected) >= 1e-9 {
		t.Fatalf("semantic term contributed nonzero drag: total=%v expected-without-semantic=%v", hit.Breakdown.Total, expected)
	}
}
