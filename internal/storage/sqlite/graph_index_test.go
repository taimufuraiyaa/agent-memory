package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphEntityImportIsIdempotentWithinRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	entity, version, evidence := graphEntityFixture("entity-1", "revision-1")

	if err := store.ImportGraphEntity(ctx, entity, version, evidence); err != nil {
		t.Fatal(err)
	}
	if err := store.ImportGraphEntity(ctx, entity, version, evidence); err != nil {
		t.Fatalf("idempotent re-import: %v", err)
	}

	for table, want := range map[string]int{
		"graph_entities": 1, "graph_entity_versions": 1, "graph_entity_evidence": 1,
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}

func TestGraphEdgeImportRequiresResolvableEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	seedGraphEntities(t, store)
	edge, version, _ := graphEdgeFixture("edge-1", "revision-1")

	err := store.ImportGraphEdge(ctx, edge, version, nil)
	if !errors.Is(err, ErrGraphEvidenceRequired) {
		t.Fatalf("edge without evidence error = %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_edges`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("evidence-free edge was stored: %d", count)
	}
}

func TestGraphCommunityReportPersistsMembershipAndStaleness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	seedGraphEntities(t, store)
	community := core.GraphCommunity{
		ID: "community-1", Scope: core.GraphScope{WorkspaceID: "workspace-a"}, ConfigurationID: "configuration-1", RevisionID: "revision-1",
		ExternalID: "community/1", Level: 0, EntityCount: 2, SourceCount: 2,
		MembershipFingerprint: "sha256:members", EvidenceFingerprint: "sha256:evidence",
	}
	report := core.GraphReport{
		ID: "report-1", Scope: community.Scope, CommunityID: community.ID, RevisionID: community.RevisionID,
		Title: "Payments", Summary: "Derived community context", Findings: []string{"Retry handler"},
		Rank: 0.8, Trust: core.GraphTrustProposed, AdmissionState: core.GraphReportAdmitted, EvidenceCount: 2,
		ModelRoute: "index-model", ModelFingerprint: "sha256:model", PromptFingerprint: "sha256:prompt",
		MembershipFingerprint: community.MembershipFingerprint, EvidenceFingerprint: community.EvidenceFingerprint, ReviewVersion: 3,
	}
	if err := store.ImportGraphCommunity(ctx, community, []GraphCommunityMember{
		{Kind: "entity", TargetID: "entity-1"}, {Kind: "entity", TargetID: "entity-2"},
	}, report); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkGraphReportStale(ctx, community.Scope, report.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GraphReport(ctx, community.Scope, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Stale || len(stored.Findings) != 1 || stored.EvidenceCount != 2 || stored.AdmissionState != core.GraphReportAdmitted ||
		stored.ModelRoute != report.ModelRoute || stored.ModelFingerprint != report.ModelFingerprint || stored.PromptFingerprint != report.PromptFingerprint ||
		stored.MembershipFingerprint != report.MembershipFingerprint || stored.EvidenceFingerprint != report.EvidenceFingerprint || stored.ReviewVersion != report.ReviewVersion {
		t.Fatalf("stored report = %#v", stored)
	}
}

func openGraphIndexStore(t *testing.T) *Store {
	t.Helper()
	store := openGraphControlStore(t)
	ctx := context.Background()
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	for _, revisionID := range []string{"revision-1", "revision-2"} {
		if err := store.CreateGraphRevision(ctx, graphRevisionFixture(revisionID, core.GraphRevisionReady)); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func graphEntityFixture(id, revisionID string) (core.GraphEntity, core.GraphEntityVersion, []core.GraphEvidence) {
	now := time.Now().UTC()
	entity := core.GraphEntity{
		ID: id, Scope: core.GraphScope{WorkspaceID: "workspace-a"}, Trust: core.GraphTrustProposed,
		FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now,
	}
	version := core.GraphEntityVersion{
		EntityID: id, RevisionID: revisionID, ExternalID: "external-" + id,
		Name: "Entity " + id, EntityType: "service", Description: "Derived entity", OccurrenceCount: 1,
	}
	evidence := []core.GraphEvidence{{
		ID: "evidence-" + id + "-" + revisionID, Scope: entity.Scope, CanonicalKind: "memory",
		CanonicalID: "memory-" + id, CanonicalFingerprint: "sha256:" + id, OccurrenceCount: 1,
	}}
	return entity, version, evidence
}

func graphEdgeFixture(id, revisionID string) (core.GraphEdge, core.GraphEdgeVersion, []core.GraphEvidence) {
	now := time.Now().UTC()
	edge := core.GraphEdge{
		ID: id, Scope: core.GraphScope{WorkspaceID: "workspace-a"}, SourceEntityID: "entity-1",
		TargetEntityID: "entity-2", NormalizedKind: "depends_on", ExternalKind: "USES",
		Trust: core.GraphTrustProposed, FirstRevisionID: revisionID, LastRevisionID: revisionID,
		CreatedAt: now, UpdatedAt: now,
	}
	version := core.GraphEdgeVersion{
		EdgeID: id, RevisionID: revisionID, ExternalID: "external-" + id,
		Description: "Entity 1 uses Entity 2", Weight: 0.75,
	}
	evidence := []core.GraphEvidence{{
		ID: "evidence-" + id + "-" + revisionID, Scope: edge.Scope, CanonicalKind: "passage",
		CanonicalID: "passage-1", CanonicalFingerprint: "sha256:passage-1", OccurrenceCount: 1,
	}}
	return edge, version, evidence
}

func seedGraphEntities(t *testing.T, store *Store) {
	t.Helper()
	for _, id := range []string{"entity-1", "entity-2"} {
		entity, version, evidence := graphEntityFixture(id, "revision-1")
		if err := store.ImportGraphEntity(context.Background(), entity, version, evidence); err != nil {
			t.Fatal(err)
		}
	}
}
