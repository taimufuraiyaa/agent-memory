package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSkillStandaloneRuntimeDisabledContentionAndWrongDatabase(t *testing.T) {
	leaders := &standaloneLeaderRepository{}
	worker, reconciler := &standaloneWorker{}, &standaloneReconciler{}
	config := standaloneRuntimeTestConfig()
	config.Enabled = false
	runtime, err := NewSkillStandaloneRuntime(leaders, worker, reconciler, config, time.Now)
	if err != nil || runtime.Run(context.Background()) != nil || leaders.acquires != 0 || worker.calls != 0 {
		t.Fatalf("disabled runtime acquired=%d worker=%d err=%v", leaders.acquires, worker.calls, err)
	}

	config.Enabled = true
	leaders.contended = true
	runtime, _ = NewSkillStandaloneRuntime(leaders, worker, reconciler, config, time.Now)
	if err := runtime.Run(context.Background()); !errors.Is(err, ErrSkillStandaloneLeaderContended) {
		t.Fatalf("contention error = %v", err)
	}

	leaders.contended, leaders.err = false, errors.New("wrong database")
	runtime, _ = NewSkillStandaloneRuntime(leaders, worker, reconciler, config, time.Now)
	if err := runtime.Run(context.Background()); err == nil || err.Error() != "wrong database" {
		t.Fatalf("wrong database error = %v", err)
	}
}

func TestSkillStandaloneRuntimeRunsBoundedCyclesDrainsAndRestarts(t *testing.T) {
	leaders := &standaloneLeaderRepository{}
	worker, reconciler := &standaloneWorker{}, &standaloneReconciler{}
	runtime, err := NewSkillStandaloneRuntime(leaders, worker, reconciler, standaloneRuntimeTestConfig(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		worker.mu.Lock()
		worker.calls = 0
		worker.mu.Unlock()
		reconciler.mu.Lock()
		reconciler.calls = 0
		reconciler.mu.Unlock()
		finished := make(chan error, 1)
		go func() { finished <- runtime.Run(context.Background()) }()
		waitForStandaloneCycles(t, worker, reconciler)
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := runtime.Shutdown(shutdown); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
	if leaders.acquires != 2 || leaders.releases != 2 {
		t.Fatalf("leader lifecycle acquired=%d released=%d", leaders.acquires, leaders.releases)
	}
}

func TestSkillStandaloneRuntimeCooperativelyCancelsSlowWorker(t *testing.T) {
	leaders := &standaloneLeaderRepository{}
	worker := &standaloneWorker{block: true, entered: make(chan struct{})}
	runtime, err := NewSkillStandaloneRuntime(leaders, worker, &standaloneReconciler{}, standaloneRuntimeTestConfig(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(context.Background()) }()
	<-worker.entered
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil || leaders.releases != 1 {
		t.Fatalf("slow drain err=%v releases=%d", err, leaders.releases)
	}
}

type standaloneLeaderRepository struct {
	mu        sync.Mutex
	acquires  int
	renewals  int
	releases  int
	contended bool
	err       error
}

func (r *standaloneLeaderRepository) AcquireSkillOrchestratorLeader(context.Context, string, string, string, time.Duration, time.Time) (int64, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.acquires++
	return int64(r.acquires), !r.contended, r.err
}

func (r *standaloneLeaderRepository) RenewSkillOrchestratorLeader(context.Context, string, string, string, int64, time.Duration, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewals++
	return nil
}

func (r *standaloneLeaderRepository) ReleaseSkillOrchestratorLeader(context.Context, string, string, string, int64, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releases++
	return nil
}

type standaloneWorker struct {
	mu      sync.Mutex
	calls   int
	block   bool
	entered chan struct{}
	once    sync.Once
}

func (w *standaloneWorker) RunOnce(ctx context.Context) (SkillWorkerRunReport, error) {
	w.mu.Lock()
	w.calls++
	block, entered := w.block, w.entered
	w.mu.Unlock()
	if block {
		w.once.Do(func() { close(entered) })
		<-ctx.Done()
		return SkillWorkerRunReport{Cancelled: 1}, ctx.Err()
	}
	return SkillWorkerRunReport{}, nil
}

type standaloneReconciler struct {
	mu    sync.Mutex
	calls int
}

func (r *standaloneReconciler) RunOnce(context.Context) (SkillReconciliationReport, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return SkillReconciliationReport{}, nil
}

func standaloneRuntimeTestConfig() SkillStandaloneRuntimeConfig {
	return SkillStandaloneRuntimeConfig{Enabled: true, InstallationID: "installation-1", DatabaseID: "database-1", Owner: "runtime-1",
		PollInterval: 10 * time.Millisecond, ReconciliationInterval: 15 * time.Millisecond,
		LeaderLeaseDuration: time.Second, LeaderRenewalInterval: 20 * time.Millisecond, DrainTimeout: time.Second}
}

func waitForStandaloneCycles(t *testing.T, worker *standaloneWorker, reconciler *standaloneReconciler) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		worker.mu.Lock()
		workerCalls := worker.calls
		worker.mu.Unlock()
		reconciler.mu.Lock()
		reconcilerCalls := reconciler.calls
		reconciler.mu.Unlock()
		if workerCalls > 0 && reconcilerCalls > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("standalone cycles did not start")
}
