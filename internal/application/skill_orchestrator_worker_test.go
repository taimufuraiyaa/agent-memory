package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillOrchestratorWorkerRunOnceFinalizesFencedSuccess(t *testing.T) {
	now := time.Now().UTC()
	job := runningWorkerJob(now, "job-success")
	repository := &workerRepository{claimed: []core.SkillJob{job}}
	registry := NewSkillStageRegistry()
	if err := registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(context.Context, core.SkillJob) (SkillStageResult, error) {
		return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceCandidate, ID: "candidate-1"}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	worker := newTestSkillWorker(t, repository, registry)

	report, err := worker.RunOnce(context.Background())
	if err != nil || report.Claimed != 1 || report.Completed != 1 || len(repository.finalized) != 1 {
		t.Fatalf("report=%+v finalized=%+v err=%v", report, repository.finalized, err)
	}
	finalized := repository.finalized[0]
	if finalized.Owner != "worker-1" || finalized.Fence != job.Fence || finalized.ExpectedWorkflowGeneration != 1 {
		t.Fatalf("finalization lost fence binding: %+v", finalized)
	}
}

func TestSkillOrchestratorWorkerLeaseRenewalLossCancelsWithoutFinalize(t *testing.T) {
	now := time.Now().UTC()
	job := runningWorkerJob(now, "job-lease-loss")
	repository := &workerRepository{claimed: []core.SkillJob{job}, renewErr: errors.New("stale lease")}
	registry := NewSkillStageRegistry()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	_ = registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(ctx context.Context, _ core.SkillJob) (SkillStageResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return SkillStageResult{}, ctx.Err()
	}))
	worker := newTestSkillWorker(t, repository, registry)
	worker.config.RenewalInterval = 5 * time.Millisecond

	report, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	<-cancelled
	if report.LeaseLost != 1 || len(repository.finalized) != 0 || repository.renewals.Load() == 0 {
		t.Fatalf("report=%+v finalized=%d renewals=%d", report, len(repository.finalized), repository.renewals.Load())
	}
}

func TestSkillOrchestratorWorkerRenewsLongRunningStageBeforeFinalize(t *testing.T) {
	now := time.Now().UTC()
	job := runningWorkerJob(now, "job-renew-success")
	repository := &workerRepository{claimed: []core.SkillJob{job}}
	registry := NewSkillStageRegistry()
	_ = registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(context.Context, core.SkillJob) (SkillStageResult, error) {
		time.Sleep(15 * time.Millisecond)
		return SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
	}))
	worker := newTestSkillWorker(t, repository, registry)
	worker.config.RenewalInterval = 5 * time.Millisecond
	report, err := worker.RunOnce(context.Background())
	if err != nil || report.Completed != 1 || repository.renewals.Load() < 1 {
		t.Fatalf("report=%+v renewals=%d err=%v", report, repository.renewals.Load(), err)
	}
}

func TestSkillOrchestratorWorkerRejectsUnsupportedAndInvalidContracts(t *testing.T) {
	now := time.Now().UTC()
	unsupported := runningWorkerJob(now, "job-unsupported")
	unsupported.ContractVersion = "skill-orchestrator/v999"
	invalid := runningWorkerJob(now, "job-invalid")
	invalid.InputDigest = "raw-content"
	repository := &workerRepository{claimed: []core.SkillJob{unsupported, invalid}}
	worker := newTestSkillWorker(t, repository, NewSkillStageRegistry())

	report, err := worker.RunOnce(context.Background())
	if err != nil || report.DeadLettered != 2 || len(repository.finalized) != 2 {
		t.Fatalf("report=%+v finalized=%+v err=%v", report, repository.finalized, err)
	}
	for _, finalization := range repository.finalized {
		if !finalization.DeadLetter || finalization.FailureClass != core.SkillFailurePermanentValidation || !strings.HasPrefix(finalization.FailureCode, "invalid_") && finalization.FailureCode != "unsupported_contract" {
			t.Fatalf("unsafe invalid-contract result %+v", finalization)
		}
	}
}

func TestSkillOrchestratorWorkerHonorsCancellationAndStaleCompletion(t *testing.T) {
	now := time.Now().UTC()
	job := runningWorkerJob(now, "job-cancel")
	repository := &workerRepository{claimed: []core.SkillJob{job}}
	registry := NewSkillStageRegistry()
	started := make(chan struct{})
	_ = registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(ctx context.Context, _ core.SkillJob) (SkillStageResult, error) {
		close(started)
		<-ctx.Done()
		return SkillStageResult{}, ctx.Err()
	}))
	worker := newTestSkillWorker(t, repository, registry)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	report, err := worker.RunOnce(ctx)
	if err != nil || report.Cancelled != 1 || len(repository.finalized) != 0 {
		t.Fatalf("cancel report=%+v finalized=%d err=%v", report, len(repository.finalized), err)
	}

	staleRepository := &workerRepository{claimed: []core.SkillJob{runningWorkerJob(now, "job-stale")}, finalizeErr: errors.New("stale fence")}
	fastRegistry := NewSkillStageRegistry()
	_ = fastRegistry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(context.Context, core.SkillJob) (SkillStageResult, error) {
		return SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
	}))
	staleReport, err := newTestSkillWorker(t, staleRepository, fastRegistry).RunOnce(context.Background())
	if err != nil || staleReport.FinalizeFailed != 1 || staleReport.Completed != 0 {
		t.Fatalf("stale report=%+v err=%v", staleReport, err)
	}
}

func TestSkillOrchestratorWorkerBoundsBatchAndConcurrency(t *testing.T) {
	now := time.Now().UTC()
	jobs := make([]core.SkillJob, 6)
	for index := range jobs {
		jobs[index] = runningWorkerJob(now, "job-bound-"+string(rune('a'+index)))
	}
	repository := &workerRepository{claimed: jobs}
	registry := NewSkillStageRegistry()
	var active, maximum atomic.Int64
	_ = registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(context.Context, core.SkillJob) (SkillStageResult, error) {
		current := active.Add(1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return SkillStageResult{ResultKind: core.SkillJobResultSucceeded}, nil
	}))
	worker := newTestSkillWorker(t, repository, registry)
	worker.config.ClaimBatch = 6
	worker.config.Concurrency = 2
	report, err := worker.RunOnce(context.Background())
	if err != nil || report.Completed != 6 || maximum.Load() > 2 {
		t.Fatalf("report=%+v maximum=%d err=%v", report, maximum.Load(), err)
	}

	if _, err := NewSkillOrchestratorWorker(repository, registry, SkillWorkerConfig{ClaimBatch: 0, Concurrency: 1}); err == nil {
		t.Fatal("expected invalid batch configuration rejection")
	}
}

func TestSkillOrchestratorWorkerAppliesFailurePolicy(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		failure SkillStageFailure
		check   func(*testing.T, SkillWorkerRunReport, *workerRepository)
	}{
		{"retry", SkillStageFailure{Class: core.SkillFailureContention, Code: "busy"}, func(t *testing.T, report SkillWorkerRunReport, repository *workerRepository) {
			if report.Retried != 1 || len(repository.retried) != 1 || !repository.retried[0].ReadyAt.After(now) {
				t.Fatalf("retry report=%+v mutations=%+v", report, repository.retried)
			}
		}},
		{"block", SkillStageFailure{Class: core.SkillFailurePolicyBlock, Code: "approval_required"}, func(t *testing.T, report SkillWorkerRunReport, repository *workerRepository) {
			if report.Blocked != 1 || len(repository.blocked) != 1 || repository.blocked[0].ReasonCode != "approval_required" {
				t.Fatalf("block report=%+v mutations=%+v", report, repository.blocked)
			}
		}},
		{"dead_letter", SkillStageFailure{Class: core.SkillFailurePermanentValidation, Code: "invalid_input"}, func(t *testing.T, report SkillWorkerRunReport, repository *workerRepository) {
			if report.DeadLettered != 1 || len(repository.finalized) != 1 || !repository.finalized[0].DeadLetter {
				t.Fatalf("dead-letter report=%+v mutations=%+v", report, repository.finalized)
			}
		}},
		{"reject", SkillStageFailure{Class: core.SkillFailureSafetyRejection, Code: "unsafe_revision"}, func(t *testing.T, report SkillWorkerRunReport, repository *workerRepository) {
			if report.Completed != 1 || len(repository.finalized) != 1 || repository.finalized[0].DeadLetter {
				t.Fatalf("reject report=%+v mutations=%+v", report, repository.finalized)
			}
		}},
		{"cancel", SkillStageFailure{Class: core.SkillFailureCancellation, Code: "shutdown"}, func(t *testing.T, report SkillWorkerRunReport, repository *workerRepository) {
			if report.Cancelled != 1 || len(repository.finalized) != 1 || repository.finalized[0].ResultKind != core.SkillJobResultCancelled {
				t.Fatalf("cancel report=%+v mutations=%+v", report, repository.finalized)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &workerRepository{claimed: []core.SkillJob{runningWorkerJob(now, "job-"+test.name)}}
			registry := NewSkillStageRegistry()
			failure := test.failure
			_ = registry.Register(core.SkillOrchestratorContractVersion, core.SkillStageDetect, SkillStageAdapterFunc(func(context.Context, core.SkillJob) (SkillStageResult, error) {
				return SkillStageResult{}, &SkillStageError{Failure: failure, Err: errors.New("adapter detail must not persist")}
			}))
			report, err := newTestSkillWorker(t, repository, registry).RunOnce(context.Background())
			if err != nil || report.AdapterFailed != 1 {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			test.check(t, report, repository)
		})
	}
}

type workerRepository struct {
	claimed     []core.SkillJob
	finalized   []contracts.SkillJobFinalization
	retried     []contracts.SkillJobRetry
	blocked     []contracts.SkillJobBlock
	renewErr    error
	finalizeErr error
	renewals    atomic.Int64
	mu          sync.Mutex
}

func (r *workerRepository) ClaimSkillJobs(context.Context, core.SkillOrchestratorScope, string, int, time.Duration, time.Duration, time.Time) ([]core.SkillJob, error) {
	return append([]core.SkillJob(nil), r.claimed...), nil
}
func (r *workerRepository) RenewSkillJobLease(context.Context, core.SkillOrchestratorScope, string, string, int64, time.Time, time.Time) error {
	r.renewals.Add(1)
	return r.renewErr
}
func (r *workerRepository) SkillWorkflowGeneration(context.Context, core.SkillOrchestratorScope, string) (int64, error) {
	return 1, nil
}
func (r *workerRepository) FinalizeSkillJob(_ context.Context, input contracts.SkillJobFinalization) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalized = append(r.finalized, input)
	return r.finalizeErr
}
func (r *workerRepository) RetrySkillJob(_ context.Context, input contracts.SkillJobRetry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retried = append(r.retried, input)
	return r.finalizeErr
}
func (r *workerRepository) BlockSkillJob(_ context.Context, input contracts.SkillJobBlock) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocked = append(r.blocked, input)
	return r.finalizeErr
}
func newTestSkillWorker(t *testing.T, repository SkillWorkerRepository, registry *SkillStageRegistry) *SkillOrchestratorWorker {
	t.Helper()
	worker, err := NewSkillOrchestratorWorker(repository, registry, SkillWorkerConfig{
		Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, Owner: "worker-1",
		ClaimBatch: 10, Concurrency: 4, LeaseDuration: 200 * time.Millisecond,
		RenewalInterval: 50 * time.Millisecond, StageTimeout: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func runningWorkerJob(now time.Time, id string) core.SkillJob {
	return core.SkillJob{
		ID: id, WorkflowID: "workflow-1", Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		Stage: core.SkillStageDetect, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: "sha256:" + strings.Repeat("a", 64), PolicyVersion: 1, State: core.SkillJobRunning,
		Priority: 100, ReadyAt: now, Attempt: 1, MaxAttempts: 3, LeaseOwner: "worker-1",
		LeaseExpiresAt: now.Add(200 * time.Millisecond), Fence: 1, TimeoutAt: now.Add(150 * time.Millisecond),
		CreatedAt: now, UpdatedAt: now,
	}
}
