package engine_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestKnowledgeGraphPreservesConflictAndAuthorization(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner := core.Principal{ID: "owner", Kind: core.PrincipalUser}
	if err := store.PutLibrary(ctx, library.Library{ID: "library", Kind: library.LibraryPersonal, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	policy := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: owner, Visibility: core.VisibilityPrivate}}
	for _, id := range []string{"claim-a", "claim-b", "private-c"} {
		if err := store.PutLibraryResourcePolicy(ctx, library.LibraryResourcePolicy{LibraryID: "library", ResourceType: library.ResourceGraphNode, ResourceID: id, Policy: policy}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	graph := engine.NewKnowledgeGraph(store)
	for _, edge := range []core.KnowledgeEdge{{ID: "supports", FromID: "claim-a", ToID: "claim-b", Kind: core.EdgeSupports, EvidenceCitationIDs: []string{"citation-1"}, Creator: owner, Confidence: .8, ReviewState: core.ReviewProposed, CreatedAt: now, UpdatedAt: now}, {ID: "contradicts", FromID: "claim-a", ToID: "claim-b", Kind: core.EdgeContradicts, EvidenceCitationIDs: []string{"citation-2"}, Creator: owner, Confidence: .7, ReviewState: core.ReviewProposed, CreatedAt: now, UpdatedAt: now}} {
		if err := graph.Put(ctx, edge); err != nil {
			t.Fatal(err)
		}
	}
	scope := core.AuthorizationScope{Principal: owner, Capabilities: []core.Capability{core.CapabilityReadSource}, PolicyVersion: "v1"}
	edges, err := graph.Expand(ctx, scope, "claim-a")
	if err != nil || len(edges) != 2 {
		t.Fatalf("conflicting edges collapsed: %+v err=%v", edges, err)
	}
	peer := scope
	peer.Principal.ID = "peer"
	edges, err = graph.Expand(ctx, peer, "claim-a")
	if err != nil || len(edges) != 0 {
		t.Fatalf("unauthorized traversal leaked: %+v err=%v", edges, err)
	}
}
