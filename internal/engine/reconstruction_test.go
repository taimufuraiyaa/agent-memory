package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestReconstructionEngineWritesReconstructedMemory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reconstruct.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	_ = store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "tm1",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "legacy kafka old-topic config and retries",
	}, "evict", "")
	_ = store.AddTombstone(ctx, core.MemoryEntry{
		ID:        "tm2",
		Workspace: "ws",
		Type:      core.SemanticMemory,
		Content:   "old-topic timeout behavior from previous release",
	}, "evict", "")
	// Fresh tombstones sit in a 7-day cooldown; move them out so the
	// reconstruction path sees them.
	_ = store.SetTombstoneCooldownForWorkspace(ctx, "ws", time.Now().UTC().Add(-time.Hour))

	re := NewReconstructionEngine(store, NewWritePipeline(store))
	out, err := re.Reconstruct(ctx, "ws", "old-topic", true)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !out.Triggered || out.ReconstructedID == "" {
		t.Fatalf("expected reconstructed memory to be written: %+v", out)
	}
	mem, err := store.GetMemory(ctx, out.ReconstructedID)
	if err != nil {
		t.Fatalf("get reconstructed memory: %v", err)
	}
	if mem.Source.Type != core.SourceReconstruction {
		t.Fatalf("expected reconstruction source type, got %s", mem.Source.Type)
	}
	n, err := store.CountReconstructionLineage(ctx, out.ReconstructedID)
	if err != nil {
		t.Fatalf("count reconstruction lineage: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected derived_from lineage links to be persisted")
	}
}

func TestReconstructionEngineRequiresConfirmationAtMediumConfidence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reconstruct-confirm.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_ = store.AddTombstone(ctx, core.MemoryEntry{ID: "a1", Workspace: "ws", Type: core.SemanticMemory, Content: "legacy settings details"}, "evict", "")
	_ = store.AddTombstone(ctx, core.MemoryEntry{ID: "a2", Workspace: "ws", Type: core.SemanticMemory, Content: "legacy fallback settings details"}, "evict", "")
	// Fresh tombstones sit in a 7-day cooldown; move them out so the
	// reconstruction path sees them.
	_ = store.SetTombstoneCooldownForWorkspace(ctx, "ws", time.Now().UTC().Add(-time.Hour))

	re := NewReconstructionEngine(store, NewWritePipeline(store))
	out, err := re.Reconstruct(ctx, "ws", "nonmatching-query", false)
	if err != nil {
		t.Fatalf("reconstruct confirm path: %v", err)
	}
	if !out.RequiresConfirm || out.Triggered {
		t.Fatalf("expected confirmation-required non-write result: %+v", out)
	}
}

func TestReconstructionEngineLoopGuard(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reconstruct-loop.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	pipe := NewWritePipeline(store)
	for i := 0; i < 3; i++ {
		_, _ = pipe.Write(ctx, WriteInput{
			Workspace: "ws",
			Type:      core.SemanticMemory,
			Content:   "Reconstructed memory for query \"ops-topic\" from historical fragments variant " + string(rune('A'+i)),
			Tags:      []string{"reconstructed"},
			Source:    core.MemorySource{Type: core.SourceReconstruction},
			Mode:      ExtractFast,
		})
	}
	_ = store.AddTombstone(ctx, core.MemoryEntry{ID: "t1", Workspace: "ws", Type: core.SemanticMemory, Content: "ops-topic historical note one"}, "evict", "")
	_ = store.AddTombstone(ctx, core.MemoryEntry{ID: "t2", Workspace: "ws", Type: core.SemanticMemory, Content: "ops-topic historical note two"}, "evict", "")

	re := NewReconstructionEngine(store, pipe)
	out, err := re.Reconstruct(ctx, "ws", "ops-topic", true)
	if err != nil {
		t.Fatalf("reconstruct loop guard: %v", err)
	}
	if out.Triggered {
		t.Fatalf("expected loop guard to prevent repeated reconstruction: %+v", out)
	}
}
