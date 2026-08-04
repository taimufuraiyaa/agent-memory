package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestConsolidationCreatesSummaryAndLineage(t *testing.T) {
	store := openLifecycleStore(t)
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.EpisodicMemory, Content: "Fixed retry bug in payment timeout handler", Source: core.MemorySource{Type: core.SourceAgentObservation}})
	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.EpisodicMemory, Content: "Retry bug in payment handler fixed by exponential backoff", Source: core.MemorySource{Type: core.SourceAgentObservation}})

	c := NewConsolidationEngine(store, pipe)
	ids, err := c.Run(ctx, "ws", MergeFast)
	if err != nil {
		t.Fatalf("consolidation run: %v", err)
	}
	if len(ids) == 0 {
		t.Fatalf("expected consolidated summaries")
	}
}

func TestConflictResolutionMarksSuperseded(t *testing.T) {
	store := openLifecycleStore(t)
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	first, _ := pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "Orders service does not publish order.created", Source: core.MemorySource{Type: core.SourceCodeAnalysis}})
	second, _ := pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "Orders service publishes order.created", Source: core.MemorySource{Type: core.SourceCodeAnalysis}})
	if first == nil || second == nil {
		t.Fatalf("seed writes failed")
	}
	engine := NewConflictEngine(store)
	out, err := engine.Resolve(ctx, "ws")
	if err != nil {
		t.Fatalf("resolve conflicts: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("expected at least one conflict")
	}
}

func TestLifecycleManagerRun(t *testing.T) {
	store := openLifecycleStore(t)
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.OutcomeMemory, Content: "Backoff retries resolved timeout", Source: core.MemorySource{Type: core.SourceAgentObservation}, Outcome: &core.Outcome{Result: core.OutcomeSuccess}})
	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.EpisodicMemory, Content: "Investigated timeout and traced root cause", Source: core.MemorySource{Type: core.SourceAgentObservation}})
	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.EpisodicMemory, Content: "Traced timeout root cause and fixed with retries", Source: core.MemorySource{Type: core.SourceAgentObservation}})

	lm := NewLifecycleManager(store, pipe)
	metrics, err := lm.Run(ctx, "ws")
	if err != nil {
		t.Fatalf("lifecycle run: %v", err)
	}
	if metrics.DecayUpdated == 0 {
		t.Fatalf("expected decay updates")
	}
}

func TestLifecycleTierRebalanceDemotesMarkdownByBudget(t *testing.T) {
	store := openLifecycleStore(t)
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.ProceduralMemory, Content: "one two three four five six seven eight", Source: core.MemorySource{Type: core.SourceAgentObservation}})
	_, _ = pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.ProceduralMemory, Content: "nine ten eleven twelve thirteen fourteen", Source: core.MemorySource{Type: core.SourceAgentObservation}})

	lm := NewLifecycleManager(store, pipe)
	lm.markdownBudget = 6
	metrics, err := lm.Run(ctx, "ws")
	if err != nil {
		t.Fatalf("lifecycle run: %v", err)
	}
	if metrics.Demoted == 0 {
		t.Fatalf("expected markdown demotion due to budget pressure")
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	logged := 0
	for _, m := range memories {
		n, _ := store.CountTierTransitions(ctx, m.ID)
		if n > 0 {
			logged++
		}
	}
	if logged == 0 {
		t.Fatalf("expected tier transition audit logs to be written")
	}
}

func TestLifecycleTierRebalanceKeepsPinnedMarkdown(t *testing.T) {
	store := openLifecycleStore(t)
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	pinned, _ := pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "critical rule must stay pinned in markdown tier",
		Tags:      []string{"pinned"},
		Source:    core.MemorySource{Type: core.SourceUserInput},
	})
	if pinned == nil {
		t.Fatalf("expected pinned write result")
	}
	_, _ = pipe.Write(ctx, WriteInput{
		Workspace: "ws",
		Type:      core.ProceduralMemory,
		Content:   "another markdown memory likely to be demoted first under pressure",
		Source:    core.MemorySource{Type: core.SourceAgentObservation},
	})

	lm := NewLifecycleManager(store, pipe)
	lm.markdownBudget = 4
	if _, err := lm.Run(ctx, "ws"); err != nil {
		t.Fatalf("lifecycle run: %v", err)
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	pinnedStillMarkdown := false
	for _, m := range memories {
		if m.ID == pinned.ID {
			pinnedStillMarkdown = (m.StorageTier == core.TierMarkdown)
		}
	}
	if !pinnedStillMarkdown {
		t.Fatalf("pinned markdown memory should not be demoted")
	}
}

func openLifecycleStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "lifecycle.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

// TestEvictionPrefersMostDecayed proves eviction picks the most-decayed
// unpinned memory first, and that pinned/low-decay memories survive.
// It drives applyEvictionPromotion directly because Run() recomputes decay
// first, which would erase the explicitly seeded decay scores.
func TestEvictionPrefersMostDecayed(t *testing.T) {
	store := openLifecycleStore(t)
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	entries := []*core.MemoryEntry{
		{ID: "m1", Type: core.SemanticMemory, Content: "pinned memory", Workspace: "ws", Source: core.MemorySource{Type: core.SourceAgentObservation}, StorageTier: core.TierVector, Pinned: true, Confidence: 0.9},
		{ID: "m2", Type: core.SemanticMemory, Content: "most decayed memory", Workspace: "ws", Source: core.MemorySource{Type: core.SourceAgentObservation}, StorageTier: core.TierVector, DecayScore: 0.9, Confidence: 0.9},
		{ID: "m3", Type: core.SemanticMemory, Content: "fresh memory", Workspace: "ws", Source: core.MemorySource{Type: core.SourceAgentObservation}, StorageTier: core.TierVector, DecayScore: 0.2, Confidence: 0.9},
	}
	for _, mm := range entries {
		if err := store.InsertMemoryByHash(ctx, mm, "hash-"+mm.ID); err != nil {
			t.Fatalf("insert %s: %v", mm.ID, err)
		}
	}

	lm := NewLifecycleManager(store, pipe)
	lm.maxEntries = 2
	lm.summarizer = nil // keep the eviction path focused

	evicted, _, _, _, err := lm.applyEvictionPromotion(ctx, "ws")
	if err != nil {
		t.Fatalf("eviction run: %v", err)
	}
	if evicted != 1 {
		t.Fatalf("expected exactly 1 eviction, got %d", evicted)
	}
	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	byID := make(map[string]core.MemoryEntry, len(memories))
	for _, mm := range memories {
		byID[mm.ID] = mm
	}
	if _, ok := byID["m2"]; ok {
		t.Fatalf("expected most-decayed memory m2 to be evicted")
	}
	if _, ok := byID["m1"]; !ok {
		t.Fatalf("expected pinned memory m1 to remain")
	}
	if _, ok := byID["m3"]; !ok {
		t.Fatalf("expected low-decay memory m3 to remain")
	}
	tombstones, err := store.ListTombstones(ctx, "ws", "")
	if err != nil {
		t.Fatalf("list tombstones: %v", err)
	}
	found := false
	for _, tb := range tombstones {
		if tb.MemoryID == "m2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a tombstone for evicted memory m2")
	}
}
