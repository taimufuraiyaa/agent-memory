package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestGraphRelationshipInferenceAndExpansion(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "graph-test.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Setup local mock provider
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	pipeline := NewWritePipelineWithEmbedder(store, provider)
	workspace := "test-graph-ws"

	// 1. Write first memory (failed attempt)
	failedInput := WriteInput{
		Workspace: workspace,
		Type:      core.OutcomeMemory,
		Content:   "Database migration failed due to lock timeout during deployment",
		Source:    core.MemorySource{Type: core.SourceUserInput, SessionID: "session-123"},
		Entities:  []string{"database", "migration", "deployment"},
		Tags:      []string{"migration", "failed"},
		Outcome: &core.Outcome{
			Result:   core.OutcomeFailure,
			Approach: "direct migration run",
			Reason:   "lock timeout",
		},
	}
	res1, err := pipeline.Write(ctx, failedInput)
	if err != nil {
		t.Fatalf("write failed input: %v", err)
	}

	// 2. Write second memory (success attempt, within same session and 1 hour)
	// Share entity: "database"
	successInput := WriteInput{
		Workspace: workspace,
		Type:      core.OutcomeMemory,
		Content:   "Database migration succeeded by scaling up connection pool size first",
		Source:    core.MemorySource{Type: core.SourceUserInput, SessionID: "session-123"},
		Entities:  []string{"database", "pool", "scaling"},
		Tags:      []string{"migration", "success"},
		Outcome: &core.Outcome{
			Result:   core.OutcomeSuccess,
			Approach: "scale connections then migrate",
			Reason:   "pool scaling resolved locks",
		},
	}
	res2, err := pipeline.Write(ctx, successInput)
	if err != nil {
		t.Fatalf("write success input: %v", err)
	}

	// 3. Verify relations were created
	// Check relations for the failed memory
	rels, err := store.ListRelations(ctx, res1.ID)
	if err != nil {
		t.Fatalf("list relations for res1: %v", err)
	}

	// Should have:
	// - RelLedTo (failed -> success outcome chain)
	// - RelCalls (temporal session link)
	// - RelDependsOn (entity co-occurrence overlap "database")
	hasLedTo := false
	hasCalls := false
	hasDependsOn := false
	for _, r := range rels {
		if r.TargetID == res2.ID {
			if r.Type == core.RelLedTo {
				hasLedTo = true
			}
			if r.Type == core.RelCalls {
				hasCalls = true
			}
			if r.Type == core.RelDependsOn {
				hasDependsOn = true
			}
		}
	}

	if !hasLedTo {
		t.Errorf("expected outcome chain led_to relation from %s to %s", res1.ID, res2.ID)
	}
	if !hasCalls {
		t.Errorf("expected temporal calls relation from %s to %s", res1.ID, res2.ID)
	}
	if !hasDependsOn {
		t.Errorf("expected co-occurrence depends_on relation from %s to %s", res1.ID, res2.ID)
	}

	// 4. Test Graph Expansion Retrieval Mode
	searcher := NewVectorSearcher(store, provider)
	engineInstance := NewRetrievalEngine(searcher)

	// Query that finds res2 first, but should expand to return res1
	graphOptions := RetrievalOptions{
		Workspace: workspace,
		Query:     "migration succeeded pool scaling",
		TopK:      1,
		Mode:      ModeGraphExpand,
		Depth:     2,
	}

	retResult, err := engineInstance.Retrieve(ctx, graphOptions)
	if err != nil {
		t.Fatalf("graph expand retrieve: %v", err)
	}

	// Since res2 is found and res1 is connected via relations, res1 should be returned!
	if len(retResult.Hits) < 2 {
		t.Fatalf("expected at least 2 hits returned under graph expansion, got %d", len(retResult.Hits))
	}

	foundRes1 := false
	for _, h := range retResult.Hits {
		if h.Memory.ID == res1.ID {
			foundRes1 = true
			break
		}
	}
	if !foundRes1 {
		t.Errorf("graph expansion failed to retrieve related memory %s", res1.ID)
	}
}

func TestListWorkspaceRelations(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "relations-test.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Insert mock memories
	m1 := &core.MemoryEntry{
		ID:          "mem-1",
		Workspace:   "ws1",
		Type:        core.SemanticMemory,
		Content:     "Hello memory 1",
		StorageTier: core.TierVector,
	}
	m2 := &core.MemoryEntry{
		ID:          "mem-2",
		Workspace:   "ws1",
		Type:        core.SemanticMemory,
		Content:     "Hello memory 2",
		StorageTier: core.TierVector,
	}
	if err := store.UpsertMemory(ctx, m1); err != nil {
		t.Fatalf("upsert m1: %v", err)
	}
	if err := store.UpsertMemory(ctx, m2); err != nil {
		t.Fatalf("upsert m2: %v", err)
	}

	// Add relation
	err = store.AddRelation(ctx, "mem-1", "mem-2", core.RelCalls, 0.75, map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("add relation: %v", err)
	}

	// Query workspace relations
	edges, err := store.ListWorkspaceRelations(ctx, "ws1")
	if err != nil {
		t.Fatalf("list workspace relations: %v", err)
	}

	if len(edges) != 1 {
		t.Fatalf("expected 1 workspace relation edge, got %d", len(edges))
	}
	if edges[0].SourceID != "mem-1" || edges[0].TargetID != "mem-2" || edges[0].Type != core.RelCalls {
		t.Errorf("unexpected edge details: %+v", edges[0])
	}
	if edges[0].Weight != 0.75 || edges[0].Metadata["foo"] != "bar" {
		t.Errorf("unexpected edge properties: %+v", edges[0])
	}
}
