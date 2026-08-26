package isolationreview

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/objectcustody"
)

func TestGraphTenantIsolationAcrossFullUpdateFailureReviewAndCredentialRevocation(t *testing.T) {
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	tenantA := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	tenantB := core.GraphScope{TenantID: "tenant-b", WorkspaceID: "workspace-b"}

	projectionA := isolatedProjection(t, tenantA, contracts.GraphIndexModeFull, "book a private fact", now)
	projectionB := isolatedProjection(t, tenantB, contracts.GraphIndexModeIncremental, "book b private fact", now)
	if bytes.Contains(projectionA.DocumentsJSONL, []byte("book b")) || bytes.Contains(projectionB.DocumentsJSONL, []byte("book a")) {
		t.Fatal("cross-tenant projection content leak")
	}
	for token := range projectionA.Correlations {
		if _, collision := projectionB.Correlations[token]; collision {
			t.Fatal("correlation token was shared across tenants")
		}
	}

	queue := &isolationGraphQueue{jobs: []graphworker.JobEnvelope{
		isolatedJob(tenantA, "a", contracts.GraphIndexModeFull),
		isolatedJob(tenantB, "b", contracts.GraphIndexModeIncremental),
	}}
	custody := &isolationGraphCustody{authorized: map[core.GraphScope]bool{tenantA: true, tenantB: true}}
	events := &isolationGraphEvents{}
	worker, err := graphworker.New(queue, custody, isolationGraphAdapter{}, events, "graph-worker", time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.RunOnce(context.Background(), 2); err != nil || processed != 2 {
		t.Fatalf("two-tenant indexing failed: processed=%d err=%v", processed, err)
	}
	if len(events.values) != 2 || events.values[0].ArtifactPrefix == events.values[1].ArtifactPrefix {
		t.Fatalf("artifact events were not isolated: %+v", events.values)
	}
	if strings.Contains(events.values[0].ArtifactPrefix, tenantB.TenantID) || strings.Contains(events.values[1].ArtifactPrefix, tenantA.TenantID) {
		t.Fatalf("cross-tenant identifier leaked in artifact prefix: %+v", events.values)
	}

	// Review carry-forward is evaluated independently for each workspace-owned
	// record; no tenant count or record identity is used as a shared cache key.
	for _, suffix := range []string{"a", "b"} {
		entityID := "entity-" + suffix
		carried := application.CarryGraphReviewState(application.GraphReviewCarryRequest{
			Entities: []core.GraphEntity{{ID: entityID, Trust: core.GraphTrustProposed}},
			Previous: []application.GraphReviewedRecord{{TargetKind: "entity", TargetID: entityID, Trust: core.GraphTrustApproved, RecordVersion: 1}},
		})
		if len(carried.Carried) != 1 || carried.Entities[0].Trust != core.GraphTrustApproved {
			t.Fatalf("tenant %s review did not carry independently: %+v", suffix, carried)
		}
	}

	// Revoke the object-read credential for both tenants. The public completion
	// shape and bounded failure code remain identical and contain no content or
	// other-tenant count, while neither request reaches staging.
	custody.authorized[tenantA], custody.authorized[tenantB] = false, false
	queue.jobs = []graphworker.JobEnvelope{
		isolatedJob(tenantA, "revoked-a", contracts.GraphIndexModeIncremental),
		isolatedJob(tenantB, "revoked-b", contracts.GraphIndexModeIncremental),
	}
	events.values = nil
	started := time.Now()
	if processed, err := worker.RunOnce(context.Background(), 2); err != nil || processed != 2 {
		t.Fatalf("credential revocation path failed: processed=%d err=%v", processed, err)
	}
	elapsed := time.Since(started)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("bounded credential-revocation path exceeded approved in-process threshold: %s", elapsed)
	}
	if len(events.values) != 2 || events.values[0].FailureCode != "projection_unavailable" || events.values[1].FailureCode != "projection_unavailable" || events.values[0].ArtifactPrefix != "" || events.values[1].ArtifactPrefix != "" {
		t.Fatalf("credential revocation disclosed asymmetric failure data: %+v", events.values)
	}
	if custody.stagedAfterRevocation != 0 {
		t.Fatal("revoked graph worker credential still wrote artifacts")
	}
}

func isolatedProjection(t *testing.T, scope core.GraphScope, mode contracts.GraphIndexMode, content string, now time.Time) application.GraphProjection {
	t.Helper()
	baseRevisionID := ""
	if mode == contracts.GraphIndexModeIncremental {
		baseRevisionID = "revision-base-" + scope.TenantID
	}
	projection, err := application.NewGraphProjectionBuilder().Build(application.GraphProjectionRequest{
		Scope: scope, ConfigurationID: "configuration", JobID: "job", RevisionID: "revision-" + scope.TenantID,
		Mode: mode, BaseRevisionID: baseRevisionID, ProjectionPolicyVersion: "projection-v1", Cutoff: core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:cutoff"},
		PromptFingerprint: "sha256:prompt", ModelRoutes: []string{"index-model"}, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), ProducerIdentity: "graph-controller",
		Records: []application.GraphProjectionRecord{{ID: "memory", Kind: application.GraphProjectionMemory, Content: content, Fingerprint: core.FingerprintText(content), EventTime: now, Authorized: true, Exportable: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func isolatedJob(scope core.GraphScope, suffix string, mode contracts.GraphIndexMode) graphworker.JobEnvelope {
	job := graphworker.JobEnvelope{Scope: scope, JobID: "job-" + suffix, ConfigurationID: "configuration-" + suffix, RevisionID: "revision-" + suffix, ProjectionRevisionID: "projection-" + suffix, Mode: mode, CreatedAt: time.Now().UTC(), Limits: graphworker.DefaultWorkspaceLimits()}
	if mode == contracts.GraphIndexModeIncremental {
		job.BaseRevisionID = "revision-base-" + suffix
	}
	return job
}

type isolationGraphQueue struct {
	jobs     []graphworker.JobEnvelope
	acked    int
	released int
}

func (q *isolationGraphQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]graphworker.JobEnvelope, error) {
	jobs := append([]graphworker.JobEnvelope(nil), q.jobs...)
	q.jobs = nil
	return jobs, nil
}
func (q *isolationGraphQueue) Ack(context.Context, graphworker.JobEnvelope) error {
	q.acked++
	return nil
}
func (q *isolationGraphQueue) Release(context.Context, graphworker.JobEnvelope, string) error {
	q.released++
	return nil
}

type isolationGraphCustody struct {
	authorized            map[core.GraphScope]bool
	stagedAfterRevocation int
}

func (c *isolationGraphCustody) ReadProjection(_ context.Context, scope core.GraphScope, _ string) ([]byte, []byte, []byte, error) {
	if !c.authorized[scope] {
		return nil, nil, nil, errors.New("object capability unavailable")
	}
	return []byte("projection"), []byte("correlations"), []byte("manifest"), nil
}
func (c *isolationGraphCustody) ReadAdapterState(_ context.Context, scope core.GraphScope, revisionID string) (map[string][]byte, contracts.GraphAdapterStateManifest, error) {
	if !c.authorized[scope] {
		return nil, contracts.GraphAdapterStateManifest{}, errors.New("object capability unavailable")
	}
	files := map[string][]byte{"entities.parquet": []byte("tenant-state")}
	manifest, err := contracts.BuildGraphAdapterStateManifest(scope, revisionID, files, time.Now())
	return files, manifest, err
}
func (c *isolationGraphCustody) Stage(_ context.Context, scope core.GraphScope, jobID, revisionID string, _ map[string][]byte, _ contracts.GraphArtifactManifest) (string, bool, error) {
	if !c.authorized[scope] {
		c.stagedAfterRevocation++
		return "", false, errors.New("object capability unavailable")
	}
	prefix, err := objectcustody.GraphArtifactStagingPrefix(scope, jobID, revisionID)
	return prefix, false, err
}
func (c *isolationGraphCustody) StageAdapterState(_ context.Context, scope core.GraphScope, _ string, _ map[string][]byte, _ contracts.GraphAdapterStateManifest) (bool, error) {
	if !c.authorized[scope] {
		c.stagedAfterRevocation++
		return false, errors.New("object capability unavailable")
	}
	return false, nil
}

type isolationGraphAdapter struct{}

func (isolationGraphAdapter) Index(_ context.Context, request graphworker.AdapterRequest) (graphworker.AdapterResult, error) {
	state := map[string][]byte{"entities.parquet": []byte("state")}
	manifest, err := contracts.BuildGraphAdapterStateManifest(request.Scope, request.RevisionID, state, time.Now())
	return graphworker.AdapterResult{Files: map[string][]byte{"entities.jsonl": []byte("normalized")}, StateFiles: state, StateManifest: manifest}, err
}

type isolationGraphEvents struct{ values []graphworker.CompletionEvent }

func (s *isolationGraphEvents) Emit(_ context.Context, event graphworker.CompletionEvent) (bool, error) {
	s.values = append(s.values, event)
	return true, nil
}
