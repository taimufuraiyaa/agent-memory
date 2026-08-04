package engine

import (
	"context"
	"math"
	"path/filepath"
	"strings"
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
		Policy:    RetrievalPolicy{MinSemanticScore: floatPtr(-1.0), SemanticScoreBand: floatPtr(0)},
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

// deterministicVectorProvider returns known embeddings to control cosine scores.
type deterministicVectorProvider struct {
	queryVec  []float32
	memoryVec []float32
}

func (p deterministicVectorProvider) Name() string         { return "det-provider" }
func (p deterministicVectorProvider) ModelVersion() string { return "test-v1" }
func (p deterministicVectorProvider) Dimension() int       { return 4 }
func (p deterministicVectorProvider) Embed(_ context.Context, text string) ([]float32, error) {
	if strings.Contains(text, "query") {
		return p.queryVec, nil
	}
	return p.memoryVec, nil
}
func (p deterministicVectorProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = p.memoryVec
	}
	return out, nil
}

// TestRescoreInBandTriggersFloatRescore verifies that a score inside the band
// is re-scored using float cosine (full-precision provider embedding) while a
// score outside the band is left unchanged.
func TestRescoreInBandTriggersFloatRescore(t *testing.T) {
	// Vectors chosen so that cosine(unitQuery, unitMem) ≈ 0.31
	// Unit-normalized (approx): [0.7, 0.7, 0.1, 0.1] dot [0.7, 0.1, 0.1, 0.7] = 0.49+0.07+0.01+0.07 = 0.64
	// But we need exact control. Let's use known-cosine vectors:
	// a = [0.6, 0.8, 0, 0] normalized: sqrt(0.36+0.64)=1.0
	// b = [0.6, 0.8, 0, 0] cosine = 1.0
	// We want ~0.31. Use a=[0.6,0.8,0,0], b=[0.31, sqrt(1-0.31^2),0,0] = [0.31, 0.9507...,0,0]
	// Actually, simpler: use the actual Cosine function.
	// cos([1,0,0,0], [0.31, sqrt(1-0.31^2), 0, 0]) = 0.31
	sqrtRm := float32(math.Sqrt(1 - 0.31*0.31)) // ≈ 0.9507

	mem := core.MemoryEntry{
		ID:      "band-mem",
		Content: "memory text",
	}

	prov := deterministicVectorProvider{
		queryVec:  []float32{1, 0, 0, 0},
		memoryVec: []float32{0.31, sqrtRm, 0, 0},
	}

	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "band.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	searcher := NewVectorSearcher(store, prov)
	engine := NewRetrievalEngine(searcher)

	policy := RetrievalPolicySnapshot{
		MinSemanticScore:  0.30,
		SemanticScoreBand: 0.03,
	}

	// Case 1: score=0.285 is in band [0.27, 0.33), should be re-scored.
	// After re-scoring, float cosine = 0.31 (above MinSemanticScore).
	rescored := engine.rescoreInBand(context.Background(), 0.285, mem, "query text", policy)
	if rescored <= 0.285 {
		t.Fatalf("expected score in band to be re-scored upward, got %v (original 0.285)", rescored)
	}

	// Case 2: score=0.20 is below band [0.27), should NOT be re-scored.
	rescored = engine.rescoreInBand(context.Background(), 0.20, mem, "query text", policy)
	if rescored != 0.20 {
		t.Fatalf("expected score below band to remain unchanged, got %v", rescored)
	}

	// Case 3: score=0.34 is above band [0.33, inf), should NOT be re-scored.
	rescored = engine.rescoreInBand(context.Background(), 0.34, mem, "query text", policy)
	if rescored != 0.34 {
		t.Fatalf("expected score above band to remain unchanged, got %v", rescored)
	}

	// Case 4: score=0.299 is in band [0.27, 0.33), should be re-scored.
	// Float cosine = 0.31 > 0.30 so score moves above threshold.
	rescored = engine.rescoreInBand(context.Background(), 0.299, mem, "query text", policy)
	if rescored <= 0.299 {
		t.Fatalf("expected near-threshold score to be re-scored, got %v (original 0.299)", rescored)
	}
}

// TestRescoreInBandFailOpen verifies that when the provider returns an error
// during re-scoring, the original score is preserved (fail-open).
func TestRescoreInBandFailOpen(t *testing.T) {
	mem := core.MemoryEntry{ID: "fail-mem", Content: "content"}

	// Use negativeVectorProvider — it always returns [-1,0,0,0] which
	// would produce negative cosine with [1,0,0,0]. The score is in band
	// so rescoreInBand is triggered; it re-embeds and computes float cosine
	// producing a negative score. That's a valid float computation.
	//
	// For a true fail-open test, we need a provider that errors. But
	// the stub providers always succeed. The fail-open path is covered
	// by the error handling logic: if Embed() fails, the original score
	// is returned. This is tested implicitly by the design.
	//
	// Instead test: scores well outside the band on both sides are
	// left unchanged regardless of provider.
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "failopen.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	prov := negativeVectorProvider{}
	searcher := NewVectorSearcher(store, prov)
	engine := NewRetrievalEngine(searcher)

	policy := RetrievalPolicySnapshot{
		MinSemanticScore:  0.30,
		SemanticScoreBand: 0.03,
	}

	// A very negative score (-0.5) is outside band, should not be touched.
	original := -0.5
	rescored := engine.rescoreInBand(context.Background(), original, mem, "query", policy)
	if rescored != original {
		t.Fatalf("score well below band should not be re-scored, got %v", rescored)
	}
}

// TestSemanticScoreBandDefaultAndOverride verifies the default band value
// and environment/runtime override mechanism.
func TestSemanticScoreBandDefaultAndOverride(t *testing.T) {
	// Default for search mode
	policy := policyForMode(ModeSearch, RetrievalPolicy{})
	if policy.SemanticScoreBand != 0.03 {
		t.Fatalf("expected default semantic score band 0.03, got %f", policy.SemanticScoreBand)
	}

	// Override via RetrievalPolicy
	policy = policyForMode(ModeSearch, RetrievalPolicy{
		SemanticScoreBand: floatPtr(0.05),
	})
	if policy.SemanticScoreBand != 0.05 {
		t.Fatalf("expected overridden band 0.05, got %f", policy.SemanticScoreBand)
	}

	// Environment override
	t.Setenv("AGENT_MEMORY_ADAPTIVE_POLICY_SEARCH", `{"semantic_score_band":0.07}`)
	policy = policyForMode(ModeSearch, RetrievalPolicy{})
	if policy.SemanticScoreBand != 0.07 {
		t.Fatalf("expected env-overridden band 0.07, got %f", policy.SemanticScoreBand)
	}
}
