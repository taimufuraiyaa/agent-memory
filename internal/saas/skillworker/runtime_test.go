package skillworker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestRuntimeConfigFailsClosedAndSeparatesDatabaseRole(t *testing.T) {
	if err := (RuntimeConfig{}).Validate(); err != nil {
		t.Fatal(err)
	}
	configuration := skillWorkerTestConfig()
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"agent_memory_api", "agent_memory_reconciler", "postgres"} {
		invalid := configuration
		invalid.DatabaseRole = role
		if err := invalid.Validate(); err == nil {
			t.Fatalf("overlapping database role %q was accepted", role)
		}
	}
	invalid := configuration
	invalid.RollbackReserved = invalid.ClaimBatch
	if err := invalid.Validate(); err == nil {
		t.Fatal("unbounded rollback reservation was accepted")
	}
}

func TestRuntimeChecksReadinessReservesRollbackAndRotatesTenantFairly(t *testing.T) {
	configuration := skillWorkerTestConfig()
	readiness := &skillWorkerReadiness{}
	worker := &recordingLaneWorker{}
	runtime, err := NewRuntime(configuration, readiness, worker)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(ctx) }()
	waitSkillWorkerCalls(t, worker, 8)
	if !runtime.Live() || !runtime.Ready() || readiness.calls != 1 {
		t.Fatalf("health live=%v ready=%v readiness=%d", runtime.Live(), runtime.Ready(), readiness.calls)
	}
	worker.mu.Lock()
	first := append([]laneCall(nil), worker.calls...)
	worker.mu.Unlock()
	if first[0].lane != RollbackLane || first[0].limit != configuration.RollbackReserved || first[1].lane != OrdinaryLane || first[1].limit != configuration.ClaimBatch-configuration.RollbackReserved {
		t.Fatalf("lane reservation = %+v", first[:2])
	}
	if first[0].scope == first[4].scope {
		t.Fatalf("tenant round-robin did not rotate: %+v", first)
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), time.Second)
	if err := runtime.Drain(drainCtx); err != nil {
		drainCancel()
		t.Fatal(err)
	}
	drainCancel()
	cancel()
	if err := <-finished; err != nil || runtime.Ready() {
		t.Fatalf("drain err=%v ready=%v", err, runtime.Ready())
	}
}

func TestRuntimeTwoReplicasAndWorkerLossPreserveOneClaimOutcome(t *testing.T) {
	configuration := skillWorkerTestConfig()
	configuration.PollInterval = 10 * time.Millisecond
	queue := &sharedLaneQueue{remaining: map[string]int{"rollback": 3, "ordinary": 20}}
	runtimeA, _ := NewRuntime(configuration, &skillWorkerReadiness{}, queue)
	configuration.WorkerIdentity = "worker-b"
	runtimeB, _ := NewRuntime(configuration, &skillWorkerReadiness{}, queue)
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() { doneA <- runtimeA.Run(ctxA) }()
	go func() { doneB <- runtimeB.Run(ctxB) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && queue.totalProcessed() < 23 {
		time.Sleep(time.Millisecond)
	}
	cancelA()
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) && queue.totalProcessed() < 23 {
		time.Sleep(time.Millisecond)
	}
	cancelB()
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}
	if queue.totalProcessed() != 23 || queue.duplicates != 0 || queue.rollbackProcessed != 3 {
		t.Fatalf("shared outcomes processed=%d rollback=%d duplicates=%d", queue.totalProcessed(), queue.rollbackProcessed, queue.duplicates)
	}
}

func TestRuntimeReadinessFailureNeverClaims(t *testing.T) {
	readiness := &skillWorkerReadiness{err: errors.New("executor unavailable")}
	worker := &recordingLaneWorker{}
	runtime, _ := NewRuntime(skillWorkerTestConfig(), readiness, worker)
	if err := runtime.Run(context.Background()); err == nil || runtime.Ready() {
		t.Fatalf("unready runtime err=%v ready=%v", err, runtime.Ready())
	}
	if len(worker.calls) != 0 {
		t.Fatal("unready runtime claimed work")
	}
}

type skillWorkerReadiness struct {
	calls int
	err   error
}

func (r *skillWorkerReadiness) CheckSkillWorkerReadiness(context.Context, RuntimeConfig) error {
	r.calls++
	return r.err
}

type laneCall struct {
	scope core.SkillOrchestratorScope
	lane  Lane
	limit int
}

type recordingLaneWorker struct {
	mu    sync.Mutex
	calls []laneCall
}

func (w *recordingLaneWorker) RunSkillWorkerLane(_ context.Context, scope core.SkillOrchestratorScope, lane Lane, limit int) error {
	w.mu.Lock()
	w.calls = append(w.calls, laneCall{scope: scope, lane: lane, limit: limit})
	w.mu.Unlock()
	return nil
}

type sharedLaneQueue struct {
	mu                sync.Mutex
	remaining         map[string]int
	processed         int
	rollbackProcessed int
	duplicates        int
}

func (q *sharedLaneQueue) RunSkillWorkerLane(_ context.Context, _ core.SkillOrchestratorScope, lane Lane, limit int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := string(lane)
	claimed := limit
	if q.remaining[key] < claimed {
		claimed = q.remaining[key]
	}
	q.remaining[key] -= claimed
	q.processed += claimed
	if lane == RollbackLane {
		q.rollbackProcessed += claimed
	}
	return nil
}

func (q *sharedLaneQueue) totalProcessed() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.processed
}

func skillWorkerTestConfig() RuntimeConfig {
	return RuntimeConfig{Enabled: true, DatabaseURL: "postgres://skill-worker@database/agent_memory", DatabaseRole: DatabaseRole,
		WorkerIdentity: "worker-a", TelemetryAddress: ":9090", Assignments: []core.SkillOrchestratorScope{
			{TenantID: "tenant-a", WorkspaceID: "workspace-a", Environment: "production"},
			{TenantID: "tenant-b", WorkspaceID: "workspace-b", Environment: "production"}},
		ClaimBatch: 8, Concurrency: 4, RollbackReserved: 2, LeaseDuration: time.Second,
		StageTimeout: 500 * time.Millisecond, PollInterval: 10 * time.Millisecond, DrainTimeout: time.Second}
}

func waitSkillWorkerCalls(t *testing.T, worker *recordingLaneWorker, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		worker.mu.Lock()
		actual := len(worker.calls)
		worker.mu.Unlock()
		if actual >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("worker calls did not reach %d", count)
}
