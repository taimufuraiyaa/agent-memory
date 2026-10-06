package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestGraphConcurrentReadSeesOnlyCompleteOldOrNewRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "graph-activation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	configuration := core.GraphConfiguration{
		ID: "configuration-1", Scope: scope, Version: 1, Enabled: true, AdapterName: "agent-memory-graphrag",
		AdapterVersion: "0.1.0", IndexMethod: core.GraphIndexStandard, ProjectionVersion: "projection-v1",
		ArtifactSchemaVersion: "graph-artifact/v1", PromptFingerprint: "sha256:prompt", ModelRoute: "graph-index-primary",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	service := application.NewGraphImportService(store)
	for sequence, revisionID := range []string{"revision-old", "revision-new"} {
		revision := core.GraphRevision{ID: revisionID, Scope: scope, ConfigurationID: configuration.ID, State: core.GraphRevisionImporting,
			Cutoff: core.GraphWatermark{Sequence: int64(sequence + 1), EventTime: now.Add(time.Duration(sequence) * time.Hour), Digest: fmt.Sprintf("sha256:cutoff-%d", sequence)}, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateGraphRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
		batch := graphActivationBatch(scope, configuration.ID, revisionID, []string{"edge-" + revisionID}, now)
		if err := service.Import(ctx, application.GraphImportRequest{Batch: batch, EvidenceResolved: true, AdmissionPassed: true, ReviewCarryForwardComplete: true, EvaluationPassed: true}); err != nil {
			t.Fatal(err)
		}
	}
	activation := application.NewGraphActivationService(store)
	if err := activation.Activate(ctx, core.GraphActivation{Scope: scope, ConfigurationID: configuration.ID, CandidateRevision: "revision-old"}); err != nil {
		t.Fatal(err)
	}
	if edges := graphQueryableEdgeIDs(t, store, scope); len(edges) != 1 || edges[0] != "edge-revision-old" {
		t.Fatalf("inactive new revision leaked before activation: %v", edges)
	}

	var wg sync.WaitGroup
	errorsSeen := make(chan error, 128)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			edges, err := store.ListQueryableGraphEdges(ctx, scope)
			if err != nil {
				errorsSeen <- err
				return
			}
			if len(edges) != 1 || (edges[0].ID != "edge-revision-old" && edges[0].ID != "edge-revision-new") {
				errorsSeen <- fmt.Errorf("partial revision observed: %#v", edges)
			}
		}()
	}
	if err := activation.Activate(ctx, core.GraphActivation{Scope: scope, ConfigurationID: configuration.ID, ExpectedRevision: "revision-old", CandidateRevision: "revision-new"}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if err := activation.Rollback(ctx, scope, configuration.ID); err != nil {
		t.Fatal(err)
	}
	if edges := graphQueryableEdgeIDs(t, store, scope); len(edges) != 1 || edges[0] != "edge-revision-old" {
		t.Fatalf("rollback did not restore prior normalized revision: %v", edges)
	}
}

func graphActivationBatch(scope core.GraphScope, configurationID, revisionID string, edgeIDs []string, now time.Time) contracts.GraphRevisionImportBatch {
	entities := make([]contracts.GraphEntityImportRecord, 0, 2)
	for _, id := range []string{"entity-a", "entity-b"} {
		entity := core.GraphEntity{ID: id, Scope: scope, Trust: core.GraphTrustProposed, FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now}
		version := core.GraphEntityVersion{EntityID: id, RevisionID: revisionID, ExternalID: revisionID + "-" + id, Name: id, EntityType: "service", OccurrenceCount: 1}
		evidence := []core.GraphEvidence{{ID: revisionID + "-evidence-" + id, Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-" + id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1}}
		entities = append(entities, contracts.GraphEntityImportRecord{Entity: entity, Version: version, Evidence: evidence})
	}
	edges := make([]contracts.GraphEdgeImportRecord, 0, len(edgeIDs))
	for _, id := range edgeIDs {
		edge := core.GraphEdge{ID: id, Scope: scope, SourceEntityID: "entity-a", TargetEntityID: "entity-b", NormalizedKind: "supports", ExternalKind: "supports", Trust: core.GraphTrustProposed, FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now}
		version := core.GraphEdgeVersion{EdgeID: id, RevisionID: revisionID, ExternalID: revisionID + "-" + id, Description: "supports", Weight: 0.8, Origin: core.GraphRelationshipOriginInferred}
		evidence := []core.GraphEvidence{{ID: revisionID + "-evidence-" + id, Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-edge", CanonicalFingerprint: "sha256:edge", OccurrenceCount: 1}}
		edges = append(edges, contracts.GraphEdgeImportRecord{Edge: edge, Version: version, Evidence: evidence})
	}
	return contracts.GraphRevisionImportBatch{Scope: scope, ConfigurationID: configurationID, RevisionID: revisionID, Entities: entities, Edges: edges, ExpectedEntities: len(entities), ExpectedEdges: len(edges)}
}

func graphQueryableEdgeIDs(t *testing.T, store *sqlite.Store, scope core.GraphScope) []string {
	t.Helper()
	edges, err := store.ListQueryableGraphEdges(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(edges))
	for index, edge := range edges {
		ids[index] = edge.ID
	}
	return ids
}
