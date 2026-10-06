package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrSkillEvaluationBudgetExhausted = core.ErrSkillEvaluationBudgetExhausted

type SkillEvaluationStageConfiguration struct {
	Signal                 SkillSignalConfiguration
	SuiteID                string
	SuiteVersion           int64
	SuiteDigest            string
	Evaluator              string
	EvaluatorVersion       string
	EnvironmentFingerprint string
	Timeout                time.Duration
	MaximumCases           int
	BudgetUnits            int64
}

func (c SkillEvaluationStageConfiguration) Validate() error {
	if err := c.Signal.Validate(); err != nil {
		return err
	}
	input := SkillEvaluationInput{ID: "validation", Workspace: "validation", SkillID: "validation",
		CandidateRevisionID: "candidate", BaselineRevisionID: "baseline", SuiteID: c.SuiteID,
		SuiteVersion: c.SuiteVersion, SuiteDigest: c.SuiteDigest, Evaluator: c.Evaluator,
		EvaluatorVersion: c.EvaluatorVersion, EnvironmentFingerprint: c.EnvironmentFingerprint,
		Timeout: c.Timeout, MaximumCases: c.MaximumCases}
	if err := validateSkillEvaluationInput(input); err != nil {
		return err
	}
	if c.BudgetUnits < 1 {
		return errors.New("skill evaluation budget units must be positive")
	}
	return nil
}

type SkillEvaluationBudgetRequest struct {
	Scope         core.SkillOrchestratorScope
	JobID         string
	PolicyVersion int64
	Units         int64
}

type SkillEvaluationBudgetReservation interface {
	Commit(context.Context, int64) error
	Release(context.Context) error
}

type SkillEvaluationBudget interface {
	Reserve(context.Context, SkillEvaluationBudgetRequest) (SkillEvaluationBudgetReservation, error)
}

type SkillEvaluationExecutorReadiness interface {
	CheckSkillEvaluationExecutor(context.Context, string, string, string) error
}

type SkillEvaluationBaselineResolver interface {
	ResolveSkillEvaluationBaseline(context.Context, core.SkillRevision) (core.SkillRevision, error)
}

type SkillEvaluationStageRepository interface {
	skillEvaluationRepositoryContract
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	GetSkillEvaluationRun(context.Context, string, string) (core.SkillEvaluationRun, error)
	TransitionSkillRevisionState(context.Context, string, string, core.SkillRevisionState, core.SkillRevisionState) (core.SkillRevision, error)
}

type SkillEvaluationAdapter struct {
	repository    SkillEvaluationStageRepository
	orchestrator  *SkillEvaluationOrchestrator
	baselines     SkillEvaluationBaselineResolver
	readiness     SkillEvaluationExecutorReadiness
	budget        SkillEvaluationBudget
	configuration SkillEvaluationStageConfiguration
	downstream    SkillLessonSignalRouter
}

func (a *SkillEvaluationAdapter) WithDownstreamRouter(router SkillLessonSignalRouter) *SkillEvaluationAdapter {
	a.downstream = router
	return a
}

func NewSkillEvaluationAdapter(repository SkillEvaluationStageRepository, runner RestrictedSkillEvaluationRunner, baselines SkillEvaluationBaselineResolver, readiness SkillEvaluationExecutorReadiness, budget SkillEvaluationBudget, configuration SkillEvaluationStageConfiguration, now func() time.Time) (*SkillEvaluationAdapter, error) {
	if repository == nil || runner == nil || baselines == nil || readiness == nil || budget == nil {
		return nil, errors.New("skill evaluation adapter dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillEvaluationAdapter{repository: repository, orchestrator: NewSkillEvaluationOrchestrator(repository, runner, now), baselines: baselines, readiness: readiness, budget: budget, configuration: configuration}, nil
}

func SkillLifecycleSignalForRevision(revision core.SkillRevision, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := revision.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if revision.State != core.SkillRevisionDraft && revision.State != core.SkillRevisionTesting {
		return SkillLifecycleSignal{}, errors.New("skill revision is not evaluable")
	}
	return SkillLifecycleSignal{ID: revision.ID, Kind: SkillSignalRevision,
		Scope:   core.SkillOrchestratorScope{WorkspaceID: revision.Workspace, Environment: configuration.Environment},
		SkillID: revision.SkillID, RevisionID: revision.ID, ReferenceID: revision.ID, EvidenceDigest: revision.BundleDigest,
		Verified: true, Authorized: true, ConfigurationVersion: configuration.ConfigurationVersion,
		PolicyVersion: configuration.PolicyVersion, PolicyDigest: configuration.PolicyDigest, OccurredAt: revision.CreatedAt}, nil
}

func (a *SkillEvaluationAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.repository == nil || job.Stage != core.SkillStageEvaluate {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "invalid_evaluation_job", errors.New("invalid evaluation job"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "evaluation_workflow_unavailable", err)
	}
	candidate, err := a.repository.GetSkillRevision(ctx, job.Scope.WorkspaceID, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "candidate_revision_unavailable", err)
	}
	signal, err := SkillLifecycleSignalForRevision(candidate, a.configuration.Signal)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "candidate_revision_ineligible", err)
	}
	expectedDigest := digestSkillLifecycleSignal(signal, nil)
	if workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || job.InputDigest != expectedDigest || workflow.InputDigest != expectedDigest || job.PolicyVersion != a.configuration.Signal.PolicyVersion || workflow.ConfigurationVersion != a.configuration.Signal.ConfigurationVersion || workflow.PolicyDigest != a.configuration.Signal.PolicyDigest {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "evaluation_binding_mismatch", errors.New("evaluation workflow binding mismatch"))
	}
	if candidate.State == core.SkillRevisionDraft {
		candidate, err = a.repository.TransitionSkillRevisionState(ctx, job.Scope.WorkspaceID, candidate.ID, core.SkillRevisionDraft, core.SkillRevisionTesting)
		if err != nil {
			return SkillStageResult{}, evaluationStageError(core.SkillFailureContention, "evaluation_revision_transition_failed", err)
		}
	}
	baseline, err := a.baselines.ResolveSkillEvaluationBaseline(ctx, candidate)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureInsufficientEvidence, "evaluation_baseline_unavailable", err)
	}
	if err := validateEvaluationRevisionPair(candidate, baseline); err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "evaluation_baseline_invalid", err)
	}
	candidateRunID, baselineRunID := job.ID+"-candidate", job.ID+"-baseline"
	if replay, replayErr := a.loadEvaluationReplay(ctx, candidateRunID, baselineRunID, candidate, baseline); replayErr != nil || replay != nil {
		if replayErr != nil {
			return SkillStageResult{}, replayErr
		}
		return a.completeEvaluation(ctx, *replay)
	}
	if err := a.readiness.CheckSkillEvaluationExecutor(ctx, a.configuration.Evaluator, a.configuration.EvaluatorVersion, a.configuration.EnvironmentFingerprint); err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "evaluation_executor_unready", err)
	}
	reservation, err := a.budget.Reserve(ctx, SkillEvaluationBudgetRequest{Scope: job.Scope, JobID: job.ID, PolicyVersion: job.PolicyVersion, Units: a.configuration.BudgetUnits})
	if err != nil {
		class, code := core.SkillFailureDependencyUnavailable, "evaluation_budget_unavailable"
		if errors.Is(err, ErrSkillEvaluationBudgetExhausted) {
			class, code = core.SkillFailurePolicyBlock, "evaluation_budget_exhausted"
		}
		return SkillStageResult{}, evaluationStageError(class, code, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = reservation.Release(context.WithoutCancel(ctx))
		}
	}()
	result, err := a.orchestrator.Evaluate(ctx, SkillEvaluationInput{ID: job.ID, Workspace: job.Scope.WorkspaceID,
		SkillID: candidate.SkillID, CandidateRevisionID: candidate.ID, BaselineRevisionID: baseline.ID,
		SuiteID: a.configuration.SuiteID, SuiteVersion: a.configuration.SuiteVersion, SuiteDigest: a.configuration.SuiteDigest,
		Evaluator: a.configuration.Evaluator, EvaluatorVersion: a.configuration.EvaluatorVersion,
		EnvironmentFingerprint: a.configuration.EnvironmentFingerprint, Timeout: a.configuration.Timeout,
		MaximumCases: a.configuration.MaximumCases})
	if ctx.Err() != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureCancellation, "evaluation_cancelled", ctx.Err())
	}
	if err != nil {
		if ctx.Err() != nil {
			return SkillStageResult{}, evaluationStageError(core.SkillFailureCancellation, "evaluation_cancelled", ctx.Err())
		}
		if replay, replayErr := a.loadEvaluationReplay(ctx, candidateRunID, baselineRunID, candidate, baseline); replayErr == nil && replay != nil {
			result = *replay
		} else {
			return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "evaluation_failed", err)
		}
	}
	if err := reservation.Commit(ctx, a.configuration.BudgetUnits); err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "evaluation_budget_commit_failed", err)
	}
	committed = true
	return a.completeEvaluation(ctx, result)
}

func (a *SkillEvaluationAdapter) completeEvaluation(ctx context.Context, result SkillEvaluationResult) (SkillStageResult, error) {
	if a.downstream != nil {
		next, err := SkillLifecycleSignalForEvaluation(result.Candidate, result.Baseline, a.configuration.Signal)
		if err != nil {
			return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "evaluation_signal_invalid", err)
		}
		if _, err := a.downstream.Route(ctx, next); err != nil {
			return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "evaluation_signal_unavailable", err)
		}
	}
	return evaluationStageResult(result), nil
}

func (a *SkillEvaluationAdapter) loadEvaluationReplay(ctx context.Context, candidateRunID, baselineRunID string, candidate, baseline core.SkillRevision) (*SkillEvaluationResult, error) {
	candidateRun, candidateErr := a.repository.GetSkillEvaluationRun(ctx, candidate.Workspace, candidateRunID)
	baselineRun, baselineErr := a.repository.GetSkillEvaluationRun(ctx, candidate.Workspace, baselineRunID)
	if candidateErr != nil && baselineErr != nil {
		return nil, nil
	}
	if candidateErr != nil || baselineErr != nil || !evaluationRunMatches(candidateRun, candidate, baseline, a.configuration, true) || !evaluationRunMatches(baselineRun, baseline, core.SkillRevision{}, a.configuration, false) {
		return nil, evaluationStageError(core.SkillFailurePermanentValidation, "evaluation_replay_mismatch", errors.New("stored evaluation pair is incomplete or mismatched"))
	}
	return &SkillEvaluationResult{Candidate: candidateRun, Baseline: baselineRun}, nil
}

func evaluationRunMatches(run core.SkillEvaluationRun, revision, baseline core.SkillRevision, configuration SkillEvaluationStageConfiguration, candidate bool) bool {
	if run.Workspace != revision.Workspace || run.SkillID != revision.SkillID || run.RevisionID != revision.ID || run.RevisionDigest != revision.BundleDigest || run.SuiteID != configuration.SuiteID || run.SuiteVersion != configuration.SuiteVersion || run.SuiteDigest != configuration.SuiteDigest || run.Evaluator != configuration.Evaluator || run.EvaluatorVersion != configuration.EvaluatorVersion || run.EnvironmentFingerprint != configuration.EnvironmentFingerprint {
		return false
	}
	if candidate {
		return run.BaselineRevisionID == baseline.ID && run.BaselineDigest == baseline.BundleDigest
	}
	return run.BaselineRevisionID == "" && run.BaselineDigest == ""
}

func validateEvaluationRevisionPair(candidate, baseline core.SkillRevision) error {
	if err := baseline.Validate(); err != nil {
		return err
	}
	if candidate.Workspace != baseline.Workspace || candidate.SkillID != baseline.SkillID || candidate.ID == baseline.ID || candidate.BundleDigest == baseline.BundleDigest {
		return errors.New("candidate and baseline revisions are not distinct and comparable")
	}
	return nil
}

func evaluationStageResult(result SkillEvaluationResult) SkillStageResult {
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceEvaluationRun, ID: result.Candidate.ID}, {Kind: core.SkillReferenceEvaluationRun, ID: result.Baseline.ID}}}
}

func evaluationStageError(class core.SkillJobFailureClass, code string, err error) error {
	return &SkillStageError{Failure: SkillStageFailure{Class: class, Code: code}, Err: err}
}

type SkillPolicyStageConfiguration struct {
	Signal        SkillSignalConfiguration
	PolicyID      string
	CanarySamples int
}

func (c SkillPolicyStageConfiguration) Validate() error {
	if err := c.Signal.Validate(); err != nil {
		return err
	}
	if !validSkillSignalIdentifier(c.PolicyID) || c.CanarySamples < 0 {
		return errors.New("skill policy stage configuration is invalid")
	}
	return nil
}

type SkillPolicyStageRepository interface {
	skillPolicyRepositoryContract
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	GetSkillPolicyDecision(context.Context, string, string) (core.SkillPolicyDecision, error)
}

type SkillPolicyDecisionAdapter struct {
	repository    SkillPolicyStageRepository
	engine        *SkillPolicyEngine
	configuration SkillPolicyStageConfiguration
	downstream    SkillLessonSignalRouter
}

func (a *SkillPolicyDecisionAdapter) WithDownstreamRouter(router SkillLessonSignalRouter) *SkillPolicyDecisionAdapter {
	a.downstream = router
	return a
}

func NewSkillPolicyDecisionAdapter(repository SkillPolicyStageRepository, configuration SkillPolicyStageConfiguration, now func() time.Time) (*SkillPolicyDecisionAdapter, error) {
	if repository == nil {
		return nil, errors.New("skill policy decision adapter repository is required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillPolicyDecisionAdapter{repository: repository, engine: NewSkillPolicyEngine(repository, now), configuration: configuration}, nil
}

func SkillLifecycleSignalForEvaluation(candidate, baseline core.SkillEvaluationRun, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	if err := configuration.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := candidate.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if err := baseline.Validate(); err != nil {
		return SkillLifecycleSignal{}, err
	}
	if candidate.Workspace != baseline.Workspace || candidate.SkillID != baseline.SkillID || candidate.BaselineRevisionID != baseline.RevisionID || candidate.BaselineDigest != baseline.RevisionDigest || candidate.SuiteID != baseline.SuiteID || candidate.SuiteVersion != baseline.SuiteVersion || candidate.SuiteDigest != baseline.SuiteDigest || candidate.EnvironmentFingerprint != baseline.EnvironmentFingerprint {
		return SkillLifecycleSignal{}, errors.New("evaluation runs are not comparable")
	}
	canonical := strings.Join([]string{candidate.ID, candidate.RevisionID, candidate.RevisionDigest, baseline.ID, baseline.RevisionID, baseline.RevisionDigest, candidate.SuiteID, fmt.Sprint(candidate.SuiteVersion), candidate.SuiteDigest, candidate.Evaluator, candidate.EvaluatorVersion, candidate.EnvironmentFingerprint, string(candidate.Verdict), string(baseline.Verdict)}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	occurredAt := candidate.CompletedAt
	if baseline.CompletedAt.After(occurredAt) {
		occurredAt = baseline.CompletedAt
	}
	return SkillLifecycleSignal{ID: candidate.ID, Kind: SkillSignalEvaluation,
		Scope:   core.SkillOrchestratorScope{WorkspaceID: candidate.Workspace, Environment: configuration.Environment},
		SkillID: candidate.SkillID, RevisionID: candidate.RevisionID, ReferenceID: candidate.ID,
		EvidenceDigest: "sha256:" + hex.EncodeToString(digest[:]), Verified: true, Authorized: true,
		ConfigurationVersion: configuration.ConfigurationVersion, PolicyVersion: configuration.PolicyVersion,
		PolicyDigest: configuration.PolicyDigest, OccurredAt: occurredAt}, nil
}

func (a *SkillPolicyDecisionAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.repository == nil || job.Stage != core.SkillStageDecide {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "invalid_policy_job", errors.New("invalid policy decision job"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "policy_workflow_unavailable", err)
	}
	candidate, err := a.repository.GetSkillEvaluationRun(ctx, job.Scope.WorkspaceID, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "candidate_evaluation_unavailable", err)
	}
	baselineID := strings.TrimSuffix(candidate.ID, "-candidate") + "-baseline"
	if baselineID == candidate.ID+"-baseline" {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "evaluation_pair_identity_invalid", errors.New("candidate evaluation id is not canonical"))
	}
	baseline, err := a.repository.GetSkillEvaluationRun(ctx, job.Scope.WorkspaceID, baselineID)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "baseline_evaluation_unavailable", err)
	}
	signal, err := SkillLifecycleSignalForEvaluation(candidate, baseline, a.configuration.Signal)
	if err != nil {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "evaluation_pair_invalid", err)
	}
	expectedDigest := digestSkillLifecycleSignal(signal, nil)
	if workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || job.InputDigest != expectedDigest || workflow.InputDigest != expectedDigest || job.PolicyVersion != a.configuration.Signal.PolicyVersion || workflow.ConfigurationVersion != a.configuration.Signal.ConfigurationVersion || workflow.PolicyDigest != a.configuration.Signal.PolicyDigest {
		return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "policy_binding_mismatch", errors.New("policy workflow binding mismatch"))
	}
	decisionID := job.ID + "-decision"
	if existing, getErr := a.repository.GetSkillPolicyDecision(ctx, job.Scope.WorkspaceID, decisionID); getErr == nil {
		if !policyDecisionMatches(existing, candidate, baseline, a.configuration) {
			return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "policy_replay_mismatch", errors.New("stored policy decision does not match immutable inputs"))
		}
		return a.completePolicyDecision(ctx, existing)
	}
	decision, err := a.engine.Decide(ctx, SkillPolicyInput{DecisionID: decisionID, Workspace: job.Scope.WorkspaceID,
		SkillID: candidate.SkillID, RevisionID: candidate.RevisionID, PolicyID: a.configuration.PolicyID,
		PolicyVersion: a.configuration.Signal.PolicyVersion, CandidateRunID: candidate.ID, BaselineRunID: baseline.ID,
		CanarySamples: a.configuration.CanarySamples})
	if err != nil {
		if existing, getErr := a.repository.GetSkillPolicyDecision(ctx, job.Scope.WorkspaceID, decisionID); getErr == nil && policyDecisionMatches(existing, candidate, baseline, a.configuration) {
			return a.completePolicyDecision(ctx, existing)
		}
		return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "policy_decision_failed", err)
	}
	return a.completePolicyDecision(ctx, decision)
}

func (a *SkillPolicyDecisionAdapter) completePolicyDecision(ctx context.Context, decision core.SkillPolicyDecision) (SkillStageResult, error) {
	if a.downstream != nil {
		var signal SkillLifecycleSignal
		var err error
		switch decision.Decision {
		case core.SkillDecisionCanary, core.SkillDecisionApprovalRequired:
			signal, err = SkillLifecycleSignalForDecision(decision, a.configuration.Signal)
		case core.SkillDecisionPromote:
			signal, err = SkillLifecycleSignalForPromotion(decision, a.configuration.Signal)
		}
		if err != nil {
			return SkillStageResult{}, evaluationStageError(core.SkillFailurePermanentValidation, "policy_signal_invalid", err)
		}
		if signal.ID != "" {
			if _, routeErr := a.downstream.Route(ctx, signal); routeErr != nil {
				return SkillStageResult{}, evaluationStageError(core.SkillFailureDependencyUnavailable, "policy_signal_unavailable", routeErr)
			}
		}
	}
	return policyStageResult(decision), nil
}

func policyDecisionMatches(decision core.SkillPolicyDecision, candidate, baseline core.SkillEvaluationRun, configuration SkillPolicyStageConfiguration) bool {
	return decision.Workspace == candidate.Workspace && decision.SkillID == candidate.SkillID && decision.RevisionID == candidate.RevisionID && decision.PolicyID == configuration.PolicyID && decision.PolicyVersion == configuration.Signal.PolicyVersion && len(decision.EvaluationRunIDs) == 2 && decision.EvaluationRunIDs[0] == candidate.ID && decision.EvaluationRunIDs[1] == baseline.ID
}

func policyStageResult(decision core.SkillPolicyDecision) SkillStageResult {
	resultKind := core.SkillJobResultSucceeded
	if decision.Decision == core.SkillDecisionReject {
		resultKind = core.SkillJobResultRejected
	}
	return SkillStageResult{ResultKind: resultKind, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferencePolicyDecision, ID: decision.ID}}}
}
