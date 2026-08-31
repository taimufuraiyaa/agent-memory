package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	orchestratortest "github.com/taimufuraiyaa/agent-memory/internal/testkit/skillorchestrator"
)

func TestSQLiteSkillOrchestratorSharedRepositoryContract(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	orchestratortest.RunRepositoryContract(t, store, core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"})
}

func TestSQLiteSkillOrchestratorDependencyContract(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	orchestratortest.RunDependencyContract(t, store, core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"})
}

func TestSQLiteSkillOrchestratorReconciliationCursorContract(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	orchestratortest.RunReconciliationCursorContract(t, store, core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"})
}

func TestSQLiteSkillSignalRouteIsAtomicAndIdempotent(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	workflow := sqliteValidSkillWorkflow(now, "workflow-signal", "signal-1")
	workflow.OriginKind = core.SkillWorkflowOriginLifecycleSignal
	job := sqliteValidSkillJob(now, "job-signal", workflow.ID, core.SkillStageDetect)
	job.InputDigest = workflow.InputDigest

	first, err := store.RouteSkillSignal(ctx, workflow, job, nil)
	if err != nil || !first.Created {
		t.Fatalf("first route=%+v err=%v", first, err)
	}
	second, err := store.RouteSkillSignal(ctx, workflow, job, nil)
	if err != nil || second.Created || second.Workflow.ID != first.Workflow.ID || second.Job.ID != first.Job.ID {
		t.Fatalf("duplicate route=%+v err=%v", second, err)
	}

	rollbackWorkflow := sqliteValidSkillWorkflow(now, "workflow-rollback", "signal-rollback")
	rollbackWorkflow.OriginKind = core.SkillWorkflowOriginLifecycleSignal
	rollbackJob := sqliteValidSkillJob(now, "job-rollback", rollbackWorkflow.ID, core.SkillStageBuild)
	rollbackJob.InputDigest = rollbackWorkflow.InputDigest
	dependency := core.SkillJobDependency{JobID: rollbackJob.ID, ParentJobID: "missing-parent", AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: now}
	if _, err := store.RouteSkillSignal(ctx, rollbackWorkflow, rollbackJob, []core.SkillJobDependency{dependency}); err == nil {
		t.Fatal("expected missing dependency to roll back route")
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_orchestrator_workflows WHERE id=?`, rollbackWorkflow.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("transaction left partial workflow count=%d", count)
	}
}

func TestSQLiteSkillOrchestratorCreateAndEnqueueAreIdempotent(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 16, 30, 0, 0, time.UTC)
	workflow := sqliteValidSkillWorkflow(now, "workflow-1", "lesson-1")

	stored, created, err := store.CreateSkillWorkflow(ctx, workflow)
	if err != nil || !created || stored.ID != workflow.ID {
		t.Fatalf("create workflow: stored=%+v created=%v err=%v", stored, created, err)
	}
	stored, created, err = store.CreateSkillWorkflow(ctx, workflow)
	if err != nil || created || stored.ID != workflow.ID {
		t.Fatalf("repeat workflow: stored=%+v created=%v err=%v", stored, created, err)
	}

	job := sqliteValidSkillJob(now, "job-1", workflow.ID, core.SkillStageDetect)
	storedJob, enqueued, err := store.EnqueueSkillJob(ctx, job, nil)
	if err != nil || !enqueued || storedJob.ID != job.ID {
		t.Fatalf("enqueue job: stored=%+v enqueued=%v err=%v", storedJob, enqueued, err)
	}
	storedJob, enqueued, err = store.EnqueueSkillJob(ctx, job, nil)
	if err != nil || enqueued || storedJob.ID != job.ID {
		t.Fatalf("repeat job: stored=%+v enqueued=%v err=%v", storedJob, enqueued, err)
	}

	job.ID = "forged-job"
	job.Scope.WorkspaceID = "other-workspace"
	if _, _, err := store.EnqueueSkillJob(ctx, job, nil); !errors.Is(err, ErrSkillOrchestratorScope) {
		t.Fatalf("expected workflow/job scope mismatch, got %v", err)
	}
}

func TestSQLiteSkillOrchestratorClaimsPriorityThenOldestAndPaginates(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 16, 35, 0, 0, time.UTC)
	workflow := sqliteValidSkillWorkflow(now, "workflow-claim", "lesson-claim")
	if _, _, err := store.CreateSkillWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	jobs := []core.SkillJob{
		sqliteValidSkillJob(now.Add(-2*time.Minute), "job-normal-old", workflow.ID, core.SkillStageDetect),
		sqliteValidSkillJob(now.Add(-time.Minute), "job-normal-new", workflow.ID, core.SkillStageBuild),
		sqliteValidSkillJob(now, "job-safety", workflow.ID, core.SkillStageRollback),
	}
	jobs[2].Priority = 1000
	for _, job := range jobs {
		if _, _, err := store.EnqueueSkillJob(ctx, job, nil); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := store.ClaimSkillJobs(ctx, workflow.Scope, "worker-1", 3, time.Minute, 45*time.Second, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"job-safety", "job-normal-old", "job-normal-new"}
	for index := range want {
		if claimed[index].ID != want[index] || claimed[index].Fence != 1 || claimed[index].Attempt != 1 {
			t.Fatalf("claim[%d]=%+v want id=%s fence=attempt=1", index, claimed[index], want[index])
		}
	}

	page1, next, err := store.ListSkillJobs(ctx, workflow.Scope, workflow.ID, "", 2)
	if err != nil || len(page1) != 2 || next == "" {
		t.Fatalf("page1 len=%d next=%q err=%v", len(page1), next, err)
	}
	page2, next, err := store.ListSkillJobs(ctx, workflow.Scope, workflow.ID, next, 2)
	if err != nil || len(page2) != 1 || next != "" {
		t.Fatalf("page2 len=%d next=%q err=%v", len(page2), next, err)
	}
}

func TestSQLiteSkillOrchestratorExpiredReclaimRejectsStaleWorker(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 16, 40, 0, 0, time.UTC)
	workflow := sqliteValidSkillWorkflow(now, "workflow-reclaim", "lesson-reclaim")
	if _, _, err := store.CreateSkillWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	job := sqliteValidSkillJob(now, "job-reclaim", workflow.ID, core.SkillStageDetect)
	if _, _, err := store.EnqueueSkillJob(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimSkillJobs(ctx, workflow.Scope, "worker-old", 1, time.Minute, 45*time.Second, now)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	if err := store.RenewSkillJobLease(ctx, workflow.Scope, job.ID, "worker-old", 1, now.Add(90*time.Second), now.Add(30*time.Second)); err != nil {
		t.Fatalf("renew active lease: %v", err)
	}
	reclaimed, err := store.ClaimSkillJobs(ctx, workflow.Scope, "worker-new", 1, time.Minute, 45*time.Second, now.Add(2*time.Minute))
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Fence != 2 || reclaimed[0].Attempt != 2 {
		t.Fatalf("reclaim=%+v err=%v", reclaimed, err)
	}
	if err := store.RenewSkillJobLease(ctx, workflow.Scope, job.ID, "worker-old", 1, now.Add(3*time.Minute), now.Add(2*time.Minute)); !errors.Is(err, ErrSkillOrchestratorStaleLease) {
		t.Fatalf("expected stale renewal rejection, got %v", err)
	}
	if err := store.FinalizeSkillJob(ctx, SkillJobFinalization{
		Scope: workflow.Scope, JobID: job.ID, Owner: "worker-old", Fence: 1, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultSucceeded, Now: now.Add(2 * time.Minute),
	}); !errors.Is(err, ErrSkillOrchestratorStaleLease) {
		t.Fatalf("expected stale completion rejection, got %v", err)
	}
	if err := store.FinalizeSkillJob(ctx, SkillJobFinalization{
		Scope: workflow.Scope, JobID: job.ID, Owner: "worker-new", Fence: 2, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultSucceeded, ResultReferences: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceCandidate, ID: "candidate-1"}},
		Now: now.Add(2*time.Minute + time.Second),
	}); err != nil {
		t.Fatalf("current worker finalize: %v", err)
	}
	if err := store.FinalizeSkillJob(ctx, SkillJobFinalization{
		Scope: workflow.Scope, JobID: job.ID, Owner: "worker-new", Fence: 2, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultSucceeded, ResultReferences: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceCandidate, ID: "candidate-1"}},
		Now: now.Add(2*time.Minute + time.Second),
	}); err != nil {
		t.Fatalf("idempotent completion replay: %v", err)
	}
}

func TestSQLiteSkillOrchestratorConcurrentClaimHasOneOwner(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	workflow := sqliteValidSkillWorkflow(now, "workflow-concurrent", "lesson-concurrent")
	if _, _, err := store.CreateSkillWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	job := sqliteValidSkillJob(now, "job-concurrent", workflow.ID, core.SkillStageDetect)
	if _, _, err := store.EnqueueSkillJob(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	type result struct {
		jobs []core.SkillJob
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, owner := range []string{"worker-a", "worker-b"} {
		owner := owner
		go func() {
			<-start
			jobs, err := store.ClaimSkillJobs(ctx, workflow.Scope, owner, 1, time.Minute, 45*time.Second, now.Add(time.Second))
			results <- result{jobs: jobs, err: err}
		}()
	}
	close(start)
	total := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent claim: %v", result.err)
		}
		total += len(result.jobs)
	}
	if total != 1 {
		t.Fatalf("expected one concurrent owner, claims=%d", total)
	}
}

func TestSQLiteSkillOrchestratorDependenciesRetryBlockCancelAndDeadLetter(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 16, 45, 0, 0, time.UTC)
	workflow := sqliteValidSkillWorkflow(now, "workflow-state", "lesson-state")
	if _, _, err := store.CreateSkillWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	parent := sqliteValidSkillJob(now, "job-parent", workflow.ID, core.SkillStageDetect)
	child := sqliteValidSkillJob(now.Add(time.Second), "job-child", workflow.ID, core.SkillStageBuild)
	if _, _, err := store.EnqueueSkillJob(ctx, parent, nil); err != nil {
		t.Fatal(err)
	}
	dependency := core.SkillJobDependency{JobID: child.ID, ParentJobID: parent.ID, AcceptedResultKinds: []core.SkillJobResultKind{core.SkillJobResultSucceeded}, CreatedAt: now}
	if _, _, err := store.EnqueueSkillJob(ctx, child, []core.SkillJobDependency{dependency}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimSkillJobs(ctx, workflow.Scope, "worker-1", 2, time.Minute, 45*time.Second, now.Add(2*time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].ID != parent.ID {
		t.Fatalf("expected only dependency parent, got %+v err=%v", claimed, err)
	}
	if err := store.FinalizeSkillJob(ctx, SkillJobFinalization{Scope: workflow.Scope, JobID: parent.ID, Owner: "worker-1", Fence: 1, ExpectedWorkflowGeneration: 1, ResultKind: core.SkillJobResultSucceeded, Now: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimSkillJobs(ctx, workflow.Scope, "worker-1", 1, time.Minute, 45*time.Second, now.Add(4*time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].ID != child.ID {
		t.Fatalf("expected satisfied child, got %+v err=%v", claimed, err)
	}
	if err := store.RetrySkillJob(ctx, SkillJobRetry{
		Scope: workflow.Scope, JobID: child.ID, Owner: "worker-1", Fence: 1, ExpectedWorkflowGeneration: 1,
		FailureClass: core.SkillFailureDependencyUnavailable, FailureCode: "evaluator_unavailable", ReadyAt: now.Add(time.Minute), Now: now.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimSkillJobs(ctx, workflow.Scope, "worker-2", 1, time.Minute, 45*time.Second, now.Add(30*time.Second))
	if err != nil || len(claimed) != 0 {
		t.Fatalf("retry claimed too early: %+v err=%v", claimed, err)
	}
	claimed, err = store.ClaimSkillJobs(ctx, workflow.Scope, "worker-2", 1, time.Minute, 45*time.Second, now.Add(2*time.Minute))
	if err != nil || len(claimed) != 1 || claimed[0].Attempt != 2 {
		t.Fatalf("retry claim=%+v err=%v", claimed, err)
	}
	if err := store.BlockSkillJob(ctx, SkillJobBlock{
		Scope: workflow.Scope, JobID: child.ID, Owner: "worker-2", Fence: 2, ExpectedWorkflowGeneration: 1,
		FailureClass: core.SkillFailureInsufficientEvidence, ReasonCode: "canary_samples_insufficient", RecheckAt: now.Add(10 * time.Minute), Now: now.Add(2*time.Minute + time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelSkillJob(ctx, workflow.Scope, child.ID, 1, "operator-1", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	dead := sqliteValidSkillJob(now, "job-dead", workflow.ID, core.SkillStageEvaluate)
	if _, _, err := store.EnqueueSkillJob(ctx, dead, nil); err != nil {
		t.Fatal(err)
	}
	claimed, _ = store.ClaimSkillJobs(ctx, workflow.Scope, "worker-3", 1, time.Minute, 45*time.Second, now.Add(time.Second))
	if len(claimed) != 1 || claimed[0].ID != dead.ID {
		t.Fatalf("expected dead-letter candidate claim, got %+v", claimed)
	}
	if err := store.FinalizeSkillJob(ctx, SkillJobFinalization{
		Scope: workflow.Scope, JobID: dead.ID, Owner: "worker-3", Fence: 1, ExpectedWorkflowGeneration: 1,
		ResultKind: core.SkillJobResultRejected, FailureClass: core.SkillFailurePermanentValidation,
		FailureCode: "input_digest_mismatch", DeadLetter: true, Now: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSkillJob(ctx, workflow.Scope, dead.ID)
	if err != nil || got.State != core.SkillJobDeadLettered {
		t.Fatalf("dead-letter got=%+v err=%v", got, err)
	}
}

func TestSQLiteSkillOrchestratorGenerationAndScopeFailClosed(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	workflow := sqliteValidSkillWorkflow(now, "workflow-fence", "lesson-fence")
	if _, _, err := store.CreateSkillWorkflow(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	job := sqliteValidSkillJob(now, "job-fence", workflow.ID, core.SkillStageActivate)
	if _, _, err := store.EnqueueSkillJob(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	claimed, _ := store.ClaimSkillJobs(ctx, workflow.Scope, "worker-1", 1, time.Minute, 45*time.Second, now.Add(time.Second))
	if len(claimed) != 1 {
		t.Fatal("expected claim")
	}
	if err := store.FinalizeSkillJob(ctx, SkillJobFinalization{Scope: workflow.Scope, JobID: job.ID, Owner: "worker-1", Fence: 1, ExpectedWorkflowGeneration: 2, ResultKind: core.SkillJobResultSucceeded, Now: now.Add(2 * time.Second)}); !errors.Is(err, ErrSkillOrchestratorGeneration) {
		t.Fatalf("expected generation rejection, got %v", err)
	}
	forged := workflow.Scope
	forged.WorkspaceID = "other"
	if _, err := store.GetSkillJob(ctx, forged, job.ID); !errors.Is(err, ErrSkillOrchestratorNotFound) {
		t.Fatalf("expected scope-safe not found, got %v", err)
	}
}

func openSkillOrchestratorStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", strings.ReplaceAll(t.Name(), "/", "-"))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sqliteValidSkillWorkflow(now time.Time, id, originID string) core.SkillWorkflow {
	digest := "sha256:" + strings.Repeat("a", 64)
	return core.SkillWorkflow{
		ID: id, Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		OriginKind: core.SkillWorkflowOriginToolLesson, OriginID: originID, Kind: core.SkillWorkflowAutomaticRevision,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest, State: core.SkillWorkflowOpen,
		CurrentStage: core.SkillStageDetect, Generation: 1, ConfigurationVersion: 1, PolicyDigest: digest,
		CreatedAt: now, UpdatedAt: now,
	}
}

func sqliteValidSkillJob(now time.Time, id, workflowID string, stage core.SkillOrchestratorStage) core.SkillJob {
	return core.SkillJob{
		ID: id, WorkflowID: workflowID, Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		Stage: stage, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: "sha256:" + strings.Repeat(string(rune('a'+len(id)%5)), 64), PolicyVersion: 1,
		State: core.SkillJobQueued, Priority: 100, ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
}
