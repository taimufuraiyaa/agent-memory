package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillActivationRequest struct {
	OperationID        string `json:"operation_id"`
	IdempotencyKey     string `json:"idempotency_key"`
	Workspace          string `json:"workspace"`
	Environment        string `json:"environment"`
	SkillID            string `json:"skill_id"`
	TargetRevisionID   string `json:"target_revision_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	PolicyDecisionID   string `json:"policy_decision_id"`
	Actor              string `json:"actor"`
	Rollback           bool   `json:"rollback"`
	Automatic          bool   `json:"automatic"`
	ReasonCode         string `json:"reason_code,omitempty"`
}

type skillActivationRepository interface {
	GetLogicalSkill(context.Context, string, string) (core.LogicalSkill, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
	CreateSkillActivationOperation(context.Context, core.SkillActivationOperation) (bool, error)
	GetSkillActivationOperationByKey(context.Context, string, string, string, string) (core.SkillActivationOperation, error)
	TransitionSkillActivationOperation(context.Context, string, string, core.SkillActivationOperationState, core.SkillActivationOperationState, string, time.Time) (core.SkillActivationOperation, error)
	CompleteSkillActivation(context.Context, string, string, string, bool, bool, string, time.Time) (core.SkillActivation, error)
	GetSkillPolicyDecision(context.Context, string, string) (core.SkillPolicyDecision, error)
	HasEffectiveSkillApproval(context.Context, string, string, string) (bool, error)
}

type skillMaterializer interface {
	Materialize(context.Context, core.SkillMaterializationRequest) (core.SkillMaterializationResult, error)
}

type SkillActivationService struct {
	repository   skillActivationRepository
	materializer skillMaterializer
	now          func() time.Time
}

func NewSkillActivationService(repository skillActivationRepository, materializer skillMaterializer, now func() time.Time) *SkillActivationService {
	if now == nil {
		now = time.Now
	}
	return &SkillActivationService{repository: repository, materializer: materializer, now: now}
}

func (s *SkillActivationService) Activate(ctx context.Context, request SkillActivationRequest) (core.SkillActivation, error) {
	if s == nil || s.repository == nil || s.materializer == nil {
		return core.SkillActivation{}, errors.New("skill activation service dependencies are required")
	}
	if err := validateSkillActivationRequest(request); err != nil {
		return core.SkillActivation{}, err
	}
	skill, err := s.repository.GetLogicalSkill(ctx, request.Workspace, request.SkillID)
	if err != nil {
		return core.SkillActivation{}, err
	}
	target, err := s.repository.GetSkillRevision(ctx, request.Workspace, request.TargetRevisionID)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if target.SkillID != skill.ID || target.Workspace != skill.Workspace {
		return core.SkillActivation{}, errors.New("target revision does not belong to logical skill")
	}
	// Rollback authorization is enforced at the public boundary and the target is
	// restricted to the recorded last-known-good revision below. A prior promotion
	// decision cannot bind that older target revision.
	if !request.Rollback {
		decision, decisionErr := s.repository.GetSkillPolicyDecision(ctx, request.Workspace, request.PolicyDecisionID)
		if decisionErr != nil {
			return core.SkillActivation{}, decisionErr
		}
		if decision.RevisionID != target.ID || decision.SkillID != skill.ID || decision.RiskTier != target.RiskTier {
			return core.SkillActivation{}, errors.New("activation policy decision does not bind target revision")
		}
		switch decision.Decision {
		case core.SkillDecisionPromote:
			if target.RiskTier != core.SkillRiskLow {
				return core.SkillActivation{}, errors.New("medium or high-risk revision cannot activate without approval")
			}
		case core.SkillDecisionApprovalRequired:
			approved, approvalErr := s.repository.HasEffectiveSkillApproval(ctx, request.Workspace, target.ID, decision.ID)
			if approvalErr != nil {
				return core.SkillActivation{}, approvalErr
			}
			if !approved {
				return core.SkillActivation{}, errors.New("effective accountable approval is required")
			}
		default:
			return core.SkillActivation{}, errors.New("policy decision is not activation-eligible")
		}
	}
	operation, err := s.loadOrReserveActivation(ctx, request)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if operation.ID != request.OperationID || operation.ToRevisionID != request.TargetRevisionID || operation.ExpectedGeneration != request.ExpectedGeneration {
		return core.SkillActivation{}, errors.New("activation idempotency key is bound to different inputs")
	}
	if operation.State == core.SkillActivationOperationCompleted {
		return s.repository.GetSkillActivation(ctx, request.Workspace, request.Environment, request.SkillID)
	}
	now := s.now().UTC()
	switch operation.State {
	case core.SkillActivationOperationReserved:
		operation, err = s.repository.TransitionSkillActivationOperation(ctx, request.Workspace, operation.ID, core.SkillActivationOperationReserved, core.SkillActivationOperationMaterializing, "", now)
	case core.SkillActivationOperationFailed:
		operation, err = s.repository.TransitionSkillActivationOperation(ctx, request.Workspace, operation.ID, core.SkillActivationOperationFailed, core.SkillActivationOperationMaterializing, "", now)
	case core.SkillActivationOperationMaterializing:
	default:
		err = errors.New("activation operation state is not recoverable")
	}
	if err != nil {
		return core.SkillActivation{}, err
	}
	if _, err := s.materializer.Materialize(ctx, core.SkillMaterializationRequest{OperationID: operation.ID, Skill: skill, Revision: target}); err != nil {
		_, transitionErr := s.repository.TransitionSkillActivationOperation(ctx, request.Workspace, operation.ID, core.SkillActivationOperationMaterializing, core.SkillActivationOperationFailed, boundedActivationError(err), s.now().UTC())
		return core.SkillActivation{}, errors.Join(err, transitionErr)
	}
	activation, err := s.repository.CompleteSkillActivation(ctx, operation.ID, request.PolicyDecisionID, request.Actor, request.Rollback, request.Automatic, request.ReasonCode, s.now().UTC())
	if err == nil {
		return activation, nil
	}
	restoreErr := s.restorePriorRevision(ctx, skill, operation)
	_, transitionErr := s.repository.TransitionSkillActivationOperation(ctx, request.Workspace, operation.ID, core.SkillActivationOperationMaterializing, core.SkillActivationOperationFailed, boundedActivationError(err), s.now().UTC())
	return core.SkillActivation{}, errors.Join(err, restoreErr, transitionErr)
}

func (s *SkillActivationService) loadOrReserveActivation(ctx context.Context, request SkillActivationRequest) (core.SkillActivationOperation, error) {
	existing, err := s.repository.GetSkillActivationOperationByKey(ctx, request.Workspace, request.Environment, request.SkillID, request.IdempotencyKey)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return core.SkillActivationOperation{}, err
	}
	activation, err := s.repository.GetSkillActivation(ctx, request.Workspace, request.Environment, request.SkillID)
	if err != nil {
		return core.SkillActivationOperation{}, err
	}
	if activation.Generation != request.ExpectedGeneration {
		return core.SkillActivationOperation{}, fmt.Errorf("activation generation is stale: got %d, want %d", request.ExpectedGeneration, activation.Generation)
	}
	if activation.ActiveRevisionID == request.TargetRevisionID {
		return core.SkillActivationOperation{}, errors.New("target revision is already active")
	}
	if request.Rollback && activation.LastKnownGoodRevisionID != request.TargetRevisionID {
		return core.SkillActivationOperation{}, errors.New("rollback target is not the last-known-good revision")
	}
	now := s.now().UTC()
	operation := core.SkillActivationOperation{
		ID: request.OperationID, Workspace: request.Workspace, Environment: request.Environment, SkillID: request.SkillID,
		FromRevisionID: activation.ActiveRevisionID, ToRevisionID: request.TargetRevisionID, ExpectedGeneration: request.ExpectedGeneration,
		State: core.SkillActivationOperationReserved, IdempotencyKey: request.IdempotencyKey, CreatedAt: now, UpdatedAt: now,
	}
	duplicate, err := s.repository.CreateSkillActivationOperation(ctx, operation)
	if err != nil {
		return core.SkillActivationOperation{}, err
	}
	if duplicate {
		return s.repository.GetSkillActivationOperationByKey(ctx, request.Workspace, request.Environment, request.SkillID, request.IdempotencyKey)
	}
	return operation, nil
}

func (s *SkillActivationService) restorePriorRevision(ctx context.Context, skill core.LogicalSkill, operation core.SkillActivationOperation) error {
	prior, err := s.repository.GetSkillRevision(ctx, operation.Workspace, operation.FromRevisionID)
	if err != nil {
		return fmt.Errorf("load prior revision for restoration: %w", err)
	}
	restoreID := operation.ID + "-restore"
	if len(restoreID) > 128 {
		restoreID = restoreID[:128]
	}
	_, err = s.materializer.Materialize(ctx, core.SkillMaterializationRequest{OperationID: restoreID, Skill: skill, Revision: prior})
	if err != nil {
		return fmt.Errorf("restore prior materialized revision: %w", err)
	}
	return nil
}

func validateSkillActivationRequest(request SkillActivationRequest) error {
	for field, value := range map[string]string{
		"operation_id": request.OperationID, "idempotency_key": request.IdempotencyKey, "workspace": request.Workspace,
		"environment": request.Environment, "skill_id": request.SkillID, "target_revision_id": request.TargetRevisionID,
		"policy_decision_id": request.PolicyDecisionID, "actor": request.Actor,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 128 {
			return fmt.Errorf("skill activation %s is required and bounded", field)
		}
	}
	if request.ExpectedGeneration < 1 {
		return errors.New("skill activation expected_generation must be positive")
	}
	if request.Rollback && strings.TrimSpace(request.ReasonCode) == "" {
		return errors.New("skill rollback reason_code is required")
	}
	if !request.Rollback && (request.Automatic || request.ReasonCode != "") {
		return errors.New("automatic and reason_code are rollback-only fields")
	}
	return nil
}

func boundedActivationError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) > core.MaxSkillReasonBytes {
		message = message[:core.MaxSkillReasonBytes]
	}
	return message
}
