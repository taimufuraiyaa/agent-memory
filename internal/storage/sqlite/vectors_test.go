package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestUpsertAndListMemoryVectorsByWorkspace(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vectors.db")
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID:          "m1",
		Type:        core.SemanticMemory,
		Content:     "alpha",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	if err := store.UpsertMemoryVector(context.Background(), "m1", "ws", "test-provider", "test-v1", []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatalf("upsert memory vector: %v", err)
	}
	vecs, err := store.ListMemoryVectorsByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list vectors: %v", err)
	}
	got, ok := vecs["m1"]
	if !ok {
		t.Fatalf("expected vector for m1")
	}
	if len(got) != 3 {
		t.Fatalf("expected vector len 3, got %d", len(got))
	}
	rows, err := store.ListMemoryVectorRowsByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list vector rows: %v", err)
	}
	if len(rows) != 1 || rows[0].EmbeddingProvider != "test-provider" {
		t.Fatalf("expected vector provenance to round-trip, got %+v", rows)
	}
	counts, err := store.CountMemoryVectorsByProvider(context.Background(), "ws")
	if err != nil {
		t.Fatalf("count by provider: %v", err)
	}
	if counts["test-provider"] != 1 {
		t.Fatalf("expected provider count for test-provider, got %+v", counts)
	}
}

func TestSearchMemoryVectorsSQL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vectors-search.db")
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{
		ID:          "m1",
		Type:        core.SemanticMemory,
		Content:     "orders",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("upsert m1: %v", err)
	}
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{
		ID:          "m2",
		Type:        core.ProceduralMemory,
		Content:     "deployment",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierMarkdown,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("upsert m2: %v", err)
	}
	if err := store.UpsertMemoryVector(ctx, "m1", "ws", "test-provider", "test-v1", []float32{1, 0, 0}); err != nil {
		t.Fatalf("vector m1: %v", err)
	}
	if err := store.UpsertMemoryVector(ctx, "m2", "ws", "test-provider", "test-v1", []float32{0, 1, 0}); err != nil {
		t.Fatalf("vector m2: %v", err)
	}
	scores, err := store.SearchMemoryVectorsSQL(ctx, "ws", "test-provider", []float32{1, 0, 0}, 5, nil, nil)
	if err != nil {
		t.Fatalf("search vectors sql: %v", err)
	}
	if len(scores) == 0 || scores[0].MemoryID != "m1" {
		t.Fatalf("expected m1 as top result, got %+v", scores)
	}
	filtered, err := store.SearchMemoryVectorsSQL(
		ctx,
		"ws",
		"test-provider",
		[]float32{1, 0, 0},
		5,
		[]core.MemoryType{core.ProceduralMemory},
		[]core.StorageTier{core.TierMarkdown},
	)
	if err != nil {
		t.Fatalf("search vectors sql filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].MemoryID != "m2" {
		t.Fatalf("expected filtered result m2, got %+v", filtered)
	}
	if err := store.UpsertMemoryVector(ctx, "m2", "ws", "legacy-provider", "legacy-v1", []float32{1, 0, 0}); err != nil {
		t.Fatalf("rewrite m2 vector with legacy provider: %v", err)
	}
	legacyFiltered, err := store.SearchMemoryVectorsSQL(ctx, "ws", "test-provider", []float32{1, 0, 0}, 5, nil, nil)
	if err != nil {
		t.Fatalf("search vectors sql with provider filter: %v", err)
	}
	if len(legacyFiltered) != 1 || legacyFiltered[0].MemoryID != "m1" {
		t.Fatalf("expected provider-filtered result to exclude legacy rows, got %+v", legacyFiltered)
	}
}

func TestInsertMemoryByHashWithVector(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vectors-atomic.db")
	store, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	entry := &core.MemoryEntry{
		ID:          "m1",
		Type:        core.SemanticMemory,
		Content:     "OPS consumes orders.events",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}
	if err := store.InsertMemoryByHashWithVector(ctx, entry, "hash-1", "test-provider", "test-v1", []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatalf("insert memory with vector: %v", err)
	}

	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(memories))
	}

	rows, err := store.ListMemoryVectorRowsByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list vector rows: %v", err)
	}
	if len(rows) != 1 || rows[0].MemoryID != "m1" || rows[0].EmbeddingProvider != "test-provider" {
		t.Fatalf("expected vector row for m1, got %+v", rows)
	}

	// Inserting the same content hash again should report ErrDuplicateContent
	// and must not create a second memory or vector row -- the insert and the
	// vector upsert happen in the same transaction, so a duplicate memory
	// insert rolls back the vector write too.
	dup := &core.MemoryEntry{
		ID:          "m2",
		Type:        core.SemanticMemory,
		Content:     "OPS consumes orders.events",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}
	if err := store.InsertMemoryByHashWithVector(ctx, dup, "hash-1", "test-provider", "test-v1", []float32{0.4, 0.5, 0.6}); !errors.Is(err, ErrDuplicateContent) {
		t.Fatalf("expected ErrDuplicateContent, got %v", err)
	}

	memories, err = store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories after dup: %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("expected dup insert to add no memory rows, got %d", len(memories))
	}

	rows, err = store.ListMemoryVectorRowsByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list vector rows after dup: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected dup insert to add no vector rows, got %d", len(rows))
	}
}
