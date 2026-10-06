package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphRecallSnapshotLoadsActiveRevisionAndReauthorizesCurrentMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	memory := core.MemoryEntry{
		ID: "memory-book", Workspace: scope.WorkspaceID, Type: core.SemanticMemory,
		Content: "Book A", Confidence: 0.9, StorageTier: core.TierVector,
		Source: core.MemorySource{Type: core.SourceUserInput}, CreatedAt: time.Now().UTC(),
	}
	if err := store.UpsertMemory(ctx, &memory); err != nil {
		t.Fatal(err)
	}
	entity, version, evidence := graphEntityFixture("entity-book", "revision-1")
	evidence[0].CanonicalID = memory.ID
	evidence[0].CanonicalFingerprint = core.FingerprintText(memory.Content)
	if err := store.ImportGraphEntity(ctx, entity, version, evidence); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGraphRevision(ctx, core.GraphActivation{Scope: scope, ConfigurationID: "configuration-1", CandidateRevision: "revision-1"}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.LoadActiveGraphSnapshot(ctx, scope, 16, 16, 4)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RevisionID != "revision-1" || len(snapshot.Nodes) != 1 || len(snapshot.Nodes[0].Evidence) != 1 {
		t.Fatalf("active graph snapshot = %#v", snapshot)
	}
	memories, authorized, err := store.ResolveGraphCanonicalMemories(ctx, scope, snapshot.Nodes[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if memories[memory.ID].Content != memory.Content || len(authorized) != 1 {
		t.Fatalf("canonical authorization = memories=%#v authorized=%#v", memories, authorized)
	}

	memory.Content = "Book A corrected"
	if err := store.UpsertMemory(ctx, &memory); err != nil {
		t.Fatal(err)
	}
	memories, authorized, err = store.ResolveGraphCanonicalMemories(ctx, scope, snapshot.Nodes[0].Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 0 || len(authorized) != 0 {
		t.Fatalf("stale evidence was reauthorized: memories=%#v authorized=%#v", memories, authorized)
	}
}

func TestGraphRecallSnapshotRejectsUnboundedLimits(t *testing.T) {
	t.Parallel()
	store := openGraphIndexStore(t)
	_, err := store.LoadActiveGraphSnapshot(context.Background(), core.GraphScope{WorkspaceID: "workspace-a"}, 4097, 1, 0)
	if err == nil {
		t.Fatal("unbounded graph snapshot limit was accepted")
	}
}
