package application

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillApprovalServiceApprovesAndReplays(t *testing.T) {
	fixture := newSkillApprovalFixture()
	input := fixture.input()
	approval, err := fixture.service.Approve(context.Background(), input)
	if err != nil || !approval.Approved {
		t.Fatalf("approval = %+v, %v", approval, err)
	}
	replayed, err := fixture.service.Approve(context.Background(), input)
	if err != nil || replayed.ID != approval.ID || len(fixture.repository.events) != 1 {
		t.Fatalf("replay = %+v, events %v, err %v", replayed, fixture.repository.events, err)
	}
}

func TestSkillApprovalServiceEnforcesAuthorizationAndSeparationOfDuty(t *testing.T) {
	fixture := newSkillApprovalFixture()
	fixture.authorizer.err = errors.New("denied")
	if _, err := fixture.service.Approve(context.Background(), fixture.input()); err == nil {
		t.Fatal("unauthorized approval succeeded")
	}
	fixture.authorizer.err = nil
	input := fixture.input()
	input.ApproverID = fixture.repository.revision.CreatedBy
	if _, err := fixture.service.Approve(context.Background(), input); err == nil {
		t.Fatal("revision author approved their own revision")
	}
}

func TestSkillApprovalServiceRejectsStaleAndCrossWorkspaceRevision(t *testing.T) {
	fixture := newSkillApprovalFixture()
	fixture.repository.revision.State = core.SkillRevisionRejected
	if _, err := fixture.service.Approve(context.Background(), fixture.input()); err == nil {
		t.Fatal("rejected revision was approved")
	}
	fixture = newSkillApprovalFixture()
	input := fixture.input()
	input.Workspace = "other"
	if _, err := fixture.service.Approve(context.Background(), input); err == nil {
		t.Fatal("cross-workspace approval succeeded")
	}
}

func TestSkillApprovalServiceRevokesIdempotently(t *testing.T) {
	fixture := newSkillApprovalFixture()
	approval, err := fixture.service.Approve(context.Background(), fixture.input())
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := fixture.service.Revoke(context.Background(), SkillApprovalRevocationInput{Workspace: "ws", ApprovalID: approval.ID, ActorID: "security-reviewer", Reason: "evidence invalidated"})
	if err != nil || revoked.RevokedAt.IsZero() {
		t.Fatalf("revoked = %+v, %v", revoked, err)
	}
	replayed, err := fixture.service.Revoke(context.Background(), SkillApprovalRevocationInput{Workspace: "ws", ApprovalID: approval.ID, ActorID: "security-reviewer", Reason: "evidence invalidated"})
	if err != nil || replayed.RevokedAt != revoked.RevokedAt || len(fixture.repository.events) != 2 {
		t.Fatalf("revocation replay = %+v, events %v, err %v", replayed, fixture.repository.events, err)
	}
}

type skillApprovalRepository struct {
	decision core.SkillPolicyDecision
	revision core.SkillRevision
	approval core.SkillApproval
	events   []string
}

func (r *skillApprovalRepository) GetSkillPolicyDecision(_ context.Context, workspace, decisionID string) (core.SkillPolicyDecision, error) {
	if workspace != r.decision.Workspace || decisionID != r.decision.ID {
		return core.SkillPolicyDecision{}, errors.New("decision not found")
	}
	return r.decision, nil
}

func (r *skillApprovalRepository) GetSkillRevision(_ context.Context, workspace, revisionID string) (core.SkillRevision, error) {
	if workspace != r.revision.Workspace || revisionID != r.revision.ID {
		return core.SkillRevision{}, errors.New("revision not found")
	}
	return r.revision, nil
}

func (r *skillApprovalRepository) GetSkillApproval(_ context.Context, workspace, approvalID string) (core.SkillApproval, error) {
	if r.approval.ID == "" || workspace != r.approval.Workspace || approvalID != r.approval.ID {
		return core.SkillApproval{}, sql.ErrNoRows
	}
	return r.approval, nil
}

func (r *skillApprovalRepository) CreateSkillApproval(_ context.Context, approval core.SkillApproval) error {
	r.approval = approval
	r.events = append(r.events, "approved")
	return nil
}

func (r *skillApprovalRepository) RevokeSkillApproval(_ context.Context, workspace, approvalID, actorID, reason string, at time.Time) (core.SkillApproval, error) {
	if workspace != r.approval.Workspace || approvalID != r.approval.ID {
		return core.SkillApproval{}, errors.New("approval not found")
	}
	if r.approval.RevokedAt.IsZero() {
		r.approval.RevokedAt = at
		r.events = append(r.events, "revoked:"+actorID+":"+reason)
	}
	return r.approval, nil
}

type skillApprovalAuthorizer struct{ err error }

func (a *skillApprovalAuthorizer) AuthorizeSkillApproval(context.Context, string, string, string) error {
	return a.err
}
func (a *skillApprovalAuthorizer) AuthorizeSkillApprovalRevocation(context.Context, string, string, string) error {
	return a.err
}

type skillApprovalFixture struct {
	repository *skillApprovalRepository
	authorizer *skillApprovalAuthorizer
	service    *SkillApprovalService
	now        time.Time
}

func newSkillApprovalFixture() skillApprovalFixture {
	now := time.Date(2026, 8, 29, 21, 0, 0, 0, time.UTC)
	revision := resolverRevision("revision-2", 2, core.SkillRevisionTesting, core.LogicalSkill{ID: "skill-1", Workspace: "ws"}, now)
	revision.RiskTier = core.SkillRiskMedium
	decision := core.SkillPolicyDecision{ID: "decision-1", Workspace: "ws", SkillID: "skill-1", RevisionID: revision.ID, PolicyID: "policy-1", PolicyVersion: 1, EvaluationRunIDs: []string{"run-1"}, RiskTier: core.SkillRiskMedium, Decision: core.SkillDecisionApprovalRequired, ReasonCodes: []string{"accountable_approval_required"}, DecidedAt: now}
	repository := &skillApprovalRepository{decision: decision, revision: revision}
	authorizer := &skillApprovalAuthorizer{}
	service := NewSkillApprovalService(repository, authorizer, func() time.Time { return now })
	return skillApprovalFixture{repository: repository, authorizer: authorizer, service: service, now: now}
}

func (f skillApprovalFixture) input() SkillApprovalInput {
	return SkillApprovalInput{ID: "approval-1", Workspace: "ws", RevisionID: f.repository.revision.ID, PolicyDecisionID: f.repository.decision.ID, ApproverID: "independent-reviewer", Approved: true, Reason: "verified evidence"}
}
