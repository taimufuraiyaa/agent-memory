package graphindex

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
)

type projectionRepositoryFixture struct {
	work             ProjectionWork
	finished, failed int
}

func (r *projectionRepositoryFixture) ScheduleDueGraphWork(context.Context, time.Time) (int, error) {
	return 1, nil
}
func (r *projectionRepositoryFixture) ClaimGraphProjection(context.Context, string, time.Duration, time.Time) (ProjectionWork, bool, error) {
	return r.work, true, nil
}
func (r *projectionRepositoryFixture) FinishGraphProjection(context.Context, ProjectionWork, string, time.Time) error {
	r.finished++
	return nil
}
func (r *projectionRepositoryFixture) FailGraphProjection(context.Context, ProjectionWork, string, time.Time) error {
	r.failed++
	return nil
}

type projectionBundleFixture struct{ puts int }

func (b *projectionBundleFixture) Put(context.Context, core.GraphScope, string, map[string][]byte, application.GraphBundleManifest) (string, error) {
	b.puts++
	return "graph-projections/tenant/workspace/revision/", nil
}

type projectionPublisherFixture struct{ jobs []graphworker.JobEnvelope }

func (p *projectionPublisherFixture) PublishJob(_ context.Context, job graphworker.JobEnvelope) (bool, error) {
	p.jobs = append(p.jobs, job)
	return false, nil
}

func TestHostedGraphDispatcherMaterializesBeforePublishingContentFreeJob(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	repository := &projectionRepositoryFixture{work: ProjectionWork{
		Job:               core.GraphJob{ID: "job-a", Scope: scope, ConfigurationID: "configuration-a", RevisionID: "revision-a", State: core.GraphJobRunning, Attempt: 1, CreatedAt: now, UpdatedAt: now},
		ProjectionVersion: "projection-v1", PromptFingerprint: "sha256:prompt", ModelRoute: "graph-index-v1",
		Cutoff:  core.GraphWatermark{Sequence: 1, EventTime: now, Digest: "sha256:cutoff"},
		Records: []application.GraphProjectionRecord{{ID: "memory-a", Kind: application.GraphProjectionMemory, Content: "Book A", Fingerprint: "sha256:a", EventTime: now, Authorized: true, Exportable: true}},
	}}
	bundles, publisher := &projectionBundleFixture{}, &projectionPublisherFixture{}
	dispatcher, err := NewDispatcher(repository, bundles, publisher, "general-worker", time.Minute, privateKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	processed, err := dispatcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !processed || bundles.puts != 1 || len(publisher.jobs) != 1 || repository.finished != 1 || repository.failed != 0 || publisher.jobs[0].Scope != scope {
		t.Fatalf("incomplete hosted graph dispatch: repo=%#v bundles=%#v jobs=%#v", repository, bundles, publisher.jobs)
	}
}

func TestHostedGraphDispatcherBindsIncrementalJobToImmutableBase(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	repository := &projectionRepositoryFixture{work: ProjectionWork{
		Job:              core.GraphJob{ID: "job-update", Scope: scope, ConfigurationID: "configuration-a", RevisionID: "revision-update", State: core.GraphJobRunning, Attempt: 1, CreatedAt: now, UpdatedAt: now},
		ExpectedRevision: "revision-base", BaseRevisionID: "revision-base", ProjectionVersion: "projection-v1", PromptFingerprint: "sha256:prompt", ModelRoute: "graph-index-v1",
		Cutoff: core.GraphWatermark{Sequence: 2, EventTime: now, Digest: "sha256:update"}, Records: []application.GraphProjectionRecord{{ID: "memory-day-10", Kind: application.GraphProjectionMemory, Content: "Day 10 association", Fingerprint: "sha256:day10", EventTime: now, Authorized: true, Exportable: true}},
	}}
	publisher := &projectionPublisherFixture{}
	dispatcher, err := NewDispatcher(repository, &projectionBundleFixture{}, publisher, "general-worker", time.Minute, privateKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := dispatcher.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("dispatch incremental: %v", err)
	}
	if len(publisher.jobs) != 1 || publisher.jobs[0].Mode != "incremental" || publisher.jobs[0].BaseRevisionID != "revision-base" || publisher.jobs[0].ExpectedRevision != "revision-base" {
		t.Fatalf("incremental base identity missing: %#v", publisher.jobs)
	}
}

func TestHostedGraphProjectionRebuildsBeforeAdapterStateCanExpire(t *testing.T) {
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	if !requiresFullGraphProjection(false, "active-revision", now.Add(-graphAdapterStateRebuildAge), now) {
		t.Fatal("aged active revision did not force a full rebuild before adapter state expiry")
	}
	if requiresFullGraphProjection(false, "active-revision", now.Add(-graphAdapterStateRebuildAge+time.Second), now) {
		t.Fatal("fresh active revision unnecessarily forced a full rebuild")
	}
	if !requiresFullGraphProjection(true, "active-revision", now, now) {
		t.Fatal("canonical deletion did not force a full rebuild")
	}
}
