package graphworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type graphWorkerQueue struct {
	jobs            []JobEnvelope
	acked, released int
}

func (q *graphWorkerQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]JobEnvelope, error) {
	return append([]JobEnvelope(nil), q.jobs...), nil
}
func (q *graphWorkerQueue) Ack(context.Context, JobEnvelope) error { q.acked++; return nil }
func (q *graphWorkerQueue) Release(context.Context, JobEnvelope, string) error {
	q.released++
	return nil
}

type graphWorkerCustody struct {
	reads, stages, stateReads, stateStages int
	prefix                                 string
}

func (c *graphWorkerCustody) ReadProjection(context.Context, core.GraphScope, string) ([]byte, []byte, []byte, error) {
	c.reads++
	return []byte("projection"), []byte("correlations"), []byte("manifest"), nil
}
func (c *graphWorkerCustody) ReadAdapterState(_ context.Context, scope core.GraphScope, revision string) (map[string][]byte, contracts.GraphAdapterStateManifest, error) {
	c.stateReads++
	files := map[string][]byte{"entities.parquet": []byte("state")}
	manifest, err := contracts.BuildGraphAdapterStateManifest(scope, revision, files, time.Now())
	return files, manifest, err
}
func (c *graphWorkerCustody) Stage(context.Context, core.GraphScope, string, string, map[string][]byte, contracts.GraphArtifactManifest) (string, bool, error) {
	c.stages++
	return c.prefix, c.stages > 1, nil
}
func (c *graphWorkerCustody) StageAdapterState(context.Context, core.GraphScope, string, map[string][]byte, contracts.GraphAdapterStateManifest) (bool, error) {
	c.stateStages++
	return c.stateStages > 1, nil
}

type graphWorkerAdapter struct {
	result AdapterResult
	err    error
}

func (a graphWorkerAdapter) Index(context.Context, AdapterRequest) (AdapterResult, error) {
	return a.result, a.err
}

type graphWorkerEvents struct {
	ids       map[string]struct{}
	failFirst bool
}

func (s *graphWorkerEvents) Emit(_ context.Context, event CompletionEvent) (bool, error) {
	if s.failFirst {
		s.failFirst = false
		return false, errors.New("event outage")
	}
	if s.ids == nil {
		s.ids = map[string]struct{}{}
	}
	_, exists := s.ids[event.ID]
	s.ids[event.ID] = struct{}{}
	return !exists, nil
}

func TestGraphWorkerAtLeastOnceReplayIsIdempotent(t *testing.T) {
	job := graphWorkerJob()
	queue := &graphWorkerQueue{jobs: []JobEnvelope{job}}
	custody := &graphWorkerCustody{prefix: "graph-artifacts/staging/tenant-a/workspace-a/job-a/revision-a/"}
	events := &graphWorkerEvents{}
	worker, err := New(queue, custody, graphWorkerAdapter{result: AdapterResult{Files: map[string][]byte{"entities.jsonl": []byte("x")}}}, events, "worker-a", time.Minute, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if queue.acked != 2 || custody.stages != 2 || len(events.ids) != 1 {
		t.Fatalf("replay not idempotent: ack=%d stages=%d events=%d", queue.acked, custody.stages, len(events.ids))
	}
}

func TestGraphWorkerLossLeavesLeaseReclaimable(t *testing.T) {
	job := graphWorkerJob()
	queue := &graphWorkerQueue{jobs: []JobEnvelope{job}}
	events := &graphWorkerEvents{failFirst: true}
	worker, _ := New(queue, &graphWorkerCustody{prefix: "prefix/"}, graphWorkerAdapter{}, events, "worker-a", time.Second, time.Now)
	if _, err := worker.RunOnce(context.Background(), 1); err == nil {
		t.Fatal("event outage should interrupt before acknowledgement")
	}
	if queue.acked != 0 || queue.released != 0 {
		t.Fatalf("lost worker job was prematurely finalized: %#v", queue)
	}
	if _, err := worker.RunOnce(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if queue.acked != 1 {
		t.Fatal("reclaimed job was not acknowledged")
	}
}

func TestGraphWorkerRejectsCrossWorkspaceReplayBeforeObjectRead(t *testing.T) {
	job := graphWorkerJob()
	job.ProjectionRevisionID = "../tenant-b"
	queue := &graphWorkerQueue{jobs: []JobEnvelope{job}}
	custody := &graphWorkerCustody{}
	worker, _ := New(queue, custody, graphWorkerAdapter{}, &graphWorkerEvents{}, "worker-a", time.Minute, time.Now)
	if _, err := worker.RunOnce(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if custody.reads != 0 || queue.released != 1 {
		t.Fatalf("forged job reached object custody: reads=%d released=%d", custody.reads, queue.released)
	}
}

func graphWorkerJob() JobEnvelope {
	return JobEnvelope{Scope: core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, JobID: "job-a", ConfigurationID: "configuration-a", RevisionID: "revision-a", ProjectionRevisionID: "projection-a", Mode: contracts.GraphIndexModeFull, CreatedAt: time.Now().UTC(), Limits: DefaultWorkspaceLimits()}
}

type graphWorkerCountingAdapter struct{ calls int }

func (a *graphWorkerCountingAdapter) Index(context.Context, AdapterRequest) (AdapterResult, error) {
	a.calls++
	return AdapterResult{}, nil
}

func TestGraphWorkerRejectsWorkspaceLimitBeforeAdapterModelCall(t *testing.T) {
	job := graphWorkerJob()
	job.Limits.MaxPendingRecords = 0
	queue := &graphWorkerQueue{jobs: []JobEnvelope{job}}
	custody := &graphWorkerCustody{}
	adapter := &graphWorkerCountingAdapter{}
	worker, _ := New(queue, custody, adapter, &graphWorkerEvents{}, "worker-a", time.Minute, time.Now)
	if _, err := worker.RunOnce(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 0 || custody.reads != 0 || queue.released != 1 {
		t.Fatalf("invalid limits crossed preflight boundary: adapter=%d reads=%d releases=%d", adapter.calls, custody.reads, queue.released)
	}

	job = graphWorkerJob()
	job.Limits.MaxPendingRecords = 1
	custody = &graphWorkerCustody{}
	adapter = &graphWorkerCountingAdapter{}
	queue = &graphWorkerQueue{jobs: []JobEnvelope{job}}
	worker, _ = New(queue, custody, adapter, &graphWorkerEvents{}, "worker-a", time.Minute, time.Now)
	if _, err := worker.RunOnce(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 {
		t.Fatalf("admitted job did not reach adapter exactly once: %d", adapter.calls)
	}
}
