package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillReconcilerRunsEveryRepairDomainWithBoundedRequests(t *testing.T) {
	domains := allTestReconciliationDomains()
	registry := NewSkillReconciliationRegistry()
	seen := make(map[core.SkillReconciliationDomain]SkillReconciliationRequest)
	var mu sync.Mutex
	for _, domain := range domains {
		domain := domain
		if err := registry.Register(domain, SkillReconciliationSweepFunc(func(_ context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
			mu.Lock()
			seen[domain] = request
			mu.Unlock()
			return SkillReconciliationSweepResult{NextCursor: "done-" + string(domain), Complete: true, Counters: core.SkillReconciliationCounters{Scanned: 1, Repaired: 1}}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	repository := newReconcilerRepository()
	reconciler := newTestReconciler(t, repository, registry, domains)
	report, err := reconciler.RunOnce(context.Background())
	if err != nil || len(report.Domains) != len(domains) || report.TimedOut || report.Cancelled {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	for _, domain := range domains {
		request, ok := seen[domain]
		if !ok || request.Limit != 10 || request.ConfigurationVersion != 3 {
			t.Fatalf("domain %s request=%+v", domain, request)
		}
		cursor := repository.cursor(domain)
		if cursor.Cursor != "done-"+string(domain) || cursor.LastCompletedAt.IsZero() || cursor.Counters.Repaired != 1 {
			t.Fatalf("domain %s cursor=%+v", domain, cursor)
		}
	}
}

func TestSkillReconcilerRestartsFromCursorAndIsolatesPartialFailure(t *testing.T) {
	domains := []core.SkillReconciliationDomain{core.SkillReconcileDependencyReadiness, core.SkillReconcileBlockedRechecks}
	repository := newReconcilerRepository()
	repository.cursors[domains[0]] = testReconciliationCursor(domains[0], "page-7", time.Now().UTC().Add(-time.Minute))
	registry := NewSkillReconciliationRegistry()
	_ = registry.Register(domains[0], SkillReconciliationSweepFunc(func(_ context.Context, request SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
		if request.Cursor != "page-7" {
			t.Fatalf("restart cursor=%q", request.Cursor)
		}
		return SkillReconciliationSweepResult{}, errors.New("dependency source unavailable")
	}))
	secondCalled := false
	_ = registry.Register(domains[1], SkillReconciliationSweepFunc(func(context.Context, SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
		secondCalled = true
		return SkillReconciliationSweepResult{NextCursor: "page-8", Counters: core.SkillReconciliationCounters{Scanned: 2, Blocked: 2}}, nil
	}))
	report, err := newTestReconciler(t, repository, registry, domains).RunOnce(context.Background())
	if err != nil || len(report.Domains) != 2 || !report.Domains[0].Failed || report.Domains[0].Code != "sweep_failed" || !secondCalled || report.Domains[1].Failed {
		t.Fatalf("report=%+v second=%v err=%v", report, secondCalled, err)
	}
	if repository.cursor(domains[0]).Cursor != "page-7" || repository.cursor(domains[0]).Counters.Failed != 1 {
		t.Fatalf("failed domain advanced cursor: %+v", repository.cursor(domains[0]))
	}
}

func TestSkillReconcilerHonorsTimeBudgetShutdownAndConcurrentCAS(t *testing.T) {
	domain := core.SkillReconcileLeaseRecovery
	registry := NewSkillReconciliationRegistry()
	_ = registry.Register(domain, SkillReconciliationSweepFunc(func(ctx context.Context, _ SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
		<-ctx.Done()
		return SkillReconciliationSweepResult{}, ctx.Err()
	}))
	repository := newReconcilerRepository()
	reconciler := newTestReconciler(t, repository, registry, []core.SkillReconciliationDomain{domain, core.SkillReconcileTerminalCleanup})
	reconciler.config.TimeBudget = 5 * time.Millisecond
	reconciler.config.DomainTimeout = 5 * time.Millisecond
	report, err := reconciler.RunOnce(context.Background())
	if err != nil || !report.TimedOut || len(report.Domains) != 1 || !report.Domains[0].Failed {
		t.Fatalf("budget report=%+v err=%v", report, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	report, err = newTestReconciler(t, newReconcilerRepository(), NewSkillReconciliationRegistry(), []core.SkillReconciliationDomain{domain}).RunOnce(cancelled)
	if err != nil || !report.Cancelled || len(report.Domains) != 0 {
		t.Fatalf("shutdown report=%+v err=%v", report, err)
	}

	concurrentRegistry := NewSkillReconciliationRegistry()
	barrier := make(chan struct{})
	started := make(chan struct{}, 2)
	_ = concurrentRegistry.Register(domain, SkillReconciliationSweepFunc(func(context.Context, SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
		started <- struct{}{}
		<-barrier
		return SkillReconciliationSweepResult{NextCursor: "next", Complete: true, Counters: core.SkillReconciliationCounters{Scanned: 1, Repaired: 1}}, nil
	}))
	concurrentRepository := newReconcilerRepository()
	workers := []*SkillOrchestratorReconciler{
		newTestReconciler(t, concurrentRepository, concurrentRegistry, []core.SkillReconciliationDomain{domain}),
		newTestReconciler(t, concurrentRepository, concurrentRegistry, []core.SkillReconciliationDomain{domain}),
	}
	reports := make(chan SkillReconciliationReport, 2)
	for _, worker := range workers {
		go func(worker *SkillOrchestratorReconciler) {
			result, _ := worker.RunOnce(context.Background())
			reports <- result
		}(worker)
	}
	<-started
	<-started
	close(barrier)
	first, second := <-reports, <-reports
	failures := 0
	for _, item := range []SkillReconciliationReport{first, second} {
		if item.Domains[0].Failed {
			failures++
		}
	}
	if failures != 1 || concurrentRepository.cursor(domain).Cursor != "next" {
		t.Fatalf("concurrent reports=%+v %+v cursor=%+v", first, second, concurrentRepository.cursor(domain))
	}
}

func TestSkillReconcilerRejectsContentBearingSweepCursor(t *testing.T) {
	domain := core.SkillReconcileMaterializationDrift
	registry := NewSkillReconciliationRegistry()
	_ = registry.Register(domain, SkillReconciliationSweepFunc(func(context.Context, SkillReconciliationRequest) (SkillReconciliationSweepResult, error) {
		return SkillReconciliationSweepResult{NextCursor: "raw\nsecret", Counters: core.SkillReconciliationCounters{Scanned: 1, Repaired: 1}}, nil
	}))
	repository := newReconcilerRepository()
	report, err := newTestReconciler(t, repository, registry, []core.SkillReconciliationDomain{domain}).RunOnce(context.Background())
	if err != nil || len(report.Domains) != 1 || !report.Domains[0].Failed || report.Domains[0].Code != "invalid_sweep_result" || repository.cursor(domain).Cursor != "" {
		t.Fatalf("report=%+v cursor=%+v err=%v", report, repository.cursor(domain), err)
	}
}

func allTestReconciliationDomains() []core.SkillReconciliationDomain {
	return []core.SkillReconciliationDomain{core.SkillReconcileLeaseRecovery, core.SkillReconcileDependencyReadiness,
		core.SkillReconcileLifecycleJobParity, core.SkillReconcileBlockedRechecks, core.SkillReconcileSafetyRollbackParity,
		core.SkillReconcileMaterializationDrift, core.SkillReconcileTerminalCleanup}
}

func newTestReconciler(t *testing.T, repository SkillReconciliationRepository, registry *SkillReconciliationRegistry, domains []core.SkillReconciliationDomain) *SkillOrchestratorReconciler {
	t.Helper()
	reconciler, err := NewSkillOrchestratorReconciler(repository, registry, SkillReconcilerConfig{
		Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, ConfigurationVersion: 3,
		BatchSize: 10, TimeBudget: time.Second, DomainTimeout: 100 * time.Millisecond, Domains: domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

type reconcilerRepository struct {
	mu      sync.Mutex
	cursors map[core.SkillReconciliationDomain]core.SkillReconciliationCursor
}

func newReconcilerRepository() *reconcilerRepository {
	return &reconcilerRepository{cursors: make(map[core.SkillReconciliationDomain]core.SkillReconciliationCursor)}
}

func (r *reconcilerRepository) LoadSkillReconciliationCursor(_ context.Context, scope core.SkillOrchestratorScope, domain core.SkillReconciliationDomain, version int64, now time.Time) (core.SkillReconciliationCursor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cursor, ok := r.cursors[domain]
	if !ok {
		cursor = core.SkillReconciliationCursor{Scope: scope, Domain: domain, ConfigurationVersion: version, UpdatedAt: now}
		r.cursors[domain] = cursor
	}
	return cursor, nil
}

func (r *reconcilerRepository) SaveSkillReconciliationCursor(_ context.Context, input contracts.SkillReconciliationCursorUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.cursors[input.Cursor.Domain]
	if !current.UpdatedAt.Equal(input.ExpectedUpdatedAt) {
		return errors.New("cursor conflict")
	}
	r.cursors[input.Cursor.Domain] = input.Cursor
	return nil
}

func (r *reconcilerRepository) cursor(domain core.SkillReconciliationDomain) core.SkillReconciliationCursor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cursors[domain]
}

func testReconciliationCursor(domain core.SkillReconciliationDomain, value string, now time.Time) core.SkillReconciliationCursor {
	return core.SkillReconciliationCursor{Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		Domain: domain, Cursor: value, ConfigurationVersion: 3, UpdatedAt: now}
}
