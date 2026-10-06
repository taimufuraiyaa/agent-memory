package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphDeletionTombstonePreventsOldArtifactResurrection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	first := graphRevisionFixture("revision-1", core.GraphRevisionImporting)
	if err := store.CreateGraphRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	batch := graphDeletionBatch(first.Scope, first.ConfigurationID, first.ID)
	if err := store.ImportGraphRevisionBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGraphRevision(ctx, core.GraphActivation{Scope: first.Scope, ConfigurationID: first.ConfigurationID, CandidateRevision: first.ID}); err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Now().UTC()
	deletion := contracts.GraphDeletionRequest{Scope: first.Scope, CanonicalKind: "memory", CanonicalID: "memory-delete", DeletedAt: deletedAt, RepairDeadline: deletedAt.Add(30 * time.Minute)}
	impact, err := store.RevokeGraphEvidence(ctx, deletion)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGraphDeletionAndScheduleRepair(ctx, deletion, impact); err != nil {
		t.Fatal(err)
	}
	edges, err := store.ListQueryableGraphEdges(ctx, first.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Fatalf("deleted edge remains queryable: %#v", edges)
	}
	second := graphRevisionFixture("revision-2", core.GraphRevisionImporting)
	if err := store.CreateGraphRevision(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportGraphRevisionBatch(ctx, graphDeletionBatch(second.Scope, second.ConfigurationID, second.ID)); err == nil {
		t.Fatal("old artifact evidence resurrected after deletion tombstone")
	}
}

func graphDeletionBatch(scope core.GraphScope, configurationID, revisionID string) contracts.GraphRevisionImportBatch {
	now := time.Now().UTC()
	entities := make([]contracts.GraphEntityImportRecord, 0, 2)
	for _, id := range []string{"entity-1", "entity-2"} {
		entity := core.GraphEntity{ID: id, Scope: scope, Trust: core.GraphTrustProposed, FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now}
		version := core.GraphEntityVersion{EntityID: id, RevisionID: revisionID, ExternalID: revisionID + "-" + id, Name: id, EntityType: "service", OccurrenceCount: 1}
		evidence := []core.GraphEvidence{{ID: revisionID + "-evidence-" + id, Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-delete", CanonicalFingerprint: "sha256:delete", OccurrenceCount: 1}}
		entities = append(entities, contracts.GraphEntityImportRecord{Entity: entity, Version: version, Evidence: evidence})
	}
	edge := core.GraphEdge{ID: "edge-1", Scope: scope, SourceEntityID: "entity-1", TargetEntityID: "entity-2", NormalizedKind: "supports", Trust: core.GraphTrustProposed, FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now}
	edgeVersion := core.GraphEdgeVersion{EdgeID: edge.ID, RevisionID: revisionID, ExternalID: revisionID + "-edge", Description: "supports", Weight: 0.8}
	edgeEvidence := []core.GraphEvidence{{ID: revisionID + "-edge-evidence", Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-delete", CanonicalFingerprint: "sha256:delete", OccurrenceCount: 1}}
	return contracts.GraphRevisionImportBatch{Scope: scope, ConfigurationID: configurationID, RevisionID: revisionID, Entities: entities, Edges: []contracts.GraphEdgeImportRecord{{Edge: edge, Version: edgeVersion, Evidence: edgeEvidence}}, ExpectedEntities: 2, ExpectedEdges: 1}
}
