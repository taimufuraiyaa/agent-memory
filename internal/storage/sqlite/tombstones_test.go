package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestAddTombstoneSetsSevenDayCooldownWindow(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "tombstones.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "m1",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "legacy ops kafka old-topic timeout settings",
	}, "evict", ""); err != nil {
		t.Fatalf("add tombstone: %v", err)
	}

	ts, err := store.ListTombstones(ctx, "ws", "")
	if err != nil {
		t.Fatalf("list tombstones: %v", err)
	}
	if len(ts) != 1 {
		t.Fatalf("expected 1 tombstone, got %d", len(ts))
	}
	if got := ts[0].CooldownUntil.Sub(ts[0].EvictedAt); got != tombstoneCooldownDuration {
		t.Fatalf("expected cooldown window %v, got %v (evicted=%v cooldown=%v)", tombstoneCooldownDuration, got, ts[0].EvictedAt, ts[0].CooldownUntil)
	}
	if !ts[0].CooldownUntil.After(ts[0].EvictedAt) {
		t.Fatalf("expected cooldown_until to be strictly after evicted_at")
	}
}

func TestListTombstonesTreatsLegacyCooldownAsEvictedAt(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "tombstones_legacy.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Rows written before the cooldown-window fix store cooldown_until equal to
	// evicted_at (old bug), or an empty value. They must be treated as
	// evicted_at, i.e. immediately out of cooldown.
	evictedAt := time.Now().UTC()
	legacyCooldowns := []string{evictedAt.Format(time.RFC3339Nano), ""}
	for i, legacyCooldown := range legacyCooldowns {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO memory_tombstones (id, memory_id, workspace, type, entity_hash, fragment_summary, eviction_reason, lineage_memory_id, evicted_at, cooldown_until)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"legacy-"+string(rune('0'+i)), "m"+string(rune('0'+i)), "ws", string(core.SemanticMemory), "", "old content", "evict", "",
			evictedAt.Format(time.RFC3339Nano), legacyCooldown,
		); err != nil {
			t.Fatalf("insert legacy row %d: %v", i, err)
		}
	}

	ts, err := store.ListTombstones(ctx, "ws", "")
	if err != nil {
		t.Fatalf("list tombstones: %v", err)
	}
	if len(ts) != 2 {
		t.Fatalf("expected 2 tombstones, got %d", len(ts))
	}
	for _, row := range ts {
		if row.CooldownUntil.IsZero() || !row.CooldownUntil.Equal(row.EvictedAt) {
			t.Fatalf("expected legacy cooldown treated as evicted_at, got cooldown=%v evicted=%v", row.CooldownUntil, row.EvictedAt)
		}
	}
}
