package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillCanaryAnalyzerAutomaticallyPromotesEligibleLowRisk(t *testing.T) {
	fixture := newCanaryAnalysisFixture()
	result, err := fixture.analyzer.Analyze(context.Background(), fixture.input())
	if err != nil || result.Decision.Decision != core.SkillDecisionPromote || result.Activation == nil {
		t.Fatalf("analysis = %+v, %v", result, err)
	}
	if fixture.activator.calls != 1 || fixture.policy.last.CanarySamples != 20 || fixture.policy.last.BaselineCanaryVerifiedSuccessRate != .9 {
		t.Fatalf("policy input = %+v, activation calls %d", fixture.policy.last, fixture.activator.calls)
	}
}

func TestSkillCanaryAnalyzerLeavesInsufficientOrRegressedEvidenceUnpromoted(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*canaryAnalysisFixture)
		want   core.SkillPromotionDecision
	}{
		{name: "insufficient", mutate: func(f *canaryAnalysisFixture) { f.repository.aggregates[0].VerifiedSamples = 5 }, want: core.SkillDecisionCanary},
		{name: "baseline regression", mutate: func(f *canaryAnalysisFixture) { f.repository.aggregates[0].VerifiedSuccesses = 17 }, want: core.SkillDecisionPause},
		{name: "evaluator gap", mutate: func(f *canaryAnalysisFixture) {
			f.repository.aggregates[0].VerifiedSamples = 0
			f.repository.aggregates[0].VerifiedSuccesses = 0
		}, want: core.SkillDecisionCanary},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanaryAnalysisFixture()
			test.mutate(&fixture)
			result, err := fixture.analyzer.Analyze(context.Background(), fixture.input())
			if err != nil || result.Decision.Decision != test.want || result.Activation != nil || fixture.activator.calls != 0 {
				t.Fatalf("analysis = %+v, calls %d, err %v", result, fixture.activator.calls, err)
			}
		})
	}
}

func TestSkillCanaryAnalyzerRefusesHighRiskAutomation(t *testing.T) {
	fixture := newCanaryAnalysisFixture()
	fixture.repository.revision.RiskTier = core.SkillRiskHigh
	if _, err := fixture.analyzer.Analyze(context.Background(), fixture.input()); err == nil {
		t.Fatal("high-risk automatic promotion was accepted")
	}
	if fixture.activator.calls != 0 {
		t.Fatal("high-risk revision reached activation")
	}
}

func TestSkillCanaryAnalyzerPromotionReplayIsIdempotent(t *testing.T) {
	fixture := newCanaryAnalysisFixture()
	first, err := fixture.analyzer.Analyze(context.Background(), fixture.input())
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.analyzer.Analyze(context.Background(), fixture.input())
	if err != nil || first.Activation.Generation != second.Activation.Generation {
		t.Fatalf("replay = %+v, %v", second, err)
	}
}

type canaryAnalysisRepository struct {
	revision   core.SkillRevision
	aggregates []core.SkillExecutionAggregate
}

func (r *canaryAnalysisRepository) GetSkillRevision(_ context.Context, workspace, revisionID string) (core.SkillRevision, error) {
	if workspace != r.revision.Workspace || revisionID != r.revision.ID {
		return core.SkillRevision{}, errors.New("revision not found")
	}
	return r.revision, nil
}

func (r *canaryAnalysisRepository) ListVerifiedSkillExecutionAggregates(context.Context, string, string, string, time.Time) ([]core.SkillExecutionAggregate, error) {
	return append([]core.SkillExecutionAggregate(nil), r.aggregates...), nil
}

type canaryPolicyDecider struct {
	last SkillPolicyInput
	now  time.Time
}

func (p *canaryPolicyDecider) Decide(_ context.Context, input SkillPolicyInput) (core.SkillPolicyDecision, error) {
	p.last = input
	decision := core.SkillDecisionPromote
	reason := "all_policy_gates_passed"
	if input.CanarySamples < 10 {
		decision, reason = core.SkillDecisionCanary, "canary_samples_required"
	} else if input.CanaryVerifiedSuccessRate < input.BaselineCanaryVerifiedSuccessRate {
		decision, reason = core.SkillDecisionPause, "canary_baseline_regression"
	}
	return core.SkillPolicyDecision{ID: input.DecisionID, Workspace: input.Workspace, SkillID: input.SkillID, RevisionID: input.RevisionID, PolicyID: input.PolicyID, PolicyVersion: input.PolicyVersion, EvaluationRunIDs: []string{input.CandidateRunID, input.BaselineRunID}, RiskTier: core.SkillRiskLow, Decision: decision, ReasonCodes: []string{reason}, DecidedAt: p.now}, nil
}

type canaryActivator struct {
	calls      int
	activation core.SkillActivation
}

func (a *canaryActivator) Activate(_ context.Context, _ SkillActivationRequest) (core.SkillActivation, error) {
	a.calls++
	return a.activation, nil
}

type canaryAnalysisFixture struct {
	repository *canaryAnalysisRepository
	policy     *canaryPolicyDecider
	activator  *canaryActivator
	analyzer   *SkillCanaryAnalyzer
	now        time.Time
}

func newCanaryAnalysisFixture() canaryAnalysisFixture {
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	revision := resolverRevision("revision-2", 2, core.SkillRevisionCanary, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now)
	repository := &canaryAnalysisRepository{revision: revision, aggregates: []core.SkillExecutionAggregate{{Workspace: "ws", Environment: "local", SkillID: "skill-1", RevisionID: "revision-2", VerifiedSamples: 20, VerifiedSuccesses: 20, Failures: 0, AverageDurationMS: 80}, {Workspace: "ws", Environment: "local", SkillID: "skill-1", RevisionID: "revision-1", VerifiedSamples: 20, VerifiedSuccesses: 18, Failures: 2, AverageDurationMS: 100}}}
	policy := &canaryPolicyDecider{now: now}
	activation := core.SkillActivation{ID: "activation-1", Workspace: "ws", Environment: "local", SkillID: "skill-1", ActiveRevisionID: revision.ID, ActiveDigest: revision.BundleDigest, LastKnownGoodRevisionID: "revision-1", LastKnownGoodDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 2, PolicyDecisionID: "decision-canary", Materialization: core.SkillMaterializationReady, ActivatedBy: "controller", ActivatedAt: now, UpdatedAt: now}
	activator := &canaryActivator{activation: activation}
	analyzer := NewSkillCanaryAnalyzer(repository, policy, activator)
	return canaryAnalysisFixture{repository: repository, policy: policy, activator: activator, analyzer: analyzer, now: now}
}

func (f canaryAnalysisFixture) input() SkillCanaryAnalysisInput {
	return SkillCanaryAnalysisInput{DecisionID: "decision-canary", OperationID: "operation-canary", IdempotencyKey: "canary-window-1", Workspace: "ws", Environment: "local", SkillID: "skill-1", CandidateRevisionID: "revision-2", BaselineRevisionID: "revision-1", PolicyID: "policy-1", PolicyVersion: 1, CandidateRunID: "candidate-run", BaselineRunID: "baseline-run", ExpectedGeneration: 1, Actor: "canary-controller", WindowStartedAt: f.now.Add(-time.Hour)}
}
