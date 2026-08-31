package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillCustodyServiceRequiresAuthorizationAndPreservesScope(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	repository := &skillCustodyRepositoryFixture{}
	authorizer := &skillCustodyAuthorizerFixture{}
	service := NewSkillCustodyService(repository, authorizer, func() time.Time { return now })
	result, err := service.DeleteEvidence(context.Background(), "privacy-operator", "ws-a", "memory", "memory-1")
	if err != nil || result.Workspace != "ws-a" || repository.workspace != "ws-a" || authorizer.action != "delete_evidence" {
		t.Fatalf("authorized deletion = %+v, %v", result, err)
	}
	authorizer.err = errors.New("denied")
	if _, err := service.PruneTelemetry(context.Background(), "other", "ws-b", now); err == nil || repository.workspace == "ws-b" {
		t.Fatal("unauthorized retention changed repository scope")
	}
}

type skillCustodyRepositoryFixture struct{ workspace string }

func (r *skillCustodyRepositoryFixture) DeleteSkillEvidence(_ context.Context, workspace, kind, id string, _ time.Time) (core.SkillEvidenceDeletionResult, error) {
	r.workspace = workspace
	return core.SkillEvidenceDeletionResult{Workspace: workspace, EvidenceKind: kind, EvidenceID: id}, nil
}
func (r *skillCustodyRepositoryFixture) PruneSkillTelemetry(_ context.Context, workspace string, _ time.Time) (int64, error) {
	r.workspace = workspace
	return 1, nil
}
func (r *skillCustodyRepositoryFixture) PlaceSkillLegalHold(context.Context, core.SkillLegalHold) error {
	return nil
}
func (r *skillCustodyRepositoryFixture) ReleaseSkillLegalHold(context.Context, string, string, time.Time) error {
	return nil
}
func (r *skillCustodyRepositoryFixture) DeleteSkillOrchestratorRecord(_ context.Context, scope core.SkillOrchestratorScope, kind, id string, _ time.Time) (core.SkillOrchestratorDeletionResult, error) {
	return core.SkillOrchestratorDeletionResult{Scope: scope, RecordKind: kind, RecordID: id}, nil
}
func (r *skillCustodyRepositoryFixture) PruneSkillOrchestratorAttempts(context.Context, core.SkillOrchestratorScope, time.Time, int) (int64, error) {
	return 1, nil
}
func (r *skillCustodyRepositoryFixture) PlaceSkillOrchestratorLegalHold(context.Context, core.SkillOrchestratorLegalHold) error {
	return nil
}
func (r *skillCustodyRepositoryFixture) ReleaseSkillOrchestratorLegalHold(context.Context, core.SkillOrchestratorScope, string, time.Time) error {
	return nil
}
func (r *skillCustodyRepositoryFixture) RestoreSkillOrchestratorTombstones(context.Context, core.SkillOrchestratorScope, map[string][]map[string]any) (int64, error) {
	return 1, nil
}

type skillCustodyAuthorizerFixture struct {
	action string
	err    error
}

func (a *skillCustodyAuthorizerFixture) AuthorizeSkillCustody(_ context.Context, _, _, action, _ string) error {
	a.action = action
	return a.err
}
