package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillPolicyEngineLowRiskProgression(t *testing.T) {
	fixture := newSkillPolicyFixture(core.SkillRiskLow)
	decision, err := fixture.engine.Decide(context.Background(), fixture.input())
	if err != nil || decision.Decision != core.SkillDecisionCanary {
		t.Fatalf("pre-canary decision = %+v, %v", decision, err)
	}
	input := fixture.input()
	input.CanarySamples = 20
	input.CanaryVerifiedSuccessRate = .98
	input.CanaryFailureRate = .01
	decision, err = fixture.engine.Decide(context.Background(), input)
	if err != nil || decision.Decision != core.SkillDecisionPromote {
		t.Fatalf("post-canary decision = %+v, %v", decision, err)
	}
}

func TestSkillPolicyEngineRequiresApprovalForMediumAndHighRisk(t *testing.T) {
	for _, risk := range []core.SkillRiskTier{core.SkillRiskMedium, core.SkillRiskHigh} {
		fixture := newSkillPolicyFixture(risk)
		decision, err := fixture.engine.Decide(context.Background(), fixture.input())
		if err != nil || decision.Decision != core.SkillDecisionApprovalRequired {
			t.Fatalf("risk %s decision = %+v, %v", risk, decision, err)
		}
	}
}

func TestSkillPolicyEngineRejectsSafetyBeforePerformance(t *testing.T) {
	fixture := newSkillPolicyFixture(core.SkillRiskLow)
	run := fixture.repository.runs["candidate-run"]
	run.Verdict = core.SkillEvaluationFail
	run.CaseResults[0] = core.SkillEvaluationCaseResult{CaseID: "safety", FailureClass: "safety_violation", DurationMS: 1}
	fixture.repository.runs[run.ID] = run
	input := fixture.input()
	input.EfficiencyImprovement = .9
	decision, err := fixture.engine.Decide(context.Background(), input)
	if err != nil || decision.Decision != core.SkillDecisionReject || decision.ReasonCodes[0] != "absolute_safety_gate_failed" {
		t.Fatalf("safety decision = %+v, %v", decision, err)
	}
}

func TestSkillPolicyEnginePausesStaleOrNonInferiorEvidence(t *testing.T) {
	fixture := newSkillPolicyFixture(core.SkillRiskLow)
	run := fixture.repository.runs["candidate-run"]
	run.RevisionDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	fixture.repository.runs[run.ID] = run
	decision, err := fixture.engine.Decide(context.Background(), fixture.input())
	if err != nil || decision.Decision != core.SkillDecisionPause || decision.ReasonCodes[0] != "stale_evaluation_evidence" {
		t.Fatalf("stale decision = %+v, %v", decision, err)
	}

	fixture = newSkillPolicyFixture(core.SkillRiskLow)
	run = fixture.repository.runs["candidate-run"]
	run.CaseResults[0].Passed = false
	run.Verdict = core.SkillEvaluationFail
	fixture.repository.runs[run.ID] = run
	decision, err = fixture.engine.Decide(context.Background(), fixture.input())
	if err != nil || decision.Decision != core.SkillDecisionReject {
		t.Fatalf("non-inferior decision = %+v, %v", decision, err)
	}
}

func TestSkillPolicyEngineBindsHistoricalPolicyVersion(t *testing.T) {
	fixture := newSkillPolicyFixture(core.SkillRiskLow)
	fixture.repository.policy.Version = 7
	input := fixture.input()
	input.PolicyVersion = 7
	decision, err := fixture.engine.Decide(context.Background(), input)
	if err != nil || decision.PolicyVersion != 7 || fixture.repository.saved.PolicyVersion != 7 {
		t.Fatalf("historical policy decision = %+v, %v", decision, err)
	}
}

type skillPolicyRepository struct {
	policy   core.SkillPromotionPolicy
	revision core.SkillRevision
	runs     map[string]core.SkillEvaluationRun
	saved    core.SkillPolicyDecision
}

func (r *skillPolicyRepository) GetSkillPromotionPolicy(_ context.Context, workspace, policyID string, version int64) (core.SkillPromotionPolicy, error) {
	if workspace != r.policy.Workspace || policyID != r.policy.ID || version != r.policy.Version {
		return core.SkillPromotionPolicy{}, errors.New("policy not found")
	}
	return r.policy, nil
}

func (r *skillPolicyRepository) GetSkillRevision(_ context.Context, workspace, revisionID string) (core.SkillRevision, error) {
	if workspace != r.revision.Workspace || revisionID != r.revision.ID {
		return core.SkillRevision{}, errors.New("revision not found")
	}
	return r.revision, nil
}

func (r *skillPolicyRepository) GetSkillEvaluationRun(_ context.Context, workspace, runID string) (core.SkillEvaluationRun, error) {
	run, exists := r.runs[runID]
	if !exists || run.Workspace != workspace {
		return core.SkillEvaluationRun{}, errors.New("run not found")
	}
	return run, nil
}

func (r *skillPolicyRepository) CreateSkillPolicyDecision(_ context.Context, decision core.SkillPolicyDecision) error {
	r.saved = decision
	return nil
}

type skillPolicyFixture struct {
	repository *skillPolicyRepository
	engine     *SkillPolicyEngine
	now        time.Time
}

func newSkillPolicyFixture(risk core.SkillRiskTier) skillPolicyFixture {
	now := time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC)
	revision := resolverRevision("revision-2", 2, core.SkillRevisionTesting, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now)
	revision.RiskTier = risk
	policy := core.SkillPromotionPolicy{ID: "policy-1", Workspace: "ws", Version: 1, RiskTier: risk, MinimumCanarySamples: 10, MinimumVerifiedSuccessRate: .95, MaximumFailureRate: .02, AllowAutomaticActivation: risk == core.SkillRiskLow, CreatedBy: "operator", CreatedAt: now}
	candidate := policyRun("candidate-run", revision.ID, revision.BundleDigest, now)
	baseline := policyRun("baseline-run", "revision-1", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", now)
	candidate.BaselineRevisionID, candidate.BaselineDigest = baseline.RevisionID, baseline.RevisionDigest
	repository := &skillPolicyRepository{policy: policy, revision: revision, runs: map[string]core.SkillEvaluationRun{candidate.ID: candidate, baseline.ID: baseline}}
	return skillPolicyFixture{repository: repository, engine: NewSkillPolicyEngine(repository, func() time.Time { return now }), now: now}
}

func (f skillPolicyFixture) input() SkillPolicyInput {
	return SkillPolicyInput{DecisionID: "decision-1", Workspace: "ws", SkillID: "skill-1", RevisionID: f.repository.revision.ID, PolicyID: f.repository.policy.ID, PolicyVersion: f.repository.policy.Version, CandidateRunID: "candidate-run", BaselineRunID: "baseline-run"}
}

func policyRun(id, revisionID, digest string, now time.Time) core.SkillEvaluationRun {
	return core.SkillEvaluationRun{ID: id, Workspace: "ws", SkillID: "skill-1", RevisionID: revisionID, RevisionDigest: digest, SuiteID: "suite-1", SuiteVersion: 1, SuiteDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Evaluator: "runner", EvaluatorVersion: "v1", EnvironmentFingerprint: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Verdict: core.SkillEvaluationPass, CaseResults: []core.SkillEvaluationCaseResult{{CaseID: "safety", Passed: true, IndependentlyVerified: true}, {CaseID: "positive", Passed: true, IndependentlyVerified: true}}, StartedAt: now, CompletedAt: now}
}
