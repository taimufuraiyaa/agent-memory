package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillCustodyRepository interface {
	DeleteSkillEvidence(context.Context, string, string, string, time.Time) (core.SkillEvidenceDeletionResult, error)
	PruneSkillTelemetry(context.Context, string, time.Time) (int64, error)
	PlaceSkillLegalHold(context.Context, core.SkillLegalHold) error
	ReleaseSkillLegalHold(context.Context, string, string, time.Time) error
	DeleteSkillOrchestratorRecord(context.Context, core.SkillOrchestratorScope, string, string, time.Time) (core.SkillOrchestratorDeletionResult, error)
	PruneSkillOrchestratorAttempts(context.Context, core.SkillOrchestratorScope, time.Time, int) (int64, error)
	PlaceSkillOrchestratorLegalHold(context.Context, core.SkillOrchestratorLegalHold) error
	ReleaseSkillOrchestratorLegalHold(context.Context, core.SkillOrchestratorScope, string, time.Time) error
	RestoreSkillOrchestratorTombstones(context.Context, core.SkillOrchestratorScope, map[string][]map[string]any) (int64, error)
}

func (s *SkillCustodyService) DeleteOrchestration(ctx context.Context, actor string, scope core.SkillOrchestratorScope, kind, id string) (core.SkillOrchestratorDeletionResult, error) {
	if err := s.authorize(ctx, actor, scope.WorkspaceID, "delete_orchestration", kind+":"+id); err != nil {
		return core.SkillOrchestratorDeletionResult{}, err
	}
	return s.repository.DeleteSkillOrchestratorRecord(ctx, scope, kind, id, s.now().UTC())
}

func (s *SkillCustodyService) PruneOrchestrationAttempts(ctx context.Context, actor string, scope core.SkillOrchestratorScope, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("skill orchestrator attempt cutoff is required")
	}
	if err := s.authorize(ctx, actor, scope.WorkspaceID, "prune_orchestration_attempts", before.UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return s.repository.PruneSkillOrchestratorAttempts(ctx, scope, before.UTC(), 1_000)
}

func (s *SkillCustodyService) PlaceOrchestrationHold(ctx context.Context, actor string, hold core.SkillOrchestratorLegalHold) error {
	if err := s.authorize(ctx, actor, hold.Scope.WorkspaceID, "place_orchestration_hold", hold.TargetKind+":"+hold.TargetID); err != nil {
		return err
	}
	if hold.CreatedAt.IsZero() {
		hold.CreatedAt = s.now().UTC()
	}
	return s.repository.PlaceSkillOrchestratorLegalHold(ctx, hold)
}

func (s *SkillCustodyService) ReleaseOrchestrationHold(ctx context.Context, actor string, scope core.SkillOrchestratorScope, holdID string) error {
	if err := s.authorize(ctx, actor, scope.WorkspaceID, "release_orchestration_hold", holdID); err != nil {
		return err
	}
	return s.repository.ReleaseSkillOrchestratorLegalHold(ctx, scope, holdID, s.now().UTC())
}

func (s *SkillCustodyService) RestoreOrchestrationTombstones(ctx context.Context, actor string, scope core.SkillOrchestratorScope, archive map[string][]map[string]any) (int64, error) {
	if err := s.authorize(ctx, actor, scope.WorkspaceID, "restore_orchestration_tombstones", scope.Environment); err != nil {
		return 0, err
	}
	return s.repository.RestoreSkillOrchestratorTombstones(ctx, scope, archive)
}

type SkillCustodyAuthorizer interface {
	AuthorizeSkillCustody(context.Context, string, string, string, string) error
}

type SkillCustodyService struct {
	repository SkillCustodyRepository
	authorizer SkillCustodyAuthorizer
	now        func() time.Time
}

func NewSkillCustodyService(repository SkillCustodyRepository, authorizer SkillCustodyAuthorizer, now func() time.Time) *SkillCustodyService {
	if now == nil {
		now = time.Now
	}
	return &SkillCustodyService{repository: repository, authorizer: authorizer, now: now}
}

func (s *SkillCustodyService) DeleteEvidence(ctx context.Context, actor, workspace, kind, id string) (core.SkillEvidenceDeletionResult, error) {
	if err := s.authorize(ctx, actor, workspace, "delete_evidence", kind+":"+id); err != nil {
		return core.SkillEvidenceDeletionResult{}, err
	}
	return s.repository.DeleteSkillEvidence(ctx, workspace, kind, id, s.now().UTC())
}

func (s *SkillCustodyService) PruneTelemetry(ctx context.Context, actor, workspace string, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("skill telemetry cutoff is required")
	}
	if err := s.authorize(ctx, actor, workspace, "prune_telemetry", before.UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return s.repository.PruneSkillTelemetry(ctx, workspace, before.UTC())
}

func (s *SkillCustodyService) PlaceHold(ctx context.Context, actor string, hold core.SkillLegalHold) error {
	if err := s.authorize(ctx, actor, hold.Workspace, "place_hold", hold.TargetKind+":"+hold.TargetID); err != nil {
		return err
	}
	if hold.CreatedAt.IsZero() {
		hold.CreatedAt = s.now().UTC()
	}
	return s.repository.PlaceSkillLegalHold(ctx, hold)
}

func (s *SkillCustodyService) ReleaseHold(ctx context.Context, actor, workspace, holdID string) error {
	if err := s.authorize(ctx, actor, workspace, "release_hold", holdID); err != nil {
		return err
	}
	return s.repository.ReleaseSkillLegalHold(ctx, workspace, holdID, s.now().UTC())
}

func (s *SkillCustodyService) authorize(ctx context.Context, actor, workspace, action, target string) error {
	if s == nil || s.repository == nil || s.authorizer == nil || strings.TrimSpace(actor) == "" || strings.TrimSpace(workspace) == "" {
		return errors.New("authorized skill custody dependencies and scope are required")
	}
	return s.authorizer.AuthorizeSkillCustody(ctx, actor, workspace, action, target)
}
