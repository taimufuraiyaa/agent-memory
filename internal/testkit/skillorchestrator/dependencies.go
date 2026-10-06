package skillorchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func RunDependencyContract(t *testing.T, repository contracts.SkillOrchestratorRepository, scope core.SkillOrchestratorScope) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)

	t.Run("out_of_order_and_duplicate", func(t *testing.T) {
		parentWorkflow := dependencyWorkflow(base, scope, "a")
		mustCreateDependencyWorkflow(t, ctx, repository, parentWorkflow)
		parent := dependencyJob(base, parentWorkflow, core.SkillStageDetect, 1_000_000)
		mustCompleteDependencyJob(t, ctx, repository, parent, core.SkillJobResultSucceeded, false, base)

		childWorkflow := dependencyWorkflow(base.Add(time.Minute), scope, "b")
		mustCreateDependencyWorkflow(t, ctx, repository, childWorkflow)
		child := dependencyJob(base.Add(time.Minute), childWorkflow, core.SkillStageBuild, 100)
		child.State, child.BlockedReason, child.DependencyCount = core.SkillJobBlocked, "dependencies_pending", 1
		input := contracts.SkillSuccessorSchedule{Job: child, Dependencies: []core.SkillJobDependency{{JobID: child.ID, ParentJobID: parent.ID, AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: base.Add(time.Minute)}}, ExpectedWorkflowGeneration: 1, Now: base.Add(time.Minute)}
		if _, created, err := repository.ScheduleSkillSuccessor(ctx, input); err != nil || !created {
			t.Fatalf("schedule created=%v err=%v", created, err)
		}
		if _, created, err := repository.ScheduleSkillSuccessor(ctx, input); err != nil || created {
			t.Fatalf("duplicate schedule created=%v err=%v", created, err)
		}
		resolved, err := repository.ResolveSkillJobDependencies(ctx, scope, child.ID, 2, base.Add(2*time.Minute))
		if err != nil || resolved.State != contracts.SkillDependenciesReady || !resolved.Changed || resolved.Job.State != core.SkillJobQueued {
			t.Fatalf("resolve=%+v err=%v", resolved, err)
		}
		duplicate, err := repository.ResolveSkillJobDependencies(ctx, scope, child.ID, 2, base.Add(3*time.Minute))
		if err != nil || duplicate.State != contracts.SkillDependenciesReady || duplicate.Changed {
			t.Fatalf("duplicate resolve=%+v err=%v", duplicate, err)
		}
		if stored, created, err := repository.ScheduleSkillSuccessor(ctx, input); err != nil || created || stored.State != core.SkillJobQueued {
			t.Fatalf("post-resolution duplicate stored=%+v created=%v err=%v", stored, created, err)
		}
		forged := input
		forged.Dependencies = []core.SkillJobDependency{{JobID: child.ID, ParentJobID: uuid.NewString(), AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: child.CreatedAt}}
		if _, _, err := repository.ScheduleSkillSuccessor(ctx, forged); err == nil {
			t.Fatal("expected immutable dependency mismatch rejection")
		}
		stale := dependencyJob(base.Add(4*time.Minute), childWorkflow, core.SkillStageEvaluate, 100)
		stale.State, stale.BlockedReason, stale.DependencyCount = core.SkillJobBlocked, "dependencies_pending", 1
		input.Job, input.ExpectedWorkflowGeneration = stale, 1
		input.Dependencies[0].JobID = stale.ID
		if _, _, err := repository.ScheduleSkillSuccessor(ctx, input); err == nil {
			t.Fatal("expected stale successor rejection")
		}
	})

	t.Run("multiple_dependencies", func(t *testing.T) {
		parentWorkflow := dependencyWorkflow(base.Add(10*time.Minute), scope, "c")
		mustCreateDependencyWorkflow(t, ctx, repository, parentWorkflow)
		first := dependencyJob(base.Add(10*time.Minute), parentWorkflow, core.SkillStageDetect, 1_000_000)
		mustCompleteDependencyJob(t, ctx, repository, first, core.SkillJobResultSucceeded, false, base.Add(10*time.Minute))
		second := dependencyJob(base.Add(11*time.Minute), parentWorkflow, core.SkillStageEvaluate, 1_000_000)
		if _, created, err := repository.EnqueueSkillJob(ctx, second, nil); err != nil || !created {
			t.Fatalf("enqueue second parent created=%v err=%v", created, err)
		}
		childWorkflow := dependencyWorkflow(base.Add(12*time.Minute), scope, "d")
		mustCreateDependencyWorkflow(t, ctx, repository, childWorkflow)
		child := dependencyJob(base.Add(12*time.Minute), childWorkflow, core.SkillStageDecide, 100)
		child.State, child.BlockedReason, child.DependencyCount = core.SkillJobBlocked, "dependencies_pending", 2
		dependencies := []core.SkillJobDependency{
			{JobID: child.ID, ParentJobID: first.ID, AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: child.CreatedAt},
			{JobID: child.ID, ParentJobID: second.ID, AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: child.CreatedAt},
		}
		if _, _, err := repository.ScheduleSkillSuccessor(ctx, contracts.SkillSuccessorSchedule{Job: child, Dependencies: dependencies, ExpectedWorkflowGeneration: 1, Now: child.CreatedAt}); err != nil {
			t.Fatal(err)
		}
		pending, err := repository.ResolveSkillJobDependencies(ctx, scope, child.ID, 2, child.CreatedAt.Add(time.Minute))
		if err != nil || pending.State != contracts.SkillDependenciesPending || pending.Job.Attempt != 0 {
			t.Fatalf("pending=%+v err=%v", pending, err)
		}
		mustClaimAndFinalizeDependencyJob(t, ctx, repository, second, core.SkillJobResultSucceeded, false, base.Add(14*time.Minute))
		ready, err := repository.ResolveSkillJobDependencies(ctx, scope, child.ID, 2, base.Add(15*time.Minute))
		if err != nil || ready.State != contracts.SkillDependenciesReady || ready.Job.State != core.SkillJobQueued {
			t.Fatalf("ready=%+v err=%v", ready, err)
		}
	})

	for _, terminal := range []struct {
		name       string
		result     core.SkillJobResultKind
		deadLetter bool
		want       contracts.SkillDependencyResolutionState
		workflow   core.SkillWorkflowState
	}{
		{"rejected_parent", core.SkillJobResultRejected, false, contracts.SkillDependenciesRejected, core.SkillWorkflowRejected},
		{"cancelled_parent", core.SkillJobResultCancelled, false, contracts.SkillDependenciesCancelled, core.SkillWorkflowCancelled},
		{"dead_letter_parent_replay_does_not_rewrite_authority", core.SkillJobResultRejected, true, contracts.SkillDependenciesRejected, core.SkillWorkflowRejected},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			parentWorkflow := dependencyWorkflow(base.Add(20*time.Minute), scope, terminal.name+"-p")
			mustCreateDependencyWorkflow(t, ctx, repository, parentWorkflow)
			parent := dependencyJob(base.Add(20*time.Minute), parentWorkflow, core.SkillStageDetect, 1_000_000)
			mustCompleteDependencyJob(t, ctx, repository, parent, terminal.result, terminal.deadLetter, base.Add(20*time.Minute))
			if terminal.deadLetter {
				replayWorkflow := dependencyWorkflow(base.Add(20*time.Minute+30*time.Second), scope, terminal.name+"-replay")
				mustCreateDependencyWorkflow(t, ctx, repository, replayWorkflow)
				replay := dependencyJob(replayWorkflow.CreatedAt, replayWorkflow, core.SkillStageDetect, 1_000_000)
				replay.ReplayOfJobID = parent.ID
				mustCompleteDependencyJob(t, ctx, repository, replay, core.SkillJobResultSucceeded, false, replayWorkflow.CreatedAt)
			}
			childWorkflow := dependencyWorkflow(base.Add(21*time.Minute), scope, terminal.name+"-c")
			mustCreateDependencyWorkflow(t, ctx, repository, childWorkflow)
			child := dependencyJob(base.Add(21*time.Minute), childWorkflow, core.SkillStageBuild, 100)
			child.State, child.BlockedReason, child.DependencyCount = core.SkillJobBlocked, "dependencies_pending", 1
			dependency := core.SkillJobDependency{JobID: child.ID, ParentJobID: parent.ID, AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: child.CreatedAt}
			if _, _, err := repository.ScheduleSkillSuccessor(ctx, contracts.SkillSuccessorSchedule{Job: child, Dependencies: []core.SkillJobDependency{dependency}, ExpectedWorkflowGeneration: 1, Now: child.CreatedAt}); err != nil {
				t.Fatal(err)
			}
			resolved, err := repository.ResolveSkillJobDependencies(ctx, scope, child.ID, 2, child.CreatedAt.Add(time.Minute))
			if err != nil || resolved.State != terminal.want || resolved.Workflow.State != terminal.workflow || !resolved.Job.State.Terminal() {
				t.Fatalf("resolved=%+v err=%v", resolved, err)
			}
		})
	}
}

func dependencyWorkflow(now time.Time, scope core.SkillOrchestratorScope, seed string) core.SkillWorkflow {
	digest := "sha256:" + strings.Repeat("d", 64)
	return core.SkillWorkflow{ID: uuid.NewString(), Scope: scope, OriginKind: core.SkillWorkflowOriginReconciliation,
		OriginID: uuid.NewString(), Kind: core.SkillWorkflowAutomaticRevision, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: digest, State: core.SkillWorkflowOpen, CurrentStage: core.SkillStageDetect, Generation: 1,
		ConfigurationVersion: 1, PolicyDigest: digest, CreatedAt: now, UpdatedAt: now}
}

func dependencyJob(now time.Time, workflow core.SkillWorkflow, stage core.SkillOrchestratorStage, priority int) core.SkillJob {
	return core.SkillJob{ID: uuid.NewString(), WorkflowID: workflow.ID, Scope: workflow.Scope, Stage: stage,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: workflow.InputDigest, PolicyVersion: 1,
		State: core.SkillJobQueued, Priority: priority, ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now}
}

func mustCreateDependencyWorkflow(t *testing.T, ctx context.Context, repository contracts.SkillOrchestratorRepository, workflow core.SkillWorkflow) {
	t.Helper()
	if _, created, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil || !created {
		t.Fatalf("create workflow created=%v err=%v", created, err)
	}
}

func mustCompleteDependencyJob(t *testing.T, ctx context.Context, repository contracts.SkillOrchestratorRepository, job core.SkillJob, result core.SkillJobResultKind, deadLetter bool, now time.Time) {
	t.Helper()
	if _, created, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil || !created {
		t.Fatalf("enqueue parent created=%v err=%v", created, err)
	}
	mustClaimAndFinalizeDependencyJob(t, ctx, repository, job, result, deadLetter, now.Add(time.Minute))
}

func mustClaimAndFinalizeDependencyJob(t *testing.T, ctx context.Context, repository contracts.SkillOrchestratorRepository, job core.SkillJob, result core.SkillJobResultKind, deadLetter bool, now time.Time) {
	t.Helper()
	claimed, err := repository.ClaimSkillJobs(ctx, job.Scope, "dependency-worker", 1, time.Minute, 45*time.Second, now)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("claim parent=%+v want=%s err=%v", claimed, job.ID, err)
	}
	failureClass, failureCode := core.SkillFailureNone, ""
	if result == core.SkillJobResultRejected {
		failureClass, failureCode = core.SkillFailurePermanentValidation, "parent_rejected"
	}
	if result == core.SkillJobResultCancelled {
		failureClass, failureCode = core.SkillFailureCancellation, "parent_cancelled"
	}
	if err := repository.FinalizeSkillJob(ctx, contracts.SkillJobFinalization{Scope: job.Scope, JobID: job.ID,
		Owner: "dependency-worker", Fence: claimed[0].Fence, ExpectedWorkflowGeneration: 1, ResultKind: result,
		FailureClass: failureClass, FailureCode: failureCode, DeadLetter: deadLetter, Now: now.Add(30 * time.Second)}); err != nil {
		t.Fatalf("finalize parent: %v", err)
	}
}
