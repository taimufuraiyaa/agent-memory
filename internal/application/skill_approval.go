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

type SkillApprovalInput struct {
	ID               string `json:"id"`
	Workspace        string `json:"workspace"`
	RevisionID       string `json:"revision_id"`
	PolicyDecisionID string `json:"policy_decision_id"`
	ApproverID       string `json:"approver_id"`
	Approved         bool   `json:"approved"`
	Reason           string `json:"reason"`
}

type SkillApprovalRevocationInput struct {
	Workspace  string `json:"workspace"`
	ApprovalID string `json:"approval_id"`
	ActorID    string `json:"actor_id"`
	Reason     string `json:"reason"`
}

type skillApprovalRepositoryContract interface {
	GetSkillPolicyDecision(context.Context, string, string) (core.SkillPolicyDecision, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillApproval(context.Context, string, string) (core.SkillApproval, error)
	CreateSkillApproval(context.Context, core.SkillApproval) error
	RevokeSkillApproval(context.Context, string, string, string, string, time.Time) (core.SkillApproval, error)
}

type SkillApprovalAuthorizer interface {
	AuthorizeSkillApproval(context.Context, string, string, string) error
	AuthorizeSkillApprovalRevocation(context.Context, string, string, string) error
}

type SkillApprovalService struct {
	repository skillApprovalRepositoryContract
	authorizer SkillApprovalAuthorizer
	now        func() time.Time
}

func NewSkillApprovalService(repository skillApprovalRepositoryContract, authorizer SkillApprovalAuthorizer, now func() time.Time) *SkillApprovalService {
	if now == nil {
		now = time.Now
	}
	return &SkillApprovalService{repository: repository, authorizer: authorizer, now: now}
}

func (s *SkillApprovalService) Approve(ctx context.Context, input SkillApprovalInput) (core.SkillApproval, error) {
	if s == nil || s.repository == nil || s.authorizer == nil {
		return core.SkillApproval{}, errors.New("skill approval dependencies are required")
	}
	if err := validateSkillApprovalInput(input); err != nil {
		return core.SkillApproval{}, err
	}
	if err := s.authorizer.AuthorizeSkillApproval(ctx, input.ApproverID, input.Workspace, input.RevisionID); err != nil {
		return core.SkillApproval{}, err
	}
	existing, err := s.repository.GetSkillApproval(ctx, input.Workspace, input.ID)
	if err == nil {
		if existing.RevisionID != input.RevisionID || existing.PolicyDecisionID != input.PolicyDecisionID || existing.ApproverID != input.ApproverID || existing.Approved != input.Approved || existing.Reason != input.Reason {
			return core.SkillApproval{}, errors.New("approval id is already bound to different inputs")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return core.SkillApproval{}, err
	}
	decision, err := s.repository.GetSkillPolicyDecision(ctx, input.Workspace, input.PolicyDecisionID)
	if err != nil {
		return core.SkillApproval{}, err
	}
	revision, err := s.repository.GetSkillRevision(ctx, input.Workspace, input.RevisionID)
	if err != nil {
		return core.SkillApproval{}, err
	}
	if decision.RevisionID != revision.ID || decision.SkillID != revision.SkillID || decision.Decision != core.SkillDecisionApprovalRequired || (decision.RiskTier != core.SkillRiskMedium && decision.RiskTier != core.SkillRiskHigh) || decision.RiskTier != revision.RiskTier {
		return core.SkillApproval{}, errors.New("policy decision is not eligible for accountable approval")
	}
	if revision.State != core.SkillRevisionTesting && revision.State != core.SkillRevisionCanary {
		return core.SkillApproval{}, errors.New("skill revision is no longer approval-eligible")
	}
	if revision.CreatedBy == input.ApproverID {
		return core.SkillApproval{}, errors.New("revision author cannot approve their own revision")
	}
	approval := core.SkillApproval{ID: input.ID, Workspace: input.Workspace, RevisionID: input.RevisionID, PolicyDecisionID: input.PolicyDecisionID, ApproverID: input.ApproverID, Approved: input.Approved, Reason: input.Reason, CreatedAt: s.now().UTC()}
	if err := approval.Validate(); err != nil {
		return core.SkillApproval{}, err
	}
	if err := s.repository.CreateSkillApproval(ctx, approval); err != nil {
		return core.SkillApproval{}, err
	}
	return approval, nil
}

func (s *SkillApprovalService) Revoke(ctx context.Context, input SkillApprovalRevocationInput) (core.SkillApproval, error) {
	if s == nil || s.repository == nil || s.authorizer == nil {
		return core.SkillApproval{}, errors.New("skill approval dependencies are required")
	}
	for field, value := range map[string]string{"workspace": input.Workspace, "approval_id": input.ApprovalID, "actor_id": input.ActorID, "reason": input.Reason} {
		if strings.TrimSpace(value) == "" || len(value) > core.MaxSkillReasonBytes {
			return core.SkillApproval{}, fmt.Errorf("skill approval revocation %s is required and bounded", field)
		}
	}
	if err := s.authorizer.AuthorizeSkillApprovalRevocation(ctx, input.ActorID, input.Workspace, input.ApprovalID); err != nil {
		return core.SkillApproval{}, err
	}
	existing, err := s.repository.GetSkillApproval(ctx, input.Workspace, input.ApprovalID)
	if err != nil {
		return core.SkillApproval{}, err
	}
	if !existing.RevokedAt.IsZero() {
		return existing, nil
	}
	return s.repository.RevokeSkillApproval(ctx, input.Workspace, input.ApprovalID, input.ActorID, input.Reason, s.now().UTC())
}

func validateSkillApprovalInput(input SkillApprovalInput) error {
	for field, value := range map[string]string{"id": input.ID, "workspace": input.Workspace, "revision_id": input.RevisionID, "policy_decision_id": input.PolicyDecisionID, "approver_id": input.ApproverID, "reason": input.Reason} {
		limit := 256
		if field == "reason" {
			limit = core.MaxSkillReasonBytes
		}
		if strings.TrimSpace(value) == "" || len(value) > limit {
			return fmt.Errorf("skill approval %s is required and bounded", field)
		}
	}
	return nil
}
