package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillStageResult struct {
	ResultKind core.SkillJobResultKind
	References []core.SkillOrchestratorReference
}

type SkillStageError struct {
	Failure SkillStageFailure
	Err     error
}

func (e *SkillStageError) Error() string {
	if e == nil || e.Err == nil {
		return "skill stage failed"
	}
	return e.Err.Error()
}

func (e *SkillStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (r SkillStageResult) Validate() error {
	if r.ResultKind != core.SkillJobResultSucceeded && r.ResultKind != core.SkillJobResultRejected {
		return errors.New("skill stage result_kind must be succeeded or rejected")
	}
	if len(r.References) > core.MaxSkillOrchestratorReferences {
		return errors.New("skill stage result references exceed bound")
	}
	for _, reference := range r.References {
		if err := reference.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type SkillStageAdapter interface {
	Execute(context.Context, core.SkillJob) (SkillStageResult, error)
}

type SkillStageAdapterFunc func(context.Context, core.SkillJob) (SkillStageResult, error)

func (f SkillStageAdapterFunc) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	return f(ctx, job)
}

type skillStageKey struct {
	version string
	stage   core.SkillOrchestratorStage
}

type SkillStageRegistry struct {
	mu       sync.RWMutex
	adapters map[skillStageKey]SkillStageAdapter
}

func NewSkillStageRegistry() *SkillStageRegistry {
	return &SkillStageRegistry{adapters: make(map[skillStageKey]SkillStageAdapter)}
}

func (r *SkillStageRegistry) Register(version string, stage core.SkillOrchestratorStage, adapter SkillStageAdapter) error {
	if r == nil || version == "" || !stage.Valid() || adapter == nil {
		return errors.New("valid skill stage registration is required")
	}
	key := skillStageKey{version: version, stage: stage}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[key]; exists {
		return errors.New("skill stage adapter is already registered")
	}
	r.adapters[key] = adapter
	return nil
}

func (r *SkillStageRegistry) resolve(version string, stage core.SkillOrchestratorStage) (SkillStageAdapter, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[skillStageKey{version: version, stage: stage}]
	return adapter, ok
}

type SkillWorkerRepository interface {
	ClaimSkillJobs(context.Context, core.SkillOrchestratorScope, string, int, time.Duration, time.Duration, time.Time) ([]core.SkillJob, error)
	SkillWorkflowGeneration(context.Context, core.SkillOrchestratorScope, string) (int64, error)
	RenewSkillJobLease(context.Context, core.SkillOrchestratorScope, string, string, int64, time.Time, time.Time) error
	FinalizeSkillJob(context.Context, contracts.SkillJobFinalization) error
	RetrySkillJob(context.Context, contracts.SkillJobRetry) error
	BlockSkillJob(context.Context, contracts.SkillJobBlock) error
}

type SkillWorkerConfig struct {
	Scope           core.SkillOrchestratorScope
	Owner           string
	ClaimBatch      int
	Concurrency     int
	LeaseDuration   time.Duration
	RenewalInterval time.Duration
	StageTimeout    time.Duration
}

func (c SkillWorkerConfig) Validate() error {
	if err := c.Scope.Validate(); err != nil {
		return err
	}
	if !validSkillSignalIdentifier(c.Owner) || c.ClaimBatch < 1 || c.ClaimBatch > 100 || c.Concurrency < 1 || c.Concurrency > c.ClaimBatch {
		return errors.New("skill worker owner, claim batch, or concurrency is invalid")
	}
	if c.LeaseDuration <= 0 || c.LeaseDuration > 24*time.Hour || c.RenewalInterval <= 0 || c.RenewalInterval >= c.LeaseDuration || c.StageTimeout <= 0 || c.StageTimeout > c.LeaseDuration {
		return errors.New("skill worker lease, renewal, or timeout is invalid")
	}
	return nil
}

type SkillWorkerRunReport struct {
	Claimed        int
	Completed      int
	DeadLettered   int
	LeaseLost      int
	Cancelled      int
	AdapterFailed  int
	FinalizeFailed int
	Retried        int
	Blocked        int
}

type SkillOrchestratorWorker struct {
	repository  SkillWorkerRepository
	registry    *SkillStageRegistry
	config      SkillWorkerConfig
	retryPolicy *SkillRetryPolicy
	now         func() time.Time
}

func NewSkillOrchestratorWorker(repository SkillWorkerRepository, registry *SkillStageRegistry, config SkillWorkerConfig) (*SkillOrchestratorWorker, error) {
	if repository == nil || registry == nil {
		return nil, errors.New("skill worker repository and stage registry are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	retryPolicy, err := NewSkillRetryPolicy(SkillRetryPolicyConfig{
		InitialBackoff: time.Second, MaximumBackoff: 5 * time.Minute,
		MaximumRetryAge: 24 * time.Hour, BlockedRecheck: 5 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	return &SkillOrchestratorWorker{repository: repository, registry: registry, config: config, retryPolicy: retryPolicy, now: time.Now}, nil
}

func (w *SkillOrchestratorWorker) RunOnce(ctx context.Context) (SkillWorkerRunReport, error) {
	if w == nil || w.repository == nil || w.registry == nil {
		return SkillWorkerRunReport{}, errors.New("skill worker is not configured")
	}
	now := w.now().UTC()
	jobs, err := w.repository.ClaimSkillJobs(ctx, w.config.Scope, w.config.Owner, w.config.ClaimBatch, w.config.LeaseDuration, w.config.StageTimeout, now)
	if err != nil {
		return SkillWorkerRunReport{}, fmt.Errorf("claim skill jobs: %w", err)
	}
	report := SkillWorkerRunReport{Claimed: len(jobs)}
	if len(jobs) == 0 {
		return report, nil
	}
	sem := make(chan struct{}, w.config.Concurrency)
	results := make(chan SkillWorkerRunReport, len(jobs))
	var wait sync.WaitGroup
	for _, job := range jobs {
		job := job
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- SkillWorkerRunReport{Cancelled: 1}
				return
			}
			results <- w.runJob(ctx, job)
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		report.Completed += result.Completed
		report.DeadLettered += result.DeadLettered
		report.LeaseLost += result.LeaseLost
		report.Cancelled += result.Cancelled
		report.AdapterFailed += result.AdapterFailed
		report.FinalizeFailed += result.FinalizeFailed
		report.Retried += result.Retried
		report.Blocked += result.Blocked
	}
	return report, nil
}

func (w *SkillOrchestratorWorker) runJob(parent context.Context, job core.SkillJob) SkillWorkerRunReport {
	generation, err := w.repository.SkillWorkflowGeneration(parent, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillWorkerRunReport{AdapterFailed: 1}
	}
	if job.ContractVersion != core.SkillOrchestratorContractVersion {
		return w.deadLetterInvalid(parent, job, generation, "unsupported_contract")
	}
	if err := job.Validate(); err != nil || job.Scope != w.config.Scope || job.State != core.SkillJobRunning || job.LeaseOwner != w.config.Owner {
		return w.deadLetterInvalid(parent, job, generation, "invalid_job_contract")
	}
	adapter, ok := w.registry.resolve(job.ContractVersion, job.Stage)
	if !ok {
		return w.deadLetterInvalid(parent, job, generation, "invalid_stage_adapter")
	}
	deadline := job.TimeoutAt
	if deadline.IsZero() {
		deadline = w.now().UTC().Add(w.config.StageTimeout)
	}
	stageCtx, deadlineCancel := context.WithDeadline(parent, deadline)
	stageCtx, leaseCancel := context.WithCancelCause(stageCtx)
	done := make(chan struct{})
	renewalFailed := make(chan error, 1)
	go w.superviseLease(stageCtx, leaseCancel, done, renewalFailed, job)
	result, executeErr := adapter.Execute(stageCtx, job)
	close(done)
	cause := context.Cause(stageCtx)
	deadlineCancel()
	if cause != nil {
		select {
		case <-renewalFailed:
			return SkillWorkerRunReport{LeaseLost: 1}
		default:
			return SkillWorkerRunReport{Cancelled: 1}
		}
	}
	if executeErr != nil {
		failure := SkillStageFailure{Class: core.SkillFailureUnknownInternal, Code: "stage_execution_failed"}
		var stageErr *SkillStageError
		if errors.As(executeErr, &stageErr) {
			failure = stageErr.Failure
		}
		return w.applyFailure(parent, job, generation, failure)
	}
	if err := result.Validate(); err != nil {
		return w.deadLetterInvalid(parent, job, generation, "invalid_stage_result")
	}
	finalization := contracts.SkillJobFinalization{
		Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
		ExpectedWorkflowGeneration: generation, ResultKind: result.ResultKind,
		ResultReferences: result.References, Now: w.now().UTC(),
	}
	if result.ResultKind == core.SkillJobResultRejected {
		finalization.FailureClass = core.SkillFailureSafetyRejection
		finalization.FailureCode = "stage_rejected"
	}
	if err := w.repository.FinalizeSkillJob(parent, finalization); err != nil {
		return SkillWorkerRunReport{FinalizeFailed: 1}
	}
	return SkillWorkerRunReport{Completed: 1}
}

func (w *SkillOrchestratorWorker) applyFailure(ctx context.Context, job core.SkillJob, generation int64, failure SkillStageFailure) SkillWorkerRunReport {
	now := w.now().UTC()
	decision := w.retryPolicy.Decide(job, failure, now)
	switch decision.Action {
	case SkillFailureRetry:
		err := w.repository.RetrySkillJob(ctx, contracts.SkillJobRetry{
			Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
			ExpectedWorkflowGeneration: generation, FailureClass: decision.FailureClass,
			FailureCode: decision.FailureCode, ReadyAt: decision.RetryAt, Now: now,
		})
		if err == nil {
			return SkillWorkerRunReport{AdapterFailed: 1, Retried: 1}
		}
	case SkillFailureBlock:
		err := w.repository.BlockSkillJob(ctx, contracts.SkillJobBlock{
			Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
			ExpectedWorkflowGeneration: generation, FailureClass: decision.FailureClass,
			ReasonCode: decision.FailureCode, RecheckAt: decision.RecheckAt, Now: now,
		})
		if err == nil {
			return SkillWorkerRunReport{AdapterFailed: 1, Blocked: 1}
		}
	case SkillFailureDeadLetter:
		err := w.repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{
			Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
			ExpectedWorkflowGeneration: generation, ResultKind: core.SkillJobResultRejected,
			FailureClass: decision.FailureClass, FailureCode: decision.FailureCode, DeadLetter: true, Now: now,
		})
		if err == nil {
			return SkillWorkerRunReport{AdapterFailed: 1, DeadLettered: 1}
		}
	case SkillFailureCompleteRejected:
		err := w.repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{
			Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
			ExpectedWorkflowGeneration: generation, ResultKind: core.SkillJobResultRejected,
			FailureClass: decision.FailureClass, FailureCode: decision.FailureCode, Now: now,
		})
		if err == nil {
			return SkillWorkerRunReport{AdapterFailed: 1, Completed: 1}
		}
	case SkillFailureCancel:
		err := w.repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{
			Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
			ExpectedWorkflowGeneration: generation, ResultKind: core.SkillJobResultCancelled,
			FailureClass: decision.FailureClass, FailureCode: decision.FailureCode, Now: now,
		})
		if err == nil {
			return SkillWorkerRunReport{AdapterFailed: 1, Cancelled: 1}
		}
	}
	return SkillWorkerRunReport{AdapterFailed: 1, FinalizeFailed: 1}
}

func (w *SkillOrchestratorWorker) superviseLease(ctx context.Context, cancel context.CancelCauseFunc, done <-chan struct{}, failed chan<- error, job core.SkillJob) {
	ticker := time.NewTicker(w.config.RenewalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := w.now().UTC()
			err := w.repository.RenewSkillJobLease(ctx, job.Scope, job.ID, job.LeaseOwner, job.Fence, now.Add(w.config.LeaseDuration), now)
			if err != nil {
				select {
				case failed <- err:
				default:
				}
				cancel(err)
				return
			}
		}
	}
}

func (w *SkillOrchestratorWorker) deadLetterInvalid(ctx context.Context, job core.SkillJob, generation int64, code string) SkillWorkerRunReport {
	err := w.repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{
		Scope: job.Scope, JobID: job.ID, Owner: job.LeaseOwner, Fence: job.Fence,
		ExpectedWorkflowGeneration: generation, ResultKind: core.SkillJobResultRejected,
		FailureClass: core.SkillFailurePermanentValidation, FailureCode: code, DeadLetter: true, Now: w.now().UTC(),
	})
	if err != nil {
		return SkillWorkerRunReport{FinalizeFailed: 1}
	}
	return SkillWorkerRunReport{DeadLettered: 1}
}
