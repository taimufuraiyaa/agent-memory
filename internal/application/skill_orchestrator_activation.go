package application

import (
	"context"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillAutomaticActivationConfiguration struct {
	Signal                   SkillSignalConfiguration
	Enabled                  bool
	Actor                    string
	ApprovalReference        string
	ReleaseEvidenceReference string
	SignatureReference       string
}

func (c SkillAutomaticActivationConfiguration) Validate() error {
	if err := c.Signal.Validate(); err != nil {
		return err
	}
	if !validSkillSignalIdentifier(c.Actor) {
		return errors.New("automatic activation actor is invalid")
	}
	if c.Enabled && (!validSkillSignalIdentifier(c.ApprovalReference) || !validSkillSignalIdentifier(c.ReleaseEvidenceReference) || !validSkillSignalIdentifier(c.SignatureReference)) {
		return errors.New("automatic activation requires approval, release evidence, and signature references")
	}
	return nil
}

func SkillLifecycleSignalForPromotion(decision core.SkillPolicyDecision, configuration SkillSignalConfiguration) (SkillLifecycleSignal, error) {
	signal, err := SkillLifecycleSignalForDecision(decision, configuration)
	if err != nil {
		return SkillLifecycleSignal{}, err
	}
	if decision.Decision != core.SkillDecisionPromote || decision.RiskTier != core.SkillRiskLow {
		return SkillLifecycleSignal{}, errors.New("only explicit low-risk promote decisions can signal activation")
	}
	signal.Kind = SkillSignalPromotion
	return signal, nil
}

type SkillAutomaticActivationRepository interface {
	GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error)
	GetSkillPolicyDecision(context.Context, string, string) (core.SkillPolicyDecision, error)
	GetSkillPromotionPolicy(context.Context, string, string, int64) (core.SkillPromotionPolicy, error)
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
}

type SkillAutomaticActivationAdapter struct {
	repository    SkillAutomaticActivationRepository
	activator     skillRevisionActivator
	configuration SkillAutomaticActivationConfiguration
}

func NewSkillAutomaticActivationAdapter(repository SkillAutomaticActivationRepository, activator skillRevisionActivator, configuration SkillAutomaticActivationConfiguration) (*SkillAutomaticActivationAdapter, error) {
	if repository == nil || activator == nil {
		return nil, errors.New("automatic activation adapter dependencies are required")
	}
	if err := configuration.Validate(); err != nil {
		return nil, err
	}
	return &SkillAutomaticActivationAdapter{repository: repository, activator: activator, configuration: configuration}, nil
}

func (a *SkillAutomaticActivationAdapter) Execute(ctx context.Context, job core.SkillJob) (SkillStageResult, error) {
	if a == nil || a.repository == nil || job.Stage != core.SkillStageActivate {
		return SkillStageResult{}, activationStageError(core.SkillFailurePermanentValidation, "invalid_activation_job", errors.New("invalid automatic activation job"))
	}
	if !a.configuration.Enabled {
		return SkillStageResult{}, activationStageError(core.SkillFailurePolicyBlock, "automatic_activation_disabled", errors.New("automatic activation is disabled"))
	}
	workflow, err := a.repository.GetSkillWorkflow(ctx, job.Scope, job.WorkflowID)
	if err != nil {
		return SkillStageResult{}, activationStageError(core.SkillFailureDependencyUnavailable, "activation_workflow_unavailable", err)
	}
	decision, err := a.repository.GetSkillPolicyDecision(ctx, job.Scope.WorkspaceID, workflow.OriginID)
	if err != nil {
		return SkillStageResult{}, activationStageError(core.SkillFailureDependencyUnavailable, "activation_decision_unavailable", err)
	}
	signal, err := SkillLifecycleSignalForPromotion(decision, a.configuration.Signal)
	if err != nil {
		return SkillStageResult{}, activationStageError(core.SkillFailurePolicyBlock, "activation_decision_ineligible", err)
	}
	expectedDigest := digestSkillLifecycleSignal(signal, nil)
	if workflow.OriginKind != core.SkillWorkflowOriginLifecycleSignal || job.InputDigest != expectedDigest || workflow.InputDigest != expectedDigest || job.PolicyVersion != a.configuration.Signal.PolicyVersion || workflow.ConfigurationVersion != a.configuration.Signal.ConfigurationVersion || workflow.PolicyDigest != a.configuration.Signal.PolicyDigest || decision.PolicyVersion != job.PolicyVersion {
		return SkillStageResult{}, activationStageError(core.SkillFailurePermanentValidation, "activation_binding_mismatch", errors.New("automatic activation binding mismatch"))
	}
	policy, err := a.repository.GetSkillPromotionPolicy(ctx, job.Scope.WorkspaceID, decision.PolicyID, decision.PolicyVersion)
	if err != nil {
		return SkillStageResult{}, activationStageError(core.SkillFailureDependencyUnavailable, "activation_policy_unavailable", err)
	}
	if policy.RiskTier != core.SkillRiskLow || !policy.AllowAutomaticActivation || decision.RiskTier != core.SkillRiskLow || decision.Decision != core.SkillDecisionPromote {
		return SkillStageResult{}, activationStageError(core.SkillFailurePolicyBlock, "automatic_activation_not_approved", errors.New("promotion policy does not approve low-risk automatic activation"))
	}
	activation, err := a.repository.GetSkillActivation(ctx, job.Scope.WorkspaceID, job.Scope.Environment, decision.SkillID)
	if err != nil {
		return SkillStageResult{}, activationStageError(core.SkillFailureDependencyUnavailable, "activation_state_unavailable", err)
	}
	if activation.ActiveRevisionID == decision.RevisionID {
		if activation.PolicyDecisionID != decision.ID {
			return SkillStageResult{}, activationStageError(core.SkillFailurePermanentValidation, "activation_replay_mismatch", errors.New("active revision is bound to another policy decision"))
		}
		return automaticActivationResult(activation), nil
	}
	activation, err = a.activator.Activate(ctx, SkillActivationRequest{OperationID: job.ID + "-operation", IdempotencyKey: job.ID,
		Workspace: job.Scope.WorkspaceID, Environment: job.Scope.Environment, SkillID: decision.SkillID,
		TargetRevisionID: decision.RevisionID, ExpectedGeneration: activation.Generation, PolicyDecisionID: decision.ID,
		Actor: a.configuration.Actor, Automatic: true})
	if err != nil {
		class, code := core.SkillFailureDependencyUnavailable, "automatic_activation_failed"
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "generation") || strings.Contains(message, "already active") {
			class, code = core.SkillFailureContention, "activation_generation_stale"
		}
		if strings.Contains(message, "policy") || strings.Contains(message, "approval") || strings.Contains(message, "low-risk") {
			class, code = core.SkillFailurePolicyBlock, "automatic_activation_rejected"
		}
		return SkillStageResult{}, activationStageError(class, code, err)
	}
	return automaticActivationResult(activation), nil
}

func automaticActivationResult(activation core.SkillActivation) SkillStageResult {
	return SkillStageResult{ResultKind: core.SkillJobResultSucceeded, References: []core.SkillOrchestratorReference{{Kind: core.SkillReferenceActivation, ID: activation.ID}}}
}

func activationStageError(class core.SkillJobFailureClass, code string, err error) error {
	return &SkillStageError{Failure: SkillStageFailure{Class: class, Code: code}, Err: err}
}
