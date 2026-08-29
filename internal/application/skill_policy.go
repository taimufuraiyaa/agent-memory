package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillPolicyInput struct {
	DecisionID                string  `json:"decision_id"`
	Workspace                 string  `json:"workspace"`
	SkillID                   string  `json:"skill_id"`
	RevisionID                string  `json:"revision_id"`
	PolicyID                  string  `json:"policy_id"`
	PolicyVersion             int64   `json:"policy_version"`
	CandidateRunID            string  `json:"candidate_run_id"`
	BaselineRunID             string  `json:"baseline_run_id"`
	CanarySamples             int     `json:"canary_samples"`
	CanaryVerifiedSuccessRate float64 `json:"canary_verified_success_rate"`
	CanaryFailureRate         float64 `json:"canary_failure_rate"`
	HarmfulFeedbackCount      int     `json:"harmful_feedback_count"`
	EfficiencyImprovement     float64 `json:"efficiency_improvement"`
}

type skillPolicyRepositoryContract interface {
	GetSkillPromotionPolicy(context.Context, string, string, int64) (core.SkillPromotionPolicy, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillEvaluationRun(context.Context, string, string) (core.SkillEvaluationRun, error)
	CreateSkillPolicyDecision(context.Context, core.SkillPolicyDecision) error
}

type SkillPolicyEngine struct {
	repository skillPolicyRepositoryContract
	now        func() time.Time
}

func NewSkillPolicyEngine(repository skillPolicyRepositoryContract, now func() time.Time) *SkillPolicyEngine {
	if now == nil {
		now = time.Now
	}
	return &SkillPolicyEngine{repository: repository, now: now}
}

func (e *SkillPolicyEngine) Decide(ctx context.Context, input SkillPolicyInput) (core.SkillPolicyDecision, error) {
	if e == nil || e.repository == nil {
		return core.SkillPolicyDecision{}, errors.New("skill policy engine repository is required")
	}
	if err := validateSkillPolicyInput(input); err != nil {
		return core.SkillPolicyDecision{}, err
	}
	policy, err := e.repository.GetSkillPromotionPolicy(ctx, input.Workspace, input.PolicyID, input.PolicyVersion)
	if err != nil {
		return core.SkillPolicyDecision{}, err
	}
	revision, err := e.repository.GetSkillRevision(ctx, input.Workspace, input.RevisionID)
	if err != nil {
		return core.SkillPolicyDecision{}, err
	}
	if revision.SkillID != input.SkillID || revision.RiskTier != policy.RiskTier {
		return core.SkillPolicyDecision{}, errors.New("promotion policy risk or skill binding does not match revision")
	}
	candidate, err := e.repository.GetSkillEvaluationRun(ctx, input.Workspace, input.CandidateRunID)
	if err != nil {
		return core.SkillPolicyDecision{}, err
	}
	baseline, err := e.repository.GetSkillEvaluationRun(ctx, input.Workspace, input.BaselineRunID)
	if err != nil {
		return core.SkillPolicyDecision{}, err
	}
	decision, reasons := evaluatePromotionPolicy(input, policy, revision, candidate, baseline)
	result := core.SkillPolicyDecision{
		ID: input.DecisionID, Workspace: input.Workspace, SkillID: input.SkillID, RevisionID: input.RevisionID,
		PolicyID: policy.ID, PolicyVersion: policy.Version, EvaluationRunIDs: []string{candidate.ID, baseline.ID},
		RiskTier: revision.RiskTier, Decision: decision, ReasonCodes: reasons, DecidedAt: e.now().UTC(),
	}
	if err := result.Validate(); err != nil {
		return core.SkillPolicyDecision{}, err
	}
	if err := e.repository.CreateSkillPolicyDecision(ctx, result); err != nil {
		return core.SkillPolicyDecision{}, err
	}
	return result, nil
}

func evaluatePromotionPolicy(input SkillPolicyInput, policy core.SkillPromotionPolicy, revision core.SkillRevision, candidate, baseline core.SkillEvaluationRun) (core.SkillPromotionDecision, []string) {
	if candidate.SkillID != input.SkillID || candidate.RevisionID != revision.ID || candidate.RevisionDigest != revision.BundleDigest || candidate.BaselineRevisionID != baseline.RevisionID || candidate.BaselineDigest != baseline.RevisionDigest || candidate.SuiteID != baseline.SuiteID || candidate.SuiteVersion != baseline.SuiteVersion || candidate.SuiteDigest != baseline.SuiteDigest || candidate.EnvironmentFingerprint != baseline.EnvironmentFingerprint {
		return core.SkillDecisionPause, []string{"stale_evaluation_evidence"}
	}
	if input.HarmfulFeedbackCount > 0 || hasAbsoluteSafetyFailure(candidate) {
		return core.SkillDecisionReject, []string{"absolute_safety_gate_failed"}
	}
	if candidate.Verdict == core.SkillEvaluationInconclusive || baseline.Verdict == core.SkillEvaluationInconclusive {
		return core.SkillDecisionPause, []string{"evaluation_inconclusive"}
	}
	if candidate.Verdict != core.SkillEvaluationPass {
		return core.SkillDecisionReject, []string{"required_evaluation_failed"}
	}
	candidateRate := independentlyVerifiedSuccessRate(candidate.CaseResults)
	baselineRate := independentlyVerifiedSuccessRate(baseline.CaseResults)
	if candidateRate < policy.MinimumVerifiedSuccessRate || candidateRate < baselineRate {
		return core.SkillDecisionReject, []string{"non_inferiority_gate_failed"}
	}
	if revision.RiskTier == core.SkillRiskMedium || revision.RiskTier == core.SkillRiskHigh || !policy.AllowAutomaticActivation {
		return core.SkillDecisionApprovalRequired, []string{"accountable_approval_required"}
	}
	if input.CanarySamples < policy.MinimumCanarySamples {
		return core.SkillDecisionCanary, []string{"canary_samples_required"}
	}
	if input.CanaryVerifiedSuccessRate < policy.MinimumVerifiedSuccessRate || input.CanaryFailureRate > policy.MaximumFailureRate {
		return core.SkillDecisionPause, []string{"canary_quality_gate_failed"}
	}
	return core.SkillDecisionPromote, []string{"all_policy_gates_passed"}
}

func hasAbsoluteSafetyFailure(run core.SkillEvaluationRun) bool {
	for _, result := range run.CaseResults {
		failure := strings.ToLower(result.FailureClass)
		if strings.Contains(failure, "safety") || strings.Contains(failure, "unauthorized") || strings.Contains(failure, "harmful") {
			return true
		}
	}
	return false
}

func independentlyVerifiedSuccessRate(results []core.SkillEvaluationCaseResult) float64 {
	if len(results) == 0 {
		return 0
	}
	successes := 0
	for _, result := range results {
		if result.Passed && result.IndependentlyVerified {
			successes++
		}
	}
	return float64(successes) / float64(len(results))
}

func validateSkillPolicyInput(input SkillPolicyInput) error {
	for field, value := range map[string]string{"decision_id": input.DecisionID, "workspace": input.Workspace, "skill_id": input.SkillID, "revision_id": input.RevisionID, "policy_id": input.PolicyID, "candidate_run_id": input.CandidateRunID, "baseline_run_id": input.BaselineRunID} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return fmt.Errorf("skill policy %s is required and bounded", field)
		}
	}
	if input.PolicyVersion < 1 || input.CanarySamples < 0 || input.HarmfulFeedbackCount < 0 || input.CanaryVerifiedSuccessRate < 0 || input.CanaryVerifiedSuccessRate > 1 || input.CanaryFailureRate < 0 || input.CanaryFailureRate > 1 {
		return errors.New("skill policy version or evidence metrics are invalid")
	}
	return nil
}
