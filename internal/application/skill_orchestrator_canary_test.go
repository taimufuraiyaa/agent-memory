package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillCanaryStartAdapterEligibilityReplayAndGeneration(t *testing.T) {
	for _, test := range []struct {
		name     string
		risk     core.SkillRiskTier
		decision core.SkillPromotionDecision
		approved bool
		wantErr  bool
	}{
		{name: "eligible low", risk: core.SkillRiskLow, decision: core.SkillDecisionCanary},
		{name: "approved medium", risk: core.SkillRiskMedium, decision: core.SkillDecisionApprovalRequired, approved: true},
		{name: "unapproved medium", risk: core.SkillRiskMedium, decision: core.SkillDecisionApprovalRequired, wantErr: true},
		{name: "high denial", risk: core.SkillRiskHigh, decision: core.SkillDecisionApprovalRequired, approved: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanaryStartAdapterFixture(t, test.risk, test.decision, test.approved)
			result, err := fixture.adapter.Execute(context.Background(), fixture.job)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ineligible canary started: %+v", result)
				}
				return
			}
			if err != nil || result.ResultKind != core.SkillJobResultSucceeded || fixture.repository.starts != 1 {
				t.Fatalf("canary start = %+v starts=%d err=%v", result, fixture.repository.starts, err)
			}
			if _, err := fixture.adapter.Execute(context.Background(), fixture.job); err != nil || fixture.repository.starts != 1 {
				t.Fatalf("canary replay starts=%d err=%v", fixture.repository.starts, err)
			}
		})
	}

	fixture := newCanaryStartAdapterFixture(t, core.SkillRiskLow, core.SkillDecisionCanary, false)
	fixture.repository.startErr = errors.New("activation generation is stale")
	_, err := fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureContention, "canary_generation_stale")
}

func TestSkillCanaryStartAdapterHonorsPolicyDisablement(t *testing.T) {
	fixture := newCanaryStartAdapterFixture(t, core.SkillRiskLow, core.SkillDecisionCanary, false)
	fixture.adapter.configuration.Enabled = false
	_, err := fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailurePolicyBlock, "canary_policy_disabled")
	if fixture.repository.starts != 0 {
		t.Fatal("disabled policy reached canary repository")
	}
}

func TestSkillCanaryDueSchedulerBoundsLowTrafficAndDeduplicatesWakeups(t *testing.T) {
	now := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	configuration := canaryStageTestConfiguration()
	router := &canarySignalRouter{seen: map[string]struct{}{}}
	scheduler, err := NewSkillCanaryDueScheduler(router, configuration)
	if err != nil {
		t.Fatal(err)
	}
	revision := resolverRevision("revision-2", 2, core.SkillRevisionCanary, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now)
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: "skill-1",
		ActiveRevisionID: "revision-1", ActiveDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanaryRevisionID: revision.ID, CanaryDigest: revision.BundleDigest, Generation: 2, PolicyDecisionID: "decision-1",
		Materialization: core.SkillMaterializationReady, ActivatedBy: "controller", ActivatedAt: now, UpdatedAt: now}

	insufficient, err := scheduler.Schedule(context.Background(), SkillCanaryDueRequest{Activation: activation, Revision: revision, WindowStarted: now, VerifiedSamples: 5, Now: now.Add(10 * time.Minute)})
	if err != nil || insufficient.Due || insufficient.NextAt.IsZero() || !insufficient.Route.Created || router.calls != 1 {
		t.Fatalf("insufficient schedule = %+v calls=%d err=%v", insufficient, router.calls, err)
	}
	maximum, err := scheduler.Schedule(context.Background(), SkillCanaryDueRequest{Activation: activation, Revision: revision, WindowStarted: now, VerifiedSamples: 5, Now: now.Add(time.Hour)})
	if err != nil || !maximum.Due || !maximum.MaximumAge || !maximum.Route.Created {
		t.Fatalf("maximum-age schedule = %+v, %v", maximum, err)
	}
	replay, err := scheduler.Schedule(context.Background(), SkillCanaryDueRequest{Activation: activation, Revision: revision, WindowStarted: now, VerifiedSamples: 5, Now: now.Add(time.Hour)})
	if err != nil || replay.Route.Created || router.calls != 3 {
		t.Fatalf("duplicate wakeup = %+v calls=%d err=%v", replay, router.calls, err)
	}
	due, err := scheduler.Schedule(context.Background(), SkillCanaryDueRequest{Activation: activation, Revision: revision, WindowStarted: now, VerifiedSamples: 10, Now: now.Add(5 * time.Minute)})
	if err != nil || !due.Due || due.MaximumAge {
		t.Fatalf("sample/time due = %+v, %v", due, err)
	}
}

func TestSkillPolicyEngineEscalatesMaximumAgeWithoutLoweringSamples(t *testing.T) {
	fixture := newSkillPolicyFixture(core.SkillRiskLow)
	input := fixture.input()
	input.MaximumCanaryAgeExceeded = true
	decision, err := fixture.engine.Decide(context.Background(), input)
	if err != nil || decision.Decision != core.SkillDecisionApprovalRequired || decision.ReasonCodes[0] != "canary_maximum_age_insufficient" {
		t.Fatalf("maximum-age decision = %+v, %v", decision, err)
	}
}

func TestSkillCanaryAnalysisAdapterRejectsAmbiguityAndPreservesRegressionEvidence(t *testing.T) {
	fixture := newCanaryAnalysisAdapterFixture(t)
	result, err := fixture.adapter.Execute(context.Background(), fixture.job)
	if err != nil || len(result.References) != 1 || fixture.policy.last.CanaryVerifiedSuccessRate >= fixture.policy.last.BaselineCanaryVerifiedSuccessRate {
		t.Fatalf("regression analysis = %+v policy=%+v err=%v", result, fixture.policy.last, err)
	}
	if !fixture.policy.last.MaximumCanaryAgeExceeded || fixture.policy.last.CanarySamples != 20 {
		t.Fatalf("bounded analysis evidence = %+v", fixture.policy.last)
	}

	fixture = newCanaryAnalysisAdapterFixture(t)
	fixture.repository.aggregates = fixture.repository.aggregates[:1]
	_, err = fixture.adapter.Execute(context.Background(), fixture.job)
	assertSkillStageFailure(t, err, core.SkillFailureInsufficientEvidence, "canary_evidence_ambiguous")
}

type canaryStartAdapterRepository struct {
	revision   core.SkillRevision
	decision   core.SkillPolicyDecision
	activation core.SkillActivation
	workflow   core.SkillWorkflow
	approved   bool
	starts     int
	startErr   error
}

func (r *canaryStartAdapterRepository) GetSkillRevision(_ context.Context, workspace, id string) (core.SkillRevision, error) {
	if workspace != r.revision.Workspace || id != r.revision.ID {
		return core.SkillRevision{}, errors.New("revision not found")
	}
	return r.revision, nil
}

func (r *canaryStartAdapterRepository) GetSkillPolicyDecision(_ context.Context, workspace, id string) (core.SkillPolicyDecision, error) {
	if workspace != r.decision.Workspace || id != r.decision.ID {
		return core.SkillPolicyDecision{}, errors.New("decision not found")
	}
	return r.decision, nil
}

func (r *canaryStartAdapterRepository) HasEffectiveSkillApproval(context.Context, string, string, string) (bool, error) {
	return r.approved, nil
}

func (r *canaryStartAdapterRepository) GetSkillWorkflow(_ context.Context, scope core.SkillOrchestratorScope, id string) (core.SkillWorkflow, error) {
	if scope != r.workflow.Scope || id != r.workflow.ID {
		return core.SkillWorkflow{}, errors.New("workflow not found")
	}
	return r.workflow, nil
}

func (r *canaryStartAdapterRepository) GetSkillActivation(_ context.Context, workspace, environment, skillID string) (core.SkillActivation, error) {
	if workspace != r.activation.Workspace || environment != r.activation.Environment || skillID != r.activation.SkillID {
		return core.SkillActivation{}, errors.New("activation not found")
	}
	return r.activation, nil
}

func (r *canaryStartAdapterRepository) StartSkillCanary(_ context.Context, _, _, _, revisionID, decisionID, actor string, expectedGeneration int64, at time.Time) (core.SkillActivation, error) {
	r.starts++
	if r.startErr != nil {
		return core.SkillActivation{}, r.startErr
	}
	if expectedGeneration != r.activation.Generation {
		return core.SkillActivation{}, errors.New("activation generation is stale")
	}
	r.revision.State = core.SkillRevisionCanary
	r.activation.CanaryRevisionID, r.activation.CanaryDigest = revisionID, r.revision.BundleDigest
	r.activation.PolicyDecisionID, r.activation.ActivatedBy = decisionID, actor
	r.activation.Generation++
	r.activation.UpdatedAt = at
	return r.activation, nil
}

type canaryStartAdapterFixture struct {
	repository *canaryStartAdapterRepository
	adapter    *SkillCanaryStartAdapter
	job        core.SkillJob
}

func newCanaryStartAdapterFixture(t *testing.T, risk core.SkillRiskTier, promotion core.SkillPromotionDecision, approved bool) canaryStartAdapterFixture {
	t.Helper()
	now := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	revision := resolverRevision("revision-2", 2, core.SkillRevisionTesting, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now)
	revision.RiskTier = risk
	decision := core.SkillPolicyDecision{ID: "decision-1", Workspace: "ws", SkillID: "skill-1", RevisionID: revision.ID,
		PolicyID: "policy-1", PolicyVersion: 7, EvaluationRunIDs: []string{"candidate-run", "baseline-run"}, RiskTier: risk,
		Decision: promotion, ReasonCodes: []string{"test_policy"}, DecidedAt: now}
	configuration := canaryStageTestConfiguration()
	signal, err := SkillLifecycleSignalForDecision(decision, configuration.Signal)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestSkillLifecycleSignal(signal, nil)
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: "skill-1",
		ActiveRevisionID: "revision-1", ActiveDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Generation: 1, Materialization: core.SkillMaterializationReady, PolicyDecisionID: "import", ActivatedBy: "operator", ActivatedAt: now, UpdatedAt: now}
	workflow := core.SkillWorkflow{ID: "workflow-canary", Scope: signal.Scope, OriginKind: core.SkillWorkflowOriginLifecycleSignal,
		OriginID: decision.ID, InputDigest: digest, ConfigurationVersion: configuration.Signal.ConfigurationVersion, PolicyDigest: configuration.Signal.PolicyDigest}
	repository := &canaryStartAdapterRepository{revision: revision, decision: decision, activation: activation, workflow: workflow, approved: approved}
	adapter, err := NewSkillCanaryStartAdapter(repository, configuration, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	job := core.SkillJob{ID: "job-canary", WorkflowID: workflow.ID, Scope: signal.Scope, SkillID: decision.SkillID,
		Stage: core.SkillStageStartCanary, InputDigest: digest, PolicyVersion: decision.PolicyVersion}
	return canaryStartAdapterFixture{repository: repository, adapter: adapter, job: job}
}

func canaryStageTestConfiguration() SkillCanaryStageConfiguration {
	return SkillCanaryStageConfiguration{Signal: SkillSignalConfiguration{Environment: "local", ConfigurationVersion: 4, PolicyVersion: 7,
		PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}, Enabled: true,
		PolicyID: "policy-1", Actor: "canary-controller", MinimumSamples: 10, MinimumWindowAge: 5 * time.Minute,
		MaximumWindowAge: time.Hour, RecheckInterval: 10 * time.Minute}
}

type canarySignalRouter struct {
	seen  map[string]struct{}
	calls int
}

func (r *canarySignalRouter) Route(_ context.Context, signal SkillLifecycleSignal) (SkillSignalRouteResult, error) {
	r.calls++
	digest := digestSkillLifecycleSignal(signal, nil)
	if _, exists := r.seen[digest]; exists {
		return SkillSignalRouteResult{}, nil
	}
	r.seen[digest] = struct{}{}
	return SkillSignalRouteResult{Created: true}, nil
}

type canaryAnalysisAdapterRepository struct {
	revision   core.SkillRevision
	activation core.SkillActivation
	decision   core.SkillPolicyDecision
	workflow   core.SkillWorkflow
	aggregates []core.SkillExecutionAggregate
}

func (r *canaryAnalysisAdapterRepository) GetSkillRevision(_ context.Context, workspace, id string) (core.SkillRevision, error) {
	if workspace == r.revision.Workspace && id == r.revision.ID {
		return r.revision, nil
	}
	return core.SkillRevision{}, errors.New("revision not found")
}

func (r *canaryAnalysisAdapterRepository) GetSkillWorkflow(_ context.Context, scope core.SkillOrchestratorScope, id string) (core.SkillWorkflow, error) {
	if scope == r.workflow.Scope && id == r.workflow.ID {
		return r.workflow, nil
	}
	return core.SkillWorkflow{}, errors.New("workflow not found")
}

func (r *canaryAnalysisAdapterRepository) GetSkillActivation(_ context.Context, workspace, environment, skillID string) (core.SkillActivation, error) {
	if workspace == r.activation.Workspace && environment == r.activation.Environment && skillID == r.activation.SkillID {
		return r.activation, nil
	}
	return core.SkillActivation{}, errors.New("activation not found")
}

func (r *canaryAnalysisAdapterRepository) GetSkillPolicyDecision(_ context.Context, workspace, id string) (core.SkillPolicyDecision, error) {
	if workspace == r.decision.Workspace && id == r.decision.ID {
		return r.decision, nil
	}
	return core.SkillPolicyDecision{}, errors.New("decision not found")
}

func (r *canaryAnalysisAdapterRepository) ListVerifiedSkillExecutionAggregates(context.Context, string, string, string, time.Time) ([]core.SkillExecutionAggregate, error) {
	return append([]core.SkillExecutionAggregate(nil), r.aggregates...), nil
}

type canaryAnalysisAdapterFixture struct {
	repository *canaryAnalysisAdapterRepository
	policy     *canaryPolicyDecider
	adapter    *SkillCanaryAnalysisAdapter
	job        core.SkillJob
}

func newCanaryAnalysisAdapterFixture(t *testing.T) canaryAnalysisAdapterFixture {
	t.Helper()
	windowStarted := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	configuration := canaryStageTestConfiguration()
	revision := resolverRevision("revision-2", 2, core.SkillRevisionCanary, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, windowStarted)
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: "skill-1",
		ActiveRevisionID: "revision-1", ActiveDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanaryRevisionID: revision.ID, CanaryDigest: revision.BundleDigest, Generation: 2, PolicyDecisionID: "decision-1",
		Materialization: core.SkillMaterializationReady, ActivatedBy: "controller", ActivatedAt: windowStarted, UpdatedAt: windowStarted}
	decision := core.SkillPolicyDecision{ID: "decision-1", Workspace: "ws", SkillID: "skill-1", RevisionID: revision.ID,
		PolicyID: configuration.PolicyID, PolicyVersion: configuration.Signal.PolicyVersion, EvaluationRunIDs: []string{"candidate-run", "baseline-run"},
		RiskTier: core.SkillRiskLow, Decision: core.SkillDecisionCanary, ReasonCodes: []string{"canary_samples_required"}, DecidedAt: windowStarted}
	dueAt := windowStarted.Add(configuration.MaximumWindowAge)
	signal, err := SkillLifecycleSignalForCanary(activation, revision, windowStarted, dueAt, configuration.Signal)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestSkillLifecycleSignal(signal, nil)
	workflow := core.SkillWorkflow{ID: "workflow-analysis", Scope: signal.Scope, OriginKind: core.SkillWorkflowOriginLifecycleSignal,
		OriginID: activation.ID, InputDigest: digest, ConfigurationVersion: configuration.Signal.ConfigurationVersion,
		PolicyDigest: configuration.Signal.PolicyDigest, CreatedAt: dueAt}
	repository := &canaryAnalysisAdapterRepository{revision: revision, activation: activation, decision: decision, workflow: workflow,
		aggregates: []core.SkillExecutionAggregate{{Workspace: "ws", Environment: "local", SkillID: "skill-1", RevisionID: revision.ID, VerifiedSamples: 20, VerifiedSuccesses: 17, Failures: 3, AverageDurationMS: 90},
			{Workspace: "ws", Environment: "local", SkillID: "skill-1", RevisionID: activation.ActiveRevisionID, VerifiedSamples: 20, VerifiedSuccesses: 19, Failures: 1, AverageDurationMS: 100}}}
	policy := &canaryPolicyDecider{now: dueAt}
	adapter, err := NewSkillCanaryAnalysisAdapter(repository, policy, configuration, func() time.Time { return dueAt })
	if err != nil {
		t.Fatal(err)
	}
	job := core.SkillJob{ID: "job-analysis", WorkflowID: workflow.ID, Scope: signal.Scope, SkillID: activation.SkillID,
		Stage: core.SkillStageAnalyzeCanary, InputDigest: digest, PolicyVersion: configuration.Signal.PolicyVersion}
	return canaryAnalysisAdapterFixture{repository: repository, policy: policy, adapter: adapter, job: job}
}
