package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestComputeDecayScoreDeterministicByType(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	updated := now.Add(-24 * 14 * time.Hour)

	episodic := ComputeDecayScore(now, core.MemoryEntry{Type: core.EpisodicMemory, UpdatedAt: updated})
	semantic := ComputeDecayScore(now, core.MemoryEntry{Type: core.SemanticMemory, UpdatedAt: updated})
	procedural := ComputeDecayScore(now, core.MemoryEntry{Type: core.ProceduralMemory, UpdatedAt: updated})
	if !(episodic > semantic && semantic > procedural) {
		t.Fatalf("expected episodic > semantic > procedural decay; got %f %f %f", episodic, semantic, procedural)
	}
}

func TestComputeDecayScoreBoosts(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	updated := now.Add(-24 * 30 * time.Hour)
	base := ComputeDecayScore(now, core.MemoryEntry{
		Type:      core.SemanticMemory,
		UpdatedAt: updated,
	})
	boosted := ComputeDecayScore(now, core.MemoryEntry{
		Type:        core.SemanticMemory,
		UpdatedAt:   updated,
		AccessCount: 20,
		Pinned:      true,
		Outcome:     &core.Outcome{Result: core.OutcomeSuccess},
	})
	if boosted >= base {
		t.Fatalf("expected boosted memory to decay less; base=%f boosted=%f", base, boosted)
	}
}

func TestDecayEngineUpdateWorkspaceDecay(t *testing.T) {
	store := openDecayStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	entry := &core.MemoryEntry{
		ID:          "m1",
		Type:        core.SemanticMemory,
		Content:     "orders service uses outbox",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		UpdatedAt:   time.Now().UTC().Add(-24 * 10 * time.Hour),
		CreatedAt:   time.Now().UTC().Add(-24 * 10 * time.Hour),
	}
	if err := store.UpsertMemory(ctx, entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	engine := NewDecayEngine(store)
	n, err := engine.UpdateWorkspaceDecay(ctx, "ws")
	if err != nil {
		t.Fatalf("update decay: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected one record updated, got %d", n)
	}
	after, err := store.GetMemory(ctx, "m1")
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	if after.DecayScore <= 0 || after.DecayScore > 1 {
		t.Fatalf("expected bounded decay score in (0,1], got %f", after.DecayScore)
	}
}

func BenchmarkDecayEngineUpdateWorkspaceDecay(b *testing.B) {
	store, cleanup := openDecayStoreBenchmark(b)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		id := "m" + time.Now().Add(time.Duration(i)*time.Nanosecond).Format("150405.000000000")
		_ = store.UpsertMemory(ctx, &core.MemoryEntry{
			ID:          id,
			Type:        core.SemanticMemory,
			Content:     "benchmark content",
			Workspace:   "bench",
			Source:      core.MemorySource{Type: core.SourceAgentObservation},
			StorageTier: core.TierVector,
			Confidence:  0.9,
			UpdatedAt:   time.Now().UTC().Add(-time.Duration(i) * time.Hour),
			CreatedAt:   time.Now().UTC().Add(-time.Duration(i) * time.Hour),
		})
	}

	engine := NewDecayEngine(store)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.UpdateWorkspaceDecay(ctx, "bench"); err != nil {
			b.Fatalf("update decay failed: %v", err)
		}
	}
}

func openDecayStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "decay.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func openDecayStoreBenchmark(b *testing.B) (*sqlite.Store, func()) {
	b.Helper()
	dbPath := filepath.Join(b.TempDir(), "decay-bench.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	return store, func() { _ = store.Close() }
}

// TestDecayAccumulatesAcrossRuns proves the decay age base is stable under
// maintenance: two runs with an advancing clock must strictly increase the
// decay score, and SetDecayScores must never restamp updated_at.
func TestDecayAccumulatesAcrossRuns(t *testing.T) {
	store := openDecayStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	entry := &core.MemoryEntry{
		ID:          "m-acc",
		Type:        core.SemanticMemory,
		Content:     "decay accumulation check",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceAgentObservation},
		StorageTier: core.TierVector,
		Confidence:  0.9,
		CreatedAt:   time.Now().UTC().Add(-48 * time.Hour),
		UpdatedAt:   time.Now().UTC().Add(-48 * time.Hour),
	}
	if err := store.UpsertMemory(ctx, entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// UpsertMemory stamps UpdatedAt; use the stored value as the age base so
	// the engine clock is anchored to the real row.
	mem, err := store.GetMemory(ctx, entry.ID)
	if err != nil {
		t.Fatalf("get memory: %v", err)
	}
	U := mem.UpdatedAt
	if U.IsZero() {
		t.Fatalf("expected upsert to stamp updated_at")
	}

	eng := NewDecayEngine(store)
	eng.clock = func() time.Time { return U.Add(48 * time.Hour) }
	if _, err := eng.UpdateWorkspaceDecay(ctx, "ws"); err != nil {
		t.Fatalf("first decay run: %v", err)
	}
	d1, err := store.GetMemory(ctx, entry.ID)
	if err != nil {
		t.Fatalf("get memory after first run: %v", err)
	}

	eng.clock = func() time.Time { return U.Add(72 * time.Hour) }
	if _, err := eng.UpdateWorkspaceDecay(ctx, "ws"); err != nil {
		t.Fatalf("second decay run: %v", err)
	}
	d2, err := store.GetMemory(ctx, entry.ID)
	if err != nil {
		t.Fatalf("get memory after second run: %v", err)
	}

	if d1.DecayScore <= 0 || d1.DecayScore > 1 {
		t.Fatalf("expected d1 in (0,1], got %f", d1.DecayScore)
	}
	if d2.DecayScore <= 0 || d2.DecayScore > 1 {
		t.Fatalf("expected d2 in (0,1], got %f", d2.DecayScore)
	}
	if d2.DecayScore <= d1.DecayScore {
		t.Fatalf("expected decay to accumulate across runs: d1=%f d2=%f", d1.DecayScore, d2.DecayScore)
	}
	if !d1.UpdatedAt.Equal(U) || !d2.UpdatedAt.Equal(U) {
		t.Fatalf("SetDecayScores must not restamp updated_at: U=%v d1=%v d2=%v", U, d1.UpdatedAt, d2.UpdatedAt)
	}
}
