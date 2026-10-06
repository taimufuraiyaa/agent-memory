package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestGraphRecallLocalAddsAuthorizedRelatedMemoryWithPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "graph-recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	book := graphRecallMemory("memory-book", "Book A", scope.WorkspaceID)
	chapter := graphRecallMemory("memory-day-10", "Day ten chapter belongs to Book A", scope.WorkspaceID)
	for _, memory := range []*core.MemoryEntry{&book, &chapter} {
		if err := store.UpsertMemory(ctx, memory); err != nil {
			t.Fatal(err)
		}
	}
	evidence := func(memory core.MemoryEntry) core.GraphEvidence {
		return core.GraphEvidence{Scope: scope, CanonicalKind: "memory", CanonicalID: memory.ID, CanonicalFingerprint: core.FingerprintText(memory.Content), OccurrenceCount: 1}
	}
	snapshot := contracts.GraphQuerySnapshot{Scope: scope, RevisionID: "revision-1", Fresh: true,
		Nodes: []contracts.GraphQueryNode{
			{Entity: core.GraphEntity{ID: "entity-book", Trust: core.GraphTrustApproved}, Evidence: []core.GraphEvidence{evidence(book)}},
			{Entity: core.GraphEntity{ID: "entity-chapter", Trust: core.GraphTrustReviewed}, Evidence: []core.GraphEvidence{evidence(chapter)}},
		},
		Edges: []contracts.GraphQueryEdge{{
			Edge:    core.GraphEdge{ID: "edge-membership", SourceEntityID: "entity-chapter", TargetEntityID: "entity-book", NormalizedKind: string(core.GraphRelationshipMembership), Trust: core.GraphTrustReviewed},
			Version: core.GraphEdgeVersion{Weight: 0.9}, Evidence: []core.GraphEvidence{evidence(chapter)},
		}},
	}
	service := NewMemoryService(store, nil, nil)
	hits, graphContext, err := service.enrichRecallWithGraph(ctx, "How is the chapter related to Book A?", []engine.RetrievalHit{{Memory: chapter, Score: 0.9, Band: engine.BandStrongRecall}}, snapshot, graphretrieval.GraphQueryLocal)
	if err != nil {
		t.Fatal(err)
	}
	if graphContext.Local == nil || len(graphContext.Local.Paths) != 1 || len(hits) != 2 {
		t.Fatalf("local graph recall = hits=%#v context=%#v", hits, graphContext)
	}
	foundBook := false
	for _, hit := range hits {
		foundBook = foundBook || hit.Memory.ID == book.ID
	}
	if !foundBook {
		t.Fatalf("related canonical memory was not added: %#v", hits)
	}
}

func graphRecallMemory(id, content, workspace string) core.MemoryEntry {
	return core.MemoryEntry{ID: id, Type: core.SemanticMemory, Content: content, Workspace: workspace, Confidence: 0.9, StorageTier: core.TierVector, Source: core.MemorySource{Type: core.SourceUserInput}, CreatedAt: time.Now().UTC()}
}
