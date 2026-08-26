package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphPostgresControlRepositoryIsIdempotentAndLeaseSafe(t *testing.T) {
	ctx, repository, scope, cleanup := graphPostgresFixture(t)
	defer cleanup()
	configuration := hostedGraphConfiguration(scope)
	if err := repository.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	revision := hostedGraphRevision(scope, configuration.ID, uuid.NewString(), core.GraphRevisionReady)
	if err := repository.CreateGraphRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}

	job := hostedGraphJob(scope, configuration.ID, revision.ID, "same-subject")
	first, created, err := repository.EnqueueGraphJob(ctx, job)
	if err != nil || !created {
		t.Fatalf("first enqueue: created=%v err=%v", created, err)
	}
	job.ID = uuid.NewString()
	second, created, err := repository.EnqueueGraphJob(ctx, job)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("duplicate enqueue = %#v created=%v err=%v", second, created, err)
	}

	now := time.Now().UTC()
	claimed, err := repository.ClaimGraphJobs(ctx, scope, "worker-1", 10, time.Minute, now)
	if err != nil || len(claimed) != 1 || claimed[0].Attempt != 1 {
		t.Fatalf("first claim = %#v err=%v", claimed, err)
	}
	claimed, err = repository.ClaimGraphJobs(ctx, scope, "worker-2", 10, time.Minute, now.Add(30*time.Second))
	if err != nil || len(claimed) != 0 {
		t.Fatalf("unexpired claim = %#v err=%v", claimed, err)
	}
	claimed, err = repository.ClaimGraphJobs(ctx, scope, "worker-2", 10, time.Minute, now.Add(2*time.Minute))
	if err != nil || len(claimed) != 1 || claimed[0].Attempt != 2 || claimed[0].LeaseOwner != "worker-2" {
		t.Fatalf("reclaimed job = %#v err=%v", claimed, err)
	}
	if err := repository.CancelGraphJob(ctx, scope, claimed[0].ID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestGraphPostgresActivationHasOneConcurrentWinner(t *testing.T) {
	ctx, repository, scope, cleanup := graphPostgresFixture(t)
	defer cleanup()
	configuration := hostedGraphConfiguration(scope)
	if err := repository.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	first := hostedGraphRevision(scope, configuration.ID, uuid.NewString(), core.GraphRevisionReady)
	if err := repository.CreateGraphRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateGraphRevision(ctx, core.GraphActivation{
		Scope: scope, ConfigurationID: configuration.ID, CandidateRevision: first.ID,
	}); err != nil {
		t.Fatal(err)
	}

	candidates := []core.GraphRevision{
		hostedGraphRevision(scope, configuration.ID, uuid.NewString(), core.GraphRevisionReady),
		hostedGraphRevision(scope, configuration.ID, uuid.NewString(), core.GraphRevisionReady),
	}
	for _, candidate := range candidates {
		if err := repository.CreateGraphRevision(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, len(candidates))
	for _, candidate := range candidates {
		candidate := candidate
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- repository.ActivateGraphRevision(ctx, core.GraphActivation{
				Scope: scope, ConfigurationID: configuration.ID,
				ExpectedRevision: first.ID, CandidateRevision: candidate.ID,
			})
		}()
	}
	wait.Wait()
	close(errorsSeen)
	winners, conflicts := 0, 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrGraphRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected activation error: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
}

func TestGraphPostgresNormalizedImportPreservesReviewAndDeletesDerivedState(t *testing.T) {
	ctx, repository, scope, cleanup := graphPostgresFixture(t)
	defer cleanup()
	configuration := hostedGraphConfiguration(scope)
	if err := repository.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	revision1 := hostedGraphRevision(scope, configuration.ID, uuid.NewString(), core.GraphRevisionReady)
	revision2 := hostedGraphRevision(scope, configuration.ID, uuid.NewString(), core.GraphRevisionReady)
	for _, revision := range []core.GraphRevision{revision1, revision2} {
		if err := repository.CreateGraphRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
	}
	entityIDs := []string{uuid.NewString(), uuid.NewString()}
	for _, entityID := range entityIDs {
		entity, version, evidence := hostedGraphEntity(scope, revision1.ID, entityID)
		if err := repository.ImportGraphEntity(ctx, entity, version, evidence); err != nil {
			t.Fatal(err)
		}
	}
	edge, edgeVersion, edgeEvidence := hostedGraphEdge(scope, revision1.ID, entityIDs)
	if err := repository.ImportGraphEdge(ctx, edge, edgeVersion, edgeEvidence); err != nil {
		t.Fatal(err)
	}
	review := core.GraphReview{
		ID: uuid.NewString(), Scope: scope, TargetKind: "edge", TargetID: edge.ID,
		From: core.GraphTrustProposed, To: core.GraphTrustRejected, ExpectedVersion: 1,
		ReviewerID: uuid.NewString(), Reason: "unsupported inference",
	}
	if err := repository.ReviewGraphRecord(ctx, review); err != nil {
		t.Fatal(err)
	}
	edge.LastRevisionID = revision2.ID
	edgeVersion.RevisionID = revision2.ID
	edgeEvidence[0].ID = uuid.NewString()
	if err := repository.ImportGraphEdge(ctx, edge, edgeVersion, edgeEvidence); err != nil {
		t.Fatal(err)
	}
	queryable, err := repository.ListQueryableGraphEdges(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(queryable) != 0 {
		t.Fatalf("rejected edge was reactivated: %#v", queryable)
	}

	community := core.GraphCommunity{
		ID: uuid.NewString(), Scope: scope, RevisionID: revision2.ID, ExternalID: "community/1",
		Level: 0, EntityCount: 2, SourceCount: 2,
	}
	report := core.GraphReport{
		ID: uuid.NewString(), Scope: scope, CommunityID: community.ID, RevisionID: revision2.ID,
		Title: "Payments", Summary: "Derived context", Findings: []string{"Retry handler"},
		Rank: 0.8, Trust: core.GraphTrustProposed, EvidenceCount: 2,
	}
	if err := repository.ImportGraphCommunity(ctx, community, []contracts.GraphCommunityMember{
		{Kind: "entity", TargetID: entityIDs[0]}, {Kind: "entity", TargetID: entityIDs[1]},
	}, report); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkGraphReportStale(ctx, scope, report.ID); err != nil {
		t.Fatal(err)
	}
	storedReport, err := repository.GraphReport(ctx, scope, report.ID)
	if err != nil || !storedReport.Stale || len(storedReport.Findings) != 1 {
		t.Fatalf("stored report = %#v err=%v", storedReport, err)
	}
	if err := repository.RecordGraphFeedback(ctx, core.GraphFeedback{
		ID: uuid.NewString(), Scope: scope, RequestID: "request-1", TargetKind: "report",
		TargetID: report.ID, Outcome: "helpful",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteGraphWorkspace(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ActiveGraphRevisions(ctx, scope, configuration.ID); err == nil {
		t.Fatal("derived graph configuration remained after workspace graph deletion")
	}
}

func graphPostgresFixture(t *testing.T) (context.Context, *GraphIndexRepository, core.GraphScope, func()) {
	t.Helper()
	connectionURL := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if connectionURL == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	pool, err := Open(ctx, connectionURL)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := Apply(ctx, pool); err != nil {
		pool.Close()
		cancel()
		t.Fatal(err)
	}
	tenantID, workspaceID, accountID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO saas_accounts(id,external_subject,verified_email,state,created_at,updated_at)
		VALUES($1,$2,$3,'active',$4,$4)`, accountID, accountID.String(), accountID.String()+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_tenants(id,kind,state,personal_owner_account_id,created_at,updated_at)
		VALUES($1,'personal','active',$2,$3,$3)`, tenantID, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO saas_workspaces(tenant_id,id,name,state,created_at,updated_at)
		VALUES($1,$2,$3,'active',$4,$4)`, tenantID, workspaceID, "graph-"+workspaceID.String(), now); err != nil {
		t.Fatal(err)
	}
	scope := core.GraphScope{TenantID: tenantID.String(), WorkspaceID: workspaceID.String()}
	return ctx, NewGraphIndexRepository(pool), scope, func() {
		pool.Close()
		cancel()
	}
}

func hostedGraphConfiguration(scope core.GraphScope) core.GraphConfiguration {
	now := time.Now().UTC()
	return core.GraphConfiguration{
		ID: uuid.NewString(), Scope: scope, Version: 1, Enabled: true,
		AdapterName: "agent-memory-graphrag", AdapterVersion: "0.1.0", IndexMethod: core.GraphIndexStandard,
		ProjectionVersion: "projection-v1", ArtifactSchemaVersion: "graph-artifact/v1",
		PromptFingerprint: "sha256:prompts", ModelRoute: "index-text-primary", CreatedAt: now, UpdatedAt: now,
	}
}

func hostedGraphRevision(scope core.GraphScope, configurationID, id string, state core.GraphRevisionState) core.GraphRevision {
	now := time.Now().UTC()
	return core.GraphRevision{
		ID: id, Scope: scope, ConfigurationID: configurationID, State: state,
		Cutoff: core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:cutoff"}, CreatedAt: now, UpdatedAt: now,
	}
}

func hostedGraphJob(scope core.GraphScope, configurationID, revisionID, key string) core.GraphJob {
	now := time.Now().UTC()
	return core.GraphJob{
		ID: uuid.NewString(), Scope: scope, ConfigurationID: configurationID, RevisionID: revisionID,
		IdempotencyKey: key, State: core.GraphJobQueued, CreatedAt: now, UpdatedAt: now,
	}
}

func hostedGraphEntity(scope core.GraphScope, revisionID, entityID string) (core.GraphEntity, core.GraphEntityVersion, []core.GraphEvidence) {
	now := time.Now().UTC()
	entity := core.GraphEntity{
		ID: entityID, Scope: scope, Trust: core.GraphTrustProposed, FirstRevisionID: revisionID,
		LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now,
	}
	version := core.GraphEntityVersion{
		EntityID: entityID, RevisionID: revisionID, ExternalID: "external-" + entityID,
		Name: "Entity", EntityType: "service", Description: "Derived entity", OccurrenceCount: 1,
	}
	evidence := []core.GraphEvidence{{
		ID: uuid.NewString(), Scope: scope, CanonicalKind: "memory", CanonicalID: uuid.NewString(),
		CanonicalFingerprint: "sha256:memory", OccurrenceCount: 1,
	}}
	return entity, version, evidence
}

func hostedGraphEdge(scope core.GraphScope, revisionID string, entityIDs []string) (core.GraphEdge, core.GraphEdgeVersion, []core.GraphEvidence) {
	now := time.Now().UTC()
	edgeID := uuid.NewString()
	edge := core.GraphEdge{
		ID: edgeID, Scope: scope, SourceEntityID: entityIDs[0], TargetEntityID: entityIDs[1],
		NormalizedKind: "depends_on", ExternalKind: "USES", Trust: core.GraphTrustProposed,
		FirstRevisionID: revisionID, LastRevisionID: revisionID, CreatedAt: now, UpdatedAt: now,
	}
	version := core.GraphEdgeVersion{
		EdgeID: edgeID, RevisionID: revisionID, ExternalID: "external-" + edgeID,
		Description: "Entity one uses entity two", Weight: 0.75,
	}
	evidence := []core.GraphEvidence{{
		ID: uuid.NewString(), Scope: scope, CanonicalKind: "passage", CanonicalID: uuid.NewString(),
		CanonicalFingerprint: "sha256:passage", OccurrenceCount: 1,
	}}
	return edge, version, evidence
}
