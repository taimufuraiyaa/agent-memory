package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestLifecycleEvictionWritesTombstones(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gap.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipe := NewWritePipeline(store)
	ctx := context.Background()

	w1, _ := pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "Legacy OPS kafka topic config old-topic", Source: core.MemorySource{Type: core.SourceCodeAnalysis}})
	w2, _ := pipe.Write(ctx, WriteInput{Workspace: "ws", Type: core.SemanticMemory, Content: "Another legacy ops topic details old-topic", Source: core.MemorySource{Type: core.SourceCodeAnalysis}})
	if w1 == nil || w2 == nil {
		t.Fatalf("seed writes failed")
	}
	_ = store.SetDecayScores(ctx, map[string]float64{w1.ID: 0.95, w2.ID: 0.95})

	lm := NewLifecycleManager(store, pipe)
	lm.maxEntries = 1
	if _, _, _, err := lm.applyEvictionPromotion(ctx, "ws"); err != nil {
		t.Fatalf("eviction/promotion run: %v", err)
	}

	tombstones, err := store.ListTombstones(ctx, "ws", "")
	if err != nil {
		t.Fatalf("list tombstones: %v", err)
	}
	if len(tombstones) == 0 {
		t.Fatalf("expected tombstones to be written on eviction")
	}
}

func TestGapDetectorTriggeredByTombstones(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gap2.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_ = store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "m1",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "legacy ops kafka old-topic timeout settings",
	}, "evict", "")
	_ = store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "m2",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "legacy ops old-topic retry strategy notes",
	}, "evict", "")

	gd := NewGapDetector(store)
	res, err := gd.Detect(ctx, "ws", "old-topic")
	if err != nil {
		t.Fatalf("detect gap: %v", err)
	}
	if !res.Triggered {
		t.Fatalf("expected gap detector to trigger, score=%f", res.Score)
	}
}

func TestGapDetectorRespectsCooldown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gap3.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_ = store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "m1",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "legacy ops kafka old-topic timeout settings",
	}, "evict", "")
	_ = store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "m2",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "legacy ops old-topic retry strategy notes",
	}, "evict", "")
	// Push cooldown into the future for both rows.
	_ = store.SetTombstoneCooldownForWorkspace(ctx, "ws", time.Now().UTC().Add(24*time.Hour))

	gd := NewGapDetector(store)
	res, err := gd.Detect(ctx, "ws", "old-topic")
	if err != nil {
		t.Fatalf("detect gap: %v", err)
	}
	if res.Triggered {
		t.Fatalf("expected cooldown to suppress triggering")
	}
}
