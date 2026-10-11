package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) PlaceSkillOrchestratorLegalHold(ctx context.Context, hold core.SkillOrchestratorLegalHold) error {
	if err := hold.Scope.Validate(); err != nil || strings.TrimSpace(hold.ID) == "" || !validSkillOrchestratorHoldKind(hold.TargetKind) || strings.TrimSpace(hold.TargetID) == "" || strings.TrimSpace(hold.Reason) == "" || hold.CreatedAt.IsZero() {
		return errors.New("skill orchestrator legal hold is invalid")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_legal_holds(tenant_id,workspace_id,environment,id,target_kind,target_id,reason,state,created_at,released_at) VALUES(?,?,?,?,?,?,?,'active',?,'')`, hold.Scope.TenantID, hold.Scope.WorkspaceID, hold.Scope.Environment, hold.ID, hold.TargetKind, hold.TargetID, hold.Reason, formatSkillOrchestratorTime(hold.CreatedAt))
	return err
}

func (s *Store) ReleaseSkillOrchestratorLegalHold(ctx context.Context, scope core.SkillOrchestratorScope, holdID string, at time.Time) error {
	if err := scope.Validate(); err != nil || strings.TrimSpace(holdID) == "" || at.IsZero() {
		return errors.New("skill orchestrator legal hold release is invalid")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE skill_orchestrator_legal_holds SET state='released',released_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND id=? AND state='active'`, formatSkillOrchestratorTime(at), scope.TenantID, scope.WorkspaceID, scope.Environment, holdID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteSkillOrchestratorRecord(ctx context.Context, scope core.SkillOrchestratorScope, kind, id string, at time.Time) (core.SkillOrchestratorDeletionResult, error) {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	result := core.SkillOrchestratorDeletionResult{Scope: scope, RecordKind: kind, RecordID: id}
	if err := scope.Validate(); err != nil || id == "" || at.IsZero() || !validSkillOrchestratorCustodyKind(kind) {
		return result, errors.New("skill orchestrator deletion scope is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var held, tombstoned int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_orchestrator_legal_holds WHERE tenant_id=? AND workspace_id=? AND environment=? AND state='active' AND ((target_kind=? AND target_id=?) OR (target_kind='workspace' AND target_id=?)))`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id, scope.WorkspaceID).Scan(&held); err != nil {
		return result, err
	}
	if held != 0 {
		return result, ErrSkillEvidenceHeld
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_orchestrator_tombstones WHERE tenant_id=? AND workspace_id=? AND environment=? AND record_kind=? AND record_id=?)`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id).Scan(&tombstoned); err != nil {
		return result, err
	}
	if tombstoned != 0 {
		result.Replayed = true
		return result, tx.Commit()
	}
	now := formatSkillOrchestratorTime(at)
	switch kind {
	case "workflow":
		jobs, execErr := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state='cancelled',lease_owner='',lease_expires_at='',timeout_at='',completed_at=?,updated_at=?,failure_class='cancellation',failure_code='evidence_deleted' WHERE tenant_id=? AND workspace_id=? AND environment=? AND workflow_id=? AND state IN ('queued','blocked','retry_wait')`, now, now, scope.TenantID, scope.WorkspaceID, scope.Environment, id)
		if execErr != nil {
			return result, execErr
		}
		result.JobsCancelled, _ = jobs.RowsAffected()
		if _, execErr = tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET cancel_requested_at=?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND workflow_id=? AND state='running'`, now, now, scope.TenantID, scope.WorkspaceID, scope.Environment, id); execErr != nil {
			return result, execErr
		}
		workflows, execErr := tx.ExecContext(ctx, `UPDATE skill_orchestrator_workflows SET state='cancelled',generation=generation+1,updated_at=?,terminal_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND id=? AND state IN ('open','paused')`, now, now, scope.TenantID, scope.WorkspaceID, scope.Environment, id)
		if execErr != nil {
			return result, execErr
		}
		result.WorkflowsClosed, _ = workflows.RowsAffected()
	case "job":
		jobs, execErr := tx.ExecContext(ctx, `UPDATE skill_orchestrator_jobs SET state=CASE WHEN state='running' THEN state ELSE 'cancelled' END,cancel_requested_at=CASE WHEN state='running' THEN ? ELSE cancel_requested_at END,completed_at=CASE WHEN state='running' THEN completed_at ELSE ? END,updated_at=?,failure_class='cancellation',failure_code='evidence_deleted' WHERE tenant_id=? AND workspace_id=? AND environment=? AND id=? AND state IN ('queued','blocked','running','retry_wait')`, now, now, now, scope.TenantID, scope.WorkspaceID, scope.Environment, id)
		if execErr != nil {
			return result, execErr
		}
		result.JobsCancelled, _ = jobs.RowsAffected()
	case "configuration":
		version, parseErr := strconv.ParseInt(id, 10, 64)
		if parseErr != nil || version < 1 {
			return result, errors.New("configuration deletion id must be a positive version")
		}
		deleted, execErr := tx.ExecContext(ctx, `DELETE FROM skill_orchestrator_configurations WHERE tenant_id=? AND workspace_id=? AND environment=? AND version=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, version)
		if execErr != nil {
			return result, execErr
		}
		result.RecordsDeleted, _ = deleted.RowsAffected()
	case "safety_signal":
		deleted, execErr := tx.ExecContext(ctx, `DELETE FROM skill_orchestrator_safety_signals WHERE tenant_id=? AND workspace_id=? AND environment=? AND id=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, id)
		if execErr != nil {
			return result, execErr
		}
		result.RecordsDeleted, _ = deleted.RowsAffected()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_tombstones(tenant_id,workspace_id,environment,record_kind,record_id,deleted_at) VALUES(?,?,?,?,?,?)`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id, now); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) PruneSkillOrchestratorAttempts(ctx context.Context, scope core.SkillOrchestratorScope, before time.Time, limit int) (int64, error) {
	if err := scope.Validate(); err != nil || before.IsZero() || limit < 1 || limit > 10_000 {
		return 0, errors.New("skill orchestrator attempt retention scope is invalid")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM skill_orchestrator_job_attempts WHERE id IN (SELECT attempt.id FROM skill_orchestrator_job_attempts attempt JOIN skill_orchestrator_jobs job ON job.id=attempt.job_id JOIN skill_orchestrator_workflows workflow ON workflow.id=job.workflow_id WHERE attempt.ended_at<>'' AND attempt.ended_at<? AND job.tenant_id=? AND job.workspace_id=? AND job.environment=? AND NOT EXISTS (SELECT 1 FROM skill_orchestrator_legal_holds hold WHERE hold.tenant_id=job.tenant_id AND hold.workspace_id=job.workspace_id AND hold.environment=job.environment AND hold.state='active' AND ((hold.target_kind='workspace' AND hold.target_id=job.workspace_id) OR (hold.target_kind='workflow' AND hold.target_id=workflow.id) OR (hold.target_kind='job' AND hold.target_id=job.id))) ORDER BY attempt.ended_at,attempt.id LIMIT ?)`, formatSkillOrchestratorTime(before), scope.TenantID, scope.WorkspaceID, scope.Environment, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func validSkillOrchestratorCustodyKind(kind string) bool {
	return kind == "workflow" || kind == "job" || kind == "configuration" || kind == "safety_signal"
}

func validSkillOrchestratorHoldKind(kind string) bool {
	return kind == "workspace" || validSkillOrchestratorCustodyKind(kind)
}

func (s *Store) IsSkillOrchestratorSignalTombstoned(ctx context.Context, scope core.SkillOrchestratorScope, kind, id string) (bool, error) {
	if err := scope.Validate(); err != nil || !validSkillOrchestratorCustodyKind(kind) || strings.TrimSpace(id) == "" {
		return false, errors.New("skill orchestrator tombstone scope is invalid")
	}
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM skill_orchestrator_tombstones WHERE tenant_id=? AND workspace_id=? AND environment=? AND record_kind=? AND record_id=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) RestoreSkillOrchestratorTombstones(ctx context.Context, scope core.SkillOrchestratorScope, archive map[string][]map[string]any) (int64, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var restored int64
	for _, item := range archive["orchestrator_tombstones"] {
		tenantID := archiveString(item["tenant_id"])
		workspaceID := archiveString(item["workspace_id"])
		environment := archiveString(item["environment"])
		kind := archiveString(item["record_kind"])
		id := archiveString(item["record_id"])
		deletedAt := archiveString(item["deleted_at"])
		if tenantID != scope.TenantID || workspaceID != scope.WorkspaceID || environment != scope.Environment || !validSkillOrchestratorCustodyKind(kind) || id == "" || deletedAt == "" {
			return 0, errors.New("skill orchestrator tombstone archive scope is invalid")
		}
		result, execErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO skill_orchestrator_tombstones(tenant_id,workspace_id,environment,record_kind,record_id,deleted_at) VALUES(?,?,?,?,?,?)`, tenantID, workspaceID, environment, kind, id, deletedAt)
		if execErr != nil {
			return 0, execErr
		}
		changed, _ := result.RowsAffected()
		restored += changed
	}
	return restored, tx.Commit()
}

func archiveString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
