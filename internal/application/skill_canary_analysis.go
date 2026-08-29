package application

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillCanaryAnalysisInput struct {
	DecisionID          string    `json:"decision_id"`
	OperationID         string    `json:"operation_id"`
	IdempotencyKey      string    `json:"idempotency_key"`
	Workspace           string    `json:"workspace"`
	Environment         string    `json:"environment"`
	SkillID             string    `json:"skill_id"`
	CandidateRevisionID string    `json:"candidate_revision_id"`
	BaselineRevisionID  string    `json:"baseline_revision_id"`
	PolicyID            string    `json:"policy_id"`
	PolicyVersion       int64     `json:"policy_version"`
	CandidateRunID      string    `json:"candidate_run_id"`
	BaselineRunID       string    `json:"baseline_run_id"`
	ExpectedGeneration  int64     `json:"expected_generation"`
	Actor               string    `json:"actor"`
	WindowStartedAt     time.Time `json:"window_started_at"`
}

type SkillCanaryAnalysisResult struct {
	Decision   core.SkillPolicyDecision `json:"decision"`
	Activation *core.SkillActivation    `json:"activation,omitempty"`
}

type skillCanaryAnalysisRepository interface {
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	ListVerifiedSkillExecutionAggregates(context.Context, string, string, string, time.Time) ([]core.SkillExecutionAggregate, error)
}

type skillPolicyDecider interface {
	Decide(context.Context, SkillPolicyInput) (core.SkillPolicyDecision, error)
}

type skillRevisionActivator interface {
	Activate(context.Context, SkillActivationRequest) (core.SkillActivation, error)
}

type SkillCanaryAnalyzer struct {
	repository skillCanaryAnalysisRepository
	policy     skillPolicyDecider
	activator  skillRevisionActivator
}

func NewSkillCanaryAnalyzer(repository skillCanaryAnalysisRepository, policy skillPolicyDecider, activator skillRevisionActivator) *SkillCanaryAnalyzer {
	return &SkillCanaryAnalyzer{repository: repository, policy: policy, activator: activator}
}

func (a *SkillCanaryAnalyzer) Analyze(ctx context.Context, input SkillCanaryAnalysisInput) (SkillCanaryAnalysisResult, error) {
	if a == nil || a.repository == nil || a.policy == nil || a.activator == nil || input.WindowStartedAt.IsZero() {
		return SkillCanaryAnalysisResult{}, errors.New("canary analyzer dependencies and window are required")
	}
	revision, err := a.repository.GetSkillRevision(ctx, input.Workspace, input.CandidateRevisionID)
	if err != nil {
		return SkillCanaryAnalysisResult{}, err
	}
	if revision.SkillID != input.SkillID || revision.State != core.SkillRevisionCanary {
		return SkillCanaryAnalysisResult{}, errors.New("candidate revision is not in canary")
	}
	if revision.RiskTier == core.SkillRiskHigh {
		return SkillCanaryAnalysisResult{}, errors.New("high-risk revision cannot be promoted automatically")
	}
	aggregates, err := a.repository.ListVerifiedSkillExecutionAggregates(ctx, input.Workspace, input.Environment, input.SkillID, input.WindowStartedAt)
	if err != nil {
		return SkillCanaryAnalysisResult{}, err
	}
	candidate, baseline := findSkillAggregate(aggregates, input.CandidateRevisionID), findSkillAggregate(aggregates, input.BaselineRevisionID)
	policyInput := SkillPolicyInput{
		DecisionID: input.DecisionID, Workspace: input.Workspace, SkillID: input.SkillID, RevisionID: input.CandidateRevisionID,
		PolicyID: input.PolicyID, PolicyVersion: input.PolicyVersion, CandidateRunID: input.CandidateRunID, BaselineRunID: input.BaselineRunID,
		CanarySamples: int(candidate.VerifiedSamples), CanaryVerifiedSuccessRate: aggregateSuccessRate(candidate),
		BaselineCanaryVerifiedSuccessRate: aggregateSuccessRate(baseline), CanaryFailureRate: aggregateFailureRate(candidate),
		HarmfulFeedbackCount: int(candidate.HarmfulFeedback), EfficiencyImprovement: aggregateEfficiency(candidate, baseline),
	}
	decision, err := a.policy.Decide(ctx, policyInput)
	if err != nil {
		return SkillCanaryAnalysisResult{}, err
	}
	result := SkillCanaryAnalysisResult{Decision: decision}
	if decision.Decision != core.SkillDecisionPromote {
		return result, nil
	}
	if revision.RiskTier != core.SkillRiskLow {
		return SkillCanaryAnalysisResult{}, errors.New("automatic activation is limited to low-risk revisions")
	}
	activation, err := a.activator.Activate(ctx, SkillActivationRequest{OperationID: input.OperationID, IdempotencyKey: input.IdempotencyKey, Workspace: input.Workspace, Environment: input.Environment, SkillID: input.SkillID, TargetRevisionID: input.CandidateRevisionID, ExpectedGeneration: input.ExpectedGeneration, PolicyDecisionID: decision.ID, Actor: input.Actor})
	if err != nil {
		return SkillCanaryAnalysisResult{}, err
	}
	result.Activation = &activation
	return result, nil
}

func findSkillAggregate(items []core.SkillExecutionAggregate, revisionID string) core.SkillExecutionAggregate {
	for _, item := range items {
		if item.RevisionID == revisionID {
			return item
		}
	}
	return core.SkillExecutionAggregate{RevisionID: revisionID}
}

func aggregateSuccessRate(item core.SkillExecutionAggregate) float64 {
	if item.VerifiedSamples == 0 {
		return 0
	}
	return float64(item.VerifiedSuccesses) / float64(item.VerifiedSamples)
}

func aggregateFailureRate(item core.SkillExecutionAggregate) float64 {
	if item.VerifiedSamples == 0 {
		return 0
	}
	return float64(item.Failures) / float64(item.VerifiedSamples)
}

func aggregateEfficiency(candidate, baseline core.SkillExecutionAggregate) float64 {
	if baseline.AverageDurationMS <= 0 {
		return 0
	}
	return (baseline.AverageDurationMS - candidate.AverageDurationMS) / baseline.AverageDurationMS
}
