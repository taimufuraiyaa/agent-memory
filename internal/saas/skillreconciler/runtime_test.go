package skillreconciler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestRuntimeBoundsPartitionsAndIsolatesFailurePauseAndDeletion(t *testing.T) {
	configuration := reconcilerTestConfig()
	repository := &partitionRepository{paused: map[core.SkillOrchestratorScope]bool{configuration.Assignments[2]: true}, deleted: map[core.SkillOrchestratorScope]bool{configuration.Assignments[3]: true}, leases: map[core.SkillOrchestratorScope]string{}}
	factory := &partitionFactory{items: map[core.SkillOrchestratorScope]*partitionReconciler{}}
	for _, scope := range configuration.Assignments {
		factory.items[scope] = &partitionReconciler{}
	}
	factory.items[configuration.Assignments[0]].err = errors.New("one domain failed")
	runtime, err := NewRuntime(repository, factory, configuration, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	report := runtime.RunOnce(context.Background())
	if report.Scanned != 4 || report.Claimed != 2 || report.Completed != 1 || report.Failed != 1 || report.Skipped != 2 {
		t.Fatalf("partition report = %+v", report)
	}
	if factory.items[configuration.Assignments[1]].calls != 1 {
		t.Fatal("one partition failure halted healthy partition")
	}
}

func TestRuntimeCursorRestartAndRoundRobinRemainBounded(t *testing.T) {
	configuration := reconcilerTestConfig()
	configuration.PartitionLimit = 2
	repository := &partitionRepository{paused: map[core.SkillOrchestratorScope]bool{}, deleted: map[core.SkillOrchestratorScope]bool{}, leases: map[core.SkillOrchestratorScope]string{}}
	factory := &partitionFactory{items: map[core.SkillOrchestratorScope]*partitionReconciler{}}
	for _, scope := range configuration.Assignments {
		factory.items[scope] = &partitionReconciler{}
	}
	runtime, _ := NewRuntime(repository, factory, configuration, time.Now)
	first, second := runtime.RunOnce(context.Background()), runtime.RunOnce(context.Background())
	if first.Scanned != 2 || second.Scanned != 2 {
		t.Fatalf("bounded reports first=%+v second=%+v", first, second)
	}
	for _, scope := range configuration.Assignments {
		if factory.items[scope].calls != 1 {
			t.Fatalf("round-robin scope %+v calls=%d", scope, factory.items[scope].calls)
		}
	}
	// A replacement runtime uses the same factory/repository; domain cursor state
	// belongs to the underlying application reconciler, not this advisory iterator.
	restarted, _ := NewRuntime(repository, factory, configuration, time.Now)
	restarted.RunOnce(context.Background())
	if factory.items[configuration.Assignments[0]].calls != 2 {
		t.Fatal("replacement runtime did not resume durable reconciler state")
	}
}

type partitionRepository struct {
	mu      sync.Mutex
	paused  map[core.SkillOrchestratorScope]bool
	deleted map[core.SkillOrchestratorScope]bool
	leases  map[core.SkillOrchestratorScope]string
	fence   int64
}

func (r *partitionRepository) ClaimSkillReconciliationPartition(_ context.Context, scope core.SkillOrchestratorScope, owner string, duration time.Duration, now time.Time) (saaspostgres.SkillReconciliationPartitionLease, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.paused[scope] || r.deleted[scope] || r.leases[scope] != "" {
		return saaspostgres.SkillReconciliationPartitionLease{Scope: scope, RestorePaused: r.paused[scope]}, false, nil
	}
	r.fence++
	r.leases[scope] = owner
	return saaspostgres.SkillReconciliationPartitionLease{Scope: scope, Owner: owner, Fence: r.fence, LeaseExpires: now.Add(duration)}, true, nil
}

func (r *partitionRepository) ReleaseSkillReconciliationPartition(_ context.Context, lease saaspostgres.SkillReconciliationPartitionLease, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leases[lease.Scope] != lease.Owner {
		return errors.New("stale partition")
	}
	delete(r.leases, lease.Scope)
	return nil
}

type partitionFactory struct {
	items map[core.SkillOrchestratorScope]*partitionReconciler
}

func (f *partitionFactory) SkillReconcilerFor(scope core.SkillOrchestratorScope) (Reconciler, error) {
	item, exists := f.items[scope]
	if !exists {
		return nil, errors.New("partition unavailable")
	}
	return item, nil
}

type partitionReconciler struct {
	calls int
	err   error
}

func (r *partitionReconciler) RunOnce(context.Context) (application.SkillReconciliationReport, error) {
	r.calls++
	return application.SkillReconciliationReport{}, r.err
}

func reconcilerTestConfig() RuntimeConfig {
	return RuntimeConfig{Enabled: true, Owner: "reconciler-a", PartitionLimit: 4, LeaseDuration: time.Minute, PollInterval: time.Second,
		Assignments: []core.SkillOrchestratorScope{{TenantID: "tenant-a", WorkspaceID: "workspace-a", Environment: "production"},
			{TenantID: "tenant-b", WorkspaceID: "workspace-b", Environment: "production"},
			{TenantID: "tenant-c", WorkspaceID: "workspace-c", Environment: "production"},
			{TenantID: "tenant-d", WorkspaceID: "workspace-d", Environment: "production"}}}
}
