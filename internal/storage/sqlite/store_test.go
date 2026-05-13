package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Run migration a second time: should be a no-op.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate second run should be idempotent: %v", err)
	}
}

func TestConcurrentUpserts(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	ctx := context.Background()

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := &core.MemoryEntry{
				ID:          fmt.Sprintf("m_%02d", i),
				Type:        core.SemanticMemory,
				Content:     "concurrent write",
				Workspace:   "ws",
				Source:      core.MemorySource{Type: core.SourceAgentObservation},
				Confidence:  0.9,
				StorageTier: core.TierVector,
			}
			if err := store.UpsertMemory(ctx, entry); err != nil {
				t.Errorf("upsert %d failed: %v", i, err)
			}
		}()
	}
	wg.Wait()

	count, err := store.CountMemories(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 20 {
		t.Fatalf("expected 20 rows, got %d", count)
	}
}
