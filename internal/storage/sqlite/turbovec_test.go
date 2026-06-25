package sqlite

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestTurbovecIndexIntegration(t *testing.T) {
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "agent-memory-turbovec-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Manually force turbovec index use for testing
	store.useTurbovec = true
	store.turbovecIndex = NewTurbovecIndex()

	dim := 384
	rand.Seed(1234)

	// Create test memories with embeddings
	workspace := "test-workspace"
	provider := "test-provider"

	m1 := &core.MemoryEntry{
		ID:        "mem-1",
		Workspace: workspace,
		Type:      core.SemanticMemory,
		Content:   "First memory about compiler optimization",
		Source: core.MemorySource{
			Type: "test",
		},
	}
	v1 := make([]float32, dim)
	for i := 0; i < dim; i++ {
		v1[i] = rand.Float32()
	}

	m2 := &core.MemoryEntry{
		ID:        "mem-2",
		Workspace: workspace,
		Type:      core.SemanticMemory,
		Content:   "Second memory about database indexing",
		Source: core.MemorySource{
			Type: "test",
		},
	}
	v2 := make([]float32, dim)
	for i := 0; i < dim; i++ {
		v2[i] = rand.Float32()
	}

	// Insert memories
	if err := store.UpsertMemory(ctx, m1); err != nil {
		t.Fatalf("failed to insert m1: %v", err)
	}
	if err := store.UpsertMemory(ctx, m2); err != nil {
		t.Fatalf("failed to insert m2: %v", err)
	}

	// Upsert vectors (should update database and turbovecIndex)
	if err := store.UpsertMemoryVector(ctx, m1.ID, workspace, provider, "1.0", v1); err != nil {
		t.Fatalf("failed to upsert v1: %v", err)
	}
	if err := store.UpsertMemoryVector(ctx, m2.ID, workspace, provider, "1.0", v2); err != nil {
		t.Fatalf("failed to upsert v2: %v", err)
	}

	// Verify they are in turbovecIndex
	if _, ok := store.turbovecIndex.Get(m1.ID); !ok {
		t.Errorf("m1 vector not found in turbovec index")
	}
	if _, ok := store.turbovecIndex.Get(m2.ID); !ok {
		t.Errorf("m2 vector not found in turbovec index")
	}

	// Search using query vector close to v1
	query := make([]float32, dim)
	copy(query, v1)
	query[0] += 0.01 // Add minor perturbation

	hits, err := store.SearchMemoryVectorsGo(ctx, workspace, provider, query, 5, nil, nil)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}

	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}

	if hits[0].MemoryID != m1.ID {
		t.Errorf("expected closest match to be m1 (%s), got %s", m1.ID, hits[0].MemoryID)
	}

	// Delete memory and ensure it's removed from turbovec index
	if err := store.DeleteByIDs(ctx, []string{m1.ID}); err != nil {
		t.Fatalf("failed to delete m1: %v", err)
	}

	if _, ok := store.turbovecIndex.Get(m1.ID); ok {
		t.Errorf("m1 vector should have been deleted from turbovec index")
	}
}
