package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillRetryPolicyClassifiesEveryFailureClass(t *testing.T) {
	now := time.Date(2026, 8, 31, 17, 30, 0, 0, time.UTC)
	policy := newTestRetryPolicy(t)
	tests := []struct {
		class  core.SkillJobFailureClass
		action SkillFailureAction
	}{
		{core.SkillFailureContention, SkillFailureRetry},
		{core.SkillFailureDependencyUnavailable, SkillFailureRetry},
		{core.SkillFailureUnknownInternal, SkillFailureRetry},
		{core.SkillFailureInsufficientEvidence, SkillFailureBlock},
		{core.SkillFailurePolicyBlock, SkillFailureBlock},
		{core.SkillFailurePermanentValidation, SkillFailureDeadLetter},
		{core.SkillFailureSafetyRejection, SkillFailureCompleteRejected},
		{core.SkillFailureCancellation, SkillFailureCancel},
	}
	for _, test := range tests {
		t.Run(string(test.class), func(t *testing.T) {
			decision := policy.Decide(runningWorkerJob(now, "job-"+string(test.class)), SkillStageFailure{Class: test.class, Code: "stable_code"}, now)
			if decision.Action != test.action || decision.FailureCode != "stable_code" {
				t.Fatalf("decision=%+v want=%s", decision, test.action)
			}
		})
	}
}

func TestSkillRetryPolicyBackoffIsDeterministicBoundedAndExhausts(t *testing.T) {
	now := time.Now().UTC()
	policy := newTestRetryPolicy(t)
	job := runningWorkerJob(now, "job-deterministic")
	first := policy.Decide(job, SkillStageFailure{Class: core.SkillFailureContention, Code: "busy"}, now)
	second := policy.Decide(job, SkillStageFailure{Class: core.SkillFailureContention, Code: "busy"}, now)
	if first.RetryAt != second.RetryAt || !first.RetryAt.After(now) || first.RetryAt.Sub(now) > 40*time.Millisecond {
		t.Fatalf("unbounded or nondeterministic decisions first=%+v second=%+v", first, second)
	}
	job.Attempt = job.MaxAttempts
	if decision := policy.Decide(job, SkillStageFailure{Class: core.SkillFailureContention, Code: "busy"}, now); decision.Action != SkillFailureDeadLetter || decision.FailureCode != "attempts_exhausted" {
		t.Fatalf("expected attempt exhaustion, got %+v", decision)
	}
	job.Attempt = 1
	job.CreatedAt = now.Add(-time.Hour)
	if decision := policy.Decide(job, SkillStageFailure{Class: core.SkillFailureUnknownInternal, Code: "internal"}, now); decision.Action != SkillFailureDeadLetter || decision.FailureCode != "retry_age_exhausted" {
		t.Fatalf("expected age exhaustion, got %+v", decision)
	}
}

func TestSkillRetryPolicyBlocksWithoutAttemptMutationAndRedactsUnsafeCode(t *testing.T) {
	now := time.Now().UTC()
	policy := newTestRetryPolicy(t)
	job := runningWorkerJob(now, "job-blocked")
	attempt := job.Attempt
	decision := policy.Decide(job, SkillStageFailure{Class: core.SkillFailureInsufficientEvidence, Code: "raw output:\nsecret"}, now)
	if decision.Action != SkillFailureBlock || decision.Attempt != attempt || decision.FailureCode != "invalid_failure_code" || !decision.RecheckAt.After(now) {
		t.Fatalf("unsafe blocked decision %+v", decision)
	}
}

func TestSkillDeadLetterReplayPreservesInputsAndRequiresAuthorization(t *testing.T) {
	now := time.Now().UTC()
	workflow := validReplayWorkflow(now)
	job := validReplayDeadLetter(now, workflow)
	repository := &skillReplayRepository{workflow: workflow, job: job}
	service := NewSkillDeadLetterReplayService(repository)
	request := SkillDeadLetterReplayRequest{
		Scope: workflow.Scope, JobID: job.ID, ActorID: "operator-1", Authorized: true,
		ReasonCode: "operator_reviewed", IdempotencyKey: "replay-key-1", Now: now.Add(time.Minute),
	}

	result, err := service.Replay(context.Background(), request)
	if err != nil || !result.Created {
		t.Fatalf("replay=%+v err=%v", result, err)
	}
	if result.Job.InputDigest != job.InputDigest || result.Job.PolicyVersion != job.PolicyVersion || result.Job.ReplayOfJobID != job.ID || result.Job.ID == job.ID || result.Workflow.ID == workflow.ID {
		t.Fatalf("replay changed immutable binding %+v", result)
	}
	duplicate, err := service.Replay(context.Background(), request)
	if err != nil || duplicate.Job.ID != result.Job.ID || repository.routes != 1 {
		t.Fatalf("duplicate replay=%+v routes=%d err=%v", duplicate, repository.routes, err)
	}

	request.Authorized = false
	request.IdempotencyKey = "replay-key-2"
	if _, err := service.Replay(context.Background(), request); err == nil {
		t.Fatal("expected unauthorized replay rejection")
	}
	repository.job.State = core.SkillJobCompleted
	repository.job.ResultKind = core.SkillJobResultSucceeded
	request.Authorized = true
	if _, err := service.Replay(context.Background(), request); err == nil {
		t.Fatal("expected non-dead-letter replay rejection")
	}
}

func newTestRetryPolicy(t *testing.T) *SkillRetryPolicy {
	t.Helper()
	policy, err := NewSkillRetryPolicy(SkillRetryPolicyConfig{
		InitialBackoff: 10 * time.Millisecond, MaximumBackoff: 40 * time.Millisecond,
		MaximumRetryAge: 30 * time.Minute, BlockedRecheck: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func validReplayWorkflow(now time.Time) core.SkillWorkflow {
	digest := "sha256:" + strings.Repeat("c", 64)
	return core.SkillWorkflow{
		ID: "workflow-original", Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"},
		OriginKind: core.SkillWorkflowOriginLifecycleSignal, OriginID: "signal-original", Kind: core.SkillWorkflowAutomaticRevision,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest, State: core.SkillWorkflowDeadLettered,
		CurrentStage: core.SkillStageEvaluate, Generation: 1, ConfigurationVersion: 3, PolicyDigest: digest,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now, TerminalAt: now,
	}
}

func validReplayDeadLetter(now time.Time, workflow core.SkillWorkflow) core.SkillJob {
	return core.SkillJob{
		ID: "job-original", WorkflowID: workflow.ID, Scope: workflow.Scope, Stage: core.SkillStageEvaluate,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: workflow.InputDigest, PolicyVersion: 7,
		State: core.SkillJobDeadLettered, Priority: 100, ReadyAt: now.Add(-time.Hour), Attempt: 3, MaxAttempts: 3,
		Fence: 3, ResultKind: core.SkillJobResultRejected, FailureClass: core.SkillFailurePermanentValidation,
		FailureCode: "invalid_fixture", CreatedAt: now.Add(-time.Hour), UpdatedAt: now, CompletedAt: now,
	}
}

type skillReplayRepository struct {
	workflow core.SkillWorkflow
	job      core.SkillJob
	routes   int
	results  map[string]contracts.SkillSignalRouteResult
}

func (r *skillReplayRepository) GetSkillJob(context.Context, core.SkillOrchestratorScope, string) (core.SkillJob, error) {
	if r.job.ID == "" {
		return core.SkillJob{}, errors.New("missing")
	}
	return r.job, nil
}
func (r *skillReplayRepository) GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error) {
	return r.workflow, nil
}
func (r *skillReplayRepository) RouteSkillSignal(_ context.Context, workflow core.SkillWorkflow, job core.SkillJob, dependencies []core.SkillJobDependency) (contracts.SkillSignalRouteResult, error) {
	if r.results == nil {
		r.results = map[string]contracts.SkillSignalRouteResult{}
	}
	if existing, ok := r.results[workflow.ID]; ok {
		return existing, nil
	}
	r.routes++
	result := contracts.SkillSignalRouteResult{Workflow: workflow, Job: job, Dependencies: dependencies, Created: true}
	r.results[workflow.ID] = result
	return result, nil
}
