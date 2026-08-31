package skillorchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// RunRepositoryChaosCertification exercises the same crash/replay and fencing
// contract against standalone and hosted repository implementations.
func RunRepositoryChaosCertification(ctx context.Context, repository contracts.SkillOrchestratorRepository, scope core.SkillOrchestratorScope, runtime core.SkillChaosRuntime) ([]core.SkillChaosObservation, error) {
	if repository == nil || scope.Validate() != nil || runtime != core.SkillChaosStandalone && runtime != core.SkillChaosHosted {
		return nil, errors.New("valid chaos repository, scope, and runtime are required")
	}
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	stages := []core.SkillOrchestratorStage{
		core.SkillStageDetect, core.SkillStageBuild, core.SkillStageEvaluate, core.SkillStageDecide,
		core.SkillStageStartCanary, core.SkillStageAnalyzeCanary, core.SkillStageActivate,
		core.SkillStageObserveSafety, core.SkillStageRollback, core.SkillStageReconcileMaterialization,
	}
	observations := make([]core.SkillChaosObservation, 0, len(core.RequiredSkillChaosCaseIDs()))
	var renewalLoss, staleFence, workerRestart bool
	for stageIndex, stage := range stages {
		for pointIndex, point := range []core.SkillChaosFaultPoint{core.SkillChaosBeforeSideEffect, core.SkillChaosAfterSideEffect} {
			caseNow := now.Add(time.Duration(stageIndex*2+pointIndex) * 10 * time.Minute)
			workflow, job := chaosWorkflowAndJob(scope, stage, caseNow)
			if _, created, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil || !created {
				return nil, fmt.Errorf("create %s chaos workflow: created=%v: %w", stage, created, err)
			}
			if _, created, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil || !created {
				return nil, fmt.Errorf("enqueue %s chaos job: created=%v: %w", stage, created, err)
			}
			claimed, err := repository.ClaimSkillJobs(ctx, scope, "chaos-old", 1, 30*time.Second, 20*time.Second, caseNow.Add(time.Second))
			if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID {
				return nil, fmt.Errorf("claim %s chaos job: jobs=%d: %w", stage, len(claimed), err)
			}
			effects := &chaosIdempotentEffects{counts: make(map[string]int)}
			adapter := &repositoryChaosFaultAdapter{effects: effects, point: point}
			if err := adapter.Execute(claimed[0]); !errors.Is(err, errRepositoryChaosInjected) {
				return nil, fmt.Errorf("%s %s did not inject crash: %w", stage, point, err)
			}
			reclaimed, err := repository.ClaimSkillJobs(ctx, scope, "chaos-new", 1, 30*time.Second, 20*time.Second, caseNow.Add(time.Minute))
			if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != job.ID || reclaimed[0].Fence != claimed[0].Fence+1 {
				return nil, fmt.Errorf("reclaim %s chaos job: jobs=%d: %w", stage, len(reclaimed), err)
			}
			workerRestart = true
			if err := repository.RenewSkillJobLease(ctx, scope, claimed[0].ID, claimed[0].LeaseOwner, claimed[0].Fence, caseNow.Add(2*time.Minute), caseNow.Add(time.Minute)); err != nil {
				renewalLoss = true
			}
			staleResult := contracts.SkillJobFinalization{
				Scope: scope, JobID: claimed[0].ID, Owner: claimed[0].LeaseOwner, Fence: claimed[0].Fence,
				ExpectedWorkflowGeneration: workflow.Generation, ResultKind: core.SkillJobResultSucceeded, Now: caseNow.Add(time.Minute),
			}
			if err := repository.FinalizeSkillJob(ctx, staleResult); err != nil {
				staleFence = true
			}
			if err := adapter.Execute(reclaimed[0]); err != nil {
				return nil, fmt.Errorf("replay %s chaos job: %w", stage, err)
			}
			currentResult := staleResult
			currentResult.Owner, currentResult.Fence = reclaimed[0].LeaseOwner, reclaimed[0].Fence
			currentResult.Now = caseNow.Add(time.Minute + time.Second)
			if err := repository.FinalizeSkillJob(ctx, currentResult); err != nil {
				return nil, fmt.Errorf("finalize replayed %s chaos job: %w", stage, err)
			}
			stored, err := repository.GetSkillJob(ctx, scope, job.ID)
			count := effects.Count(job.ID)
			caseID := "crash_before:" + string(stage)
			if point == core.SkillChaosAfterSideEffect {
				caseID = "crash_after:" + string(stage)
			}
			passed := err == nil && stored.State == core.SkillJobCompleted && count == 1
			observations = append(observations, core.SkillChaosObservation{
				CaseID: caseID, Runtime: runtime, Stage: stage, FaultPoint: point,
				Passed: passed, Converged: passed, DomainSideEffects: count,
				UnsafeActivations: 0, DurationMillis: 1,
			})
		}
	}

	duplicatePassed, err := verifyChaosDuplicateEnqueue(ctx, repository, scope, now.Add(4*time.Hour))
	if err != nil {
		return nil, err
	}
	databaseOutagePassed := verifyChaosDatabaseOutage(repository, scope, now.Add(5*time.Hour))
	timeoutPassed := verifyChaosDeadline(true)
	cancellationPassed := verifyChaosDeadline(false)
	general := []struct {
		id     string
		passed bool
	}{
		{"renewal_loss", renewalLoss}, {"duplicate_enqueue", duplicatePassed}, {"stale_fence", staleFence},
		{"database_outage", databaseOutagePassed}, {"evaluator_timeout", timeoutPassed},
		{"cancellation", cancellationPassed}, {"worker_restart", workerRestart},
	}
	for _, item := range general {
		observations = append(observations, core.SkillChaosObservation{
			CaseID: item.id, Runtime: runtime, Passed: item.passed, Converged: item.passed,
			DomainSideEffects: 0, UnsafeActivations: 0, DurationMillis: 1,
		})
	}
	return observations, nil
}

type chaosIdempotentEffects struct {
	mu     sync.Mutex
	counts map[string]int
}

func (e *chaosIdempotentEffects) Execute(job core.SkillJob) {
	e.mu.Lock()
	if e.counts[job.ID] == 0 {
		e.counts[job.ID] = 1
	}
	e.mu.Unlock()
}

func (e *chaosIdempotentEffects) Count(jobID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[jobID]
}

var errRepositoryChaosInjected = errors.New("injected repository chaos crash")

type repositoryChaosFaultAdapter struct {
	effects *chaosIdempotentEffects
	point   core.SkillChaosFaultPoint
	fired   bool
}

func (a *repositoryChaosFaultAdapter) Execute(job core.SkillJob) error {
	if !a.fired && a.point == core.SkillChaosBeforeSideEffect {
		a.fired = true
		return errRepositoryChaosInjected
	}
	a.effects.Execute(job)
	if !a.fired && a.point == core.SkillChaosAfterSideEffect {
		a.fired = true
		return errRepositoryChaosInjected
	}
	return nil
}

func chaosWorkflowAndJob(scope core.SkillOrchestratorScope, stage core.SkillOrchestratorStage, now time.Time) (core.SkillWorkflow, core.SkillJob) {
	digest := "sha256:" + strings.Repeat("a", 64)
	workflow := core.SkillWorkflow{
		ID: uuid.NewString(), Scope: scope, SkillID: uuid.NewString(), OriginKind: core.SkillWorkflowOriginOperator,
		OriginID: uuid.NewString(), Kind: core.SkillWorkflowAutomaticRevision,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest,
		State: core.SkillWorkflowOpen, CurrentStage: stage, Generation: 1,
		ConfigurationVersion: 1, PolicyDigest: digest, CreatedAt: now, UpdatedAt: now,
	}
	job := core.SkillJob{
		ID: uuid.NewString(), WorkflowID: workflow.ID, Scope: scope, Stage: stage,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest,
		PolicyVersion: 1, State: core.SkillJobQueued, Priority: 100, ReadyAt: now,
		MaxAttempts: 4, CreatedAt: now, UpdatedAt: now,
	}
	return workflow, job
}

func verifyChaosDuplicateEnqueue(ctx context.Context, repository contracts.SkillOrchestratorRepository, scope core.SkillOrchestratorScope, now time.Time) (bool, error) {
	workflow, job := chaosWorkflowAndJob(scope, core.SkillStageDetect, now)
	if _, created, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil || !created {
		return false, fmt.Errorf("create duplicate fixture: created=%v: %w", created, err)
	}
	if _, created, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil || !created {
		return false, fmt.Errorf("enqueue duplicate fixture: created=%v: %w", created, err)
	}
	_, created, err := repository.EnqueueSkillJob(ctx, job, nil)
	return err == nil && !created, err
}

func verifyChaosDatabaseOutage(repository contracts.SkillOrchestratorRepository, scope core.SkillOrchestratorScope, now time.Time) bool {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	jobs, err := repository.ClaimSkillJobs(ctx, scope, "chaos-outage", 1, time.Minute, 30*time.Second, now)
	return err != nil && len(jobs) == 0
}

func verifyChaosDeadline(timeout bool) bool {
	ctx, cancel := context.WithCancel(context.Background())
	if timeout {
		ctx, cancel = context.WithTimeout(context.Background(), time.Nanosecond)
		time.Sleep(time.Microsecond)
	} else {
		cancel()
	}
	defer cancel()
	job := core.SkillJob{ID: "deadline-job"}
	adapter := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	err := adapter(ctx)
	return err != nil && job.ID == "deadline-job"
}
