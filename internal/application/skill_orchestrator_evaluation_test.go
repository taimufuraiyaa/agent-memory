package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillEvaluationAdapterBindsBudgetAndReplaysExactPair(t *testing.T) {
	fixture := newEvaluationAdapterFixture(t)
	result, err := fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil || result.ResultKind != core.SkillJobResultSucceeded || len(result.References) != 2 {
		t.Fatalf("evaluation result = %+v, %v", result, err)
	}
	if fixture.budget.reserves != 1 || fixture.budget.reservation.committed != fixture.configuration.BudgetUnits || fixture.budget.reservation.released != 0 {
		t.Fatalf("budget state = %+v", fixture.budget)
	}
	runnerCalls := len(fixture.runner.requests)
	result, err = fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil || len(result.References) != 2 || len(fixture.runner.requests) != runnerCalls || fixture.budget.reserves != 1 {
		t.Fatalf("replay result = %+v, %v, runner=%d budget=%d", result, err, len(fixture.runner.requests), fixture.budget.reserves)
	}
}

func TestSkillEvaluationAdapterBlocksBudgetAndReadinessFailures(t *testing.T) {
	fixture := newEvaluationAdapterFixture(t)
	fixture.budget.err = ErrSkillEvaluationBudgetExhausted
	_, err := fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePolicyBlock, "evaluation_budget_exhausted")
	if len(fixture.runner.requests) != 0 {
		t.Fatal("budget-exhausted evaluation reached executor")
	}

	fixture = newEvaluationAdapterFixture(t)
	fixture.readiness.err = ErrSkillEvaluatorUnavailable
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureDependencyUnavailable, "evaluation_executor_unready")
	if fixture.budget.reserves != 0 || len(fixture.runner.requests) != 0 {
		t.Fatal("unready evaluation reserved budget or reached executor")
	}
}

func TestSkillEvaluationAdapterRejectsStaleSuiteAndCancellation(t *testing.T) {
	fixture := newEvaluationAdapterFixture(t)
	fixture.configuration.SuiteDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	adapter, err := NewSkillEvaluationAdapter(fixture.repository, fixture.runner, fixture.baseline, fixture.readiness, fixture.budget, fixture.configuration, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureDependencyUnavailable, "evaluation_failed")
	if len(fixture.repository.runs) != 0 {
		t.Fatal("stale suite persisted evaluation runs")
	}

	fixture = newEvaluationAdapterFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fixture.runner.err = context.Canceled
	_, err = fixture.adapter.Execute(ctx, fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureCancellation, "evaluation_cancelled")
	if fixture.budget.reservation.released != 1 || fixture.budget.reservation.committed != 0 {
		t.Fatalf("cancelled budget = %+v", fixture.budget.reservation)
	}
}

func TestSkillPolicyDecisionAdapterIsImmutableAcrossReplayAndPolicyChange(t *testing.T) {
	fixture := newPolicyAdapterFixture(t, core.SkillRiskLow)
	result, err := fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil || result.ResultKind != core.SkillJobResultSucceeded || fixture.repository.saved.Decision != core.SkillDecisionCanary {
		t.Fatalf("policy result = %+v saved=%+v err=%v", result, fixture.repository.saved, err)
	}
	decidedAt := fixture.repository.saved.DecidedAt
	result, err = fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil || fixture.repository.saved.DecidedAt != decidedAt {
		t.Fatalf("policy replay rewrote decision: %+v, %v", result, err)
	}

	fixture.job.PolicyVersion++
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePermanentValidation, "policy_binding_mismatch")
}

func TestSkillPolicyDecisionAdapterDeniesAutomaticHighRiskPromotion(t *testing.T) {
	fixture := newPolicyAdapterFixture(t, core.SkillRiskHigh)
	_, err := fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.repository.saved.Decision != core.SkillDecisionApprovalRequired {
		t.Fatalf("high-risk decision = %s", fixture.repository.saved.Decision)
	}
}

type evaluationAdapterRepository struct {
	*skillEvaluationRepository
	workflow core.SkillWorkflow
}

func (r *evaluationAdapterRepository) GetSkillWorkflow(_ context.Context, scope core.SkillOrchestratorScope, id string) (core.SkillWorkflow, error) {
	if scope != r.workflow.Scope || id != r.workflow.ID {
		return core.SkillWorkflow{}, errors.New("workflow not found")
	}
	return r.workflow, nil
}

func (r *evaluationAdapterRepository) GetSkillEvaluationRun(_ context.Context, workspace, id string) (core.SkillEvaluationRun, error) {
	for _, run := range r.runs {
		if run.Workspace == workspace && run.ID == id {
			return run, nil
		}
	}
	return core.SkillEvaluationRun{}, errors.New("run not found")
}

type fixedEvaluationBaseline struct{ revision core.SkillRevision }

func (r fixedEvaluationBaseline) ResolveSkillEvaluationBaseline(context.Context, core.SkillRevision) (core.SkillRevision, error) {
	return r.revision, nil
}

type evaluationReadiness struct{ err error }

func (r *evaluationReadiness) CheckSkillEvaluationExecutor(context.Context, string, string, string) error {
	return r.err
}

type evaluationBudgetReservation struct {
	committed int64
	released  int
}

func (r *evaluationBudgetReservation) Commit(_ context.Context, units int64) error {
	r.committed = units
	return nil
}

func (r *evaluationBudgetReservation) Release(context.Context) error {
	r.released++
	return nil
}

type evaluationBudget struct {
	reserves    int
	err         error
	reservation *evaluationBudgetReservation
}

func (b *evaluationBudget) Reserve(context.Context, SkillEvaluationBudgetRequest) (SkillEvaluationBudgetReservation, error) {
	b.reserves++
	if b.err != nil {
		return nil, b.err
	}
	return b.reservation, nil
}

type evaluationAdapterFixture struct {
	repository    *evaluationAdapterRepository
	runner        *skillEvaluationRunner
	baseline      fixedEvaluationBaseline
	readiness     *evaluationReadiness
	budget        *evaluationBudget
	configuration SkillEvaluationStageConfiguration
	adapter       *SkillEvaluationAdapter
	job           core.SkillJob
	now           time.Time
}

func newEvaluationAdapterFixture(t *testing.T) evaluationAdapterFixture {
	t.Helper()
	base := newSkillEvaluationFixture()
	signals := SkillSignalConfiguration{Environment: "local", ConfigurationVersion: 4, PolicyVersion: 7, PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}
	candidate := base.repository.revisions["revision-2"]
	signal, err := SkillLifecycleSignalForRevision(candidate, signals)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestSkillLifecycleSignal(signal, nil)
	scope := signal.Scope
	workflow := core.SkillWorkflow{ID: "workflow-evaluate", Scope: scope, OriginKind: core.SkillWorkflowOriginLifecycleSignal, OriginID: candidate.ID, InputDigest: digest, ConfigurationVersion: signals.ConfigurationVersion, PolicyDigest: signals.PolicyDigest}
	repository := &evaluationAdapterRepository{skillEvaluationRepository: base.repository, workflow: workflow}
	configuration := SkillEvaluationStageConfiguration{Signal: signals, SuiteID: base.repository.suite.ID, SuiteVersion: base.repository.suite.Version, SuiteDigest: base.repository.suite.Digest, Evaluator: "restricted-runner", EvaluatorVersion: "v1", EnvironmentFingerprint: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Timeout: time.Second, MaximumCases: 20, BudgetUnits: 10}
	readiness := &evaluationReadiness{}
	budget := &evaluationBudget{reservation: &evaluationBudgetReservation{}}
	baseline := fixedEvaluationBaseline{revision: base.repository.revisions["revision-1"]}
	adapter, err := NewSkillEvaluationAdapter(repository, base.runner, baseline, readiness, budget, configuration, func() time.Time { return base.now })
	if err != nil {
		t.Fatal(err)
	}
	job := core.SkillJob{ID: "job-evaluate", WorkflowID: workflow.ID, Scope: scope, SkillID: candidate.SkillID, Stage: core.SkillStageEvaluate, InputDigest: digest, PolicyVersion: signals.PolicyVersion}
	return evaluationAdapterFixture{repository: repository, runner: base.runner, baseline: baseline, readiness: readiness, budget: budget, configuration: configuration, adapter: adapter, job: job, now: base.now}
}

type policyAdapterRepository struct {
	*skillPolicyRepository
	workflow core.SkillWorkflow
}

func (r *policyAdapterRepository) GetSkillWorkflow(_ context.Context, scope core.SkillOrchestratorScope, id string) (core.SkillWorkflow, error) {
	if scope != r.workflow.Scope || id != r.workflow.ID {
		return core.SkillWorkflow{}, errors.New("workflow not found")
	}
	return r.workflow, nil
}

func (r *policyAdapterRepository) GetSkillPolicyDecision(_ context.Context, workspace, id string) (core.SkillPolicyDecision, error) {
	if r.saved.Workspace == workspace && r.saved.ID == id {
		return r.saved, nil
	}
	return core.SkillPolicyDecision{}, errors.New("decision not found")
}

type policyAdapterFixture struct {
	repository *policyAdapterRepository
	adapter    *SkillPolicyDecisionAdapter
	job        core.SkillJob
}

func newPolicyAdapterFixture(t *testing.T, risk core.SkillRiskTier) policyAdapterFixture {
	t.Helper()
	base := newSkillPolicyFixture(risk)
	base.repository.runs["job-evaluate-candidate"] = base.repository.runs["candidate-run"]
	candidate := base.repository.runs["job-evaluate-candidate"]
	candidate.ID = "job-evaluate-candidate"
	base.repository.runs[candidate.ID] = candidate
	baseline := base.repository.runs["baseline-run"]
	baseline.ID = "job-evaluate-baseline"
	base.repository.runs[baseline.ID] = baseline
	signals := SkillSignalConfiguration{Environment: "local", ConfigurationVersion: 4, PolicyVersion: base.repository.policy.Version, PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}
	signal, err := SkillLifecycleSignalForEvaluation(candidate, baseline, signals)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestSkillLifecycleSignal(signal, nil)
	workflow := core.SkillWorkflow{ID: "workflow-decide", Scope: signal.Scope, OriginKind: core.SkillWorkflowOriginLifecycleSignal, OriginID: candidate.ID, InputDigest: digest, ConfigurationVersion: signals.ConfigurationVersion, PolicyDigest: signals.PolicyDigest}
	repository := &policyAdapterRepository{skillPolicyRepository: base.repository, workflow: workflow}
	configuration := SkillPolicyStageConfiguration{Signal: signals, PolicyID: base.repository.policy.ID}
	adapter, err := NewSkillPolicyDecisionAdapter(repository, configuration, func() time.Time { return base.now })
	if err != nil {
		t.Fatal(err)
	}
	job := core.SkillJob{ID: "job-decide", WorkflowID: workflow.ID, Scope: signal.Scope, SkillID: candidate.SkillID, Stage: core.SkillStageDecide, InputDigest: digest, PolicyVersion: signals.PolicyVersion}
	return policyAdapterFixture{repository: repository, adapter: adapter, job: job}
}
