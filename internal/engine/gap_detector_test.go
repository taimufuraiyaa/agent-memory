package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
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
	if _, _, _, _, err := lm.applyEvictionPromotion(ctx, "ws"); err != nil {
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
	// Fresh tombstones sit in a 7-day cooldown; drive the clock past it so the
	// detector actually considers them.
	gd.clock = func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) }
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

func TestGapDetectorCooldownWindowSkipThenExpire(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gap4.db")
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

	ts, err := store.ListTombstones(ctx, "ws", "")
	if err != nil || len(ts) != 1 {
		t.Fatalf("list tombstones: %v len=%d", err, len(ts))
	}
	cooldownUntil := ts[0].CooldownUntil

	gd := NewGapDetector(store)

	// Inside the cooldown window the skip branch must execute and the
	// tombstone must not be resurrected.
	gd.clock = func() time.Time { return cooldownUntil.Add(-time.Hour) }
	res, err := gd.Detect(ctx, "ws", "old-topic")
	if err != nil {
		t.Fatalf("detect inside cooldown: %v", err)
	}
	if len(res.Tombstones) != 0 || res.Triggered {
		t.Fatalf("expected tombstone suppressed inside cooldown window, got active=%d triggered=%v score=%f", len(res.Tombstones), res.Triggered, res.Score)
	}
	if res.Reason != "all matching tombstones are in cooldown" {
		t.Fatalf("expected cooldown-skip reason, got %q", res.Reason)
	}

	// After the window the tombstone is considered again.
	gd.clock = func() time.Time { return cooldownUntil.Add(time.Hour) }
	res, err = gd.Detect(ctx, "ws", "old-topic")
	if err != nil {
		t.Fatalf("detect after cooldown: %v", err)
	}
	if len(res.Tombstones) != 1 || res.Tombstones[0].MemoryID != "m1" {
		t.Fatalf("expected tombstone considered after cooldown window, got %+v", res.Tombstones)
	}
}

func TestGapDetectorClampsFutureEvictedAt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gap5.db")
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
	// Take the tombstone out of cooldown so only the age path matters.
	_ = store.SetTombstoneCooldownForWorkspace(ctx, "ws", time.Now().UTC().Add(-72*time.Hour))

	gd := NewGapDetector(store)
	// Detector clock in the past => EvictedAt lies in the future => negative age.
	// Without clamping, recency = 1/(1 - 2/30) ≈ 1.07 and score ≈ 0.375.
	gd.clock = func() time.Time { return time.Now().UTC().Add(-48 * time.Hour) }

	res, err := gd.Detect(ctx, "ws", "no-such-topic")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	// Single tombstone, no query match: score == 0.35 * recency. With the age
	// clamp the recency factor is exactly 1, so the score must be exactly 0.35.
	if res.Score < 0.35 || res.Score > 0.35+1e-9 {
		t.Fatalf("expected recency factor <= 1 (score 0.35), got %f", res.Score)
	}
}
