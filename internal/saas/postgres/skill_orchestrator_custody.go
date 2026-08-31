package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrHostedSkillOrchestratorHeld = errors.New("hosted skill orchestrator record is under legal hold")

func (r *SkillOrchestratorRepository) PlaceSkillOrchestratorLegalHold(ctx context.Context, hold core.SkillOrchestratorLegalHold) error {
	scope := hold.Scope
	if err := scope.Validate(); err != nil || hold.ID == "" || !validHostedOrchestratorHoldKind(hold.TargetKind) || hold.TargetID == "" || hold.Reason == "" || hold.CreatedAt.IsZero() {
		return errors.New("hosted skill orchestrator legal hold is invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_legal_holds(tenant_id,workspace_id,environment,id,target_kind,target_id,reason,state,created_at) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7,'active',$8)`, scope.TenantID, scope.WorkspaceID, scope.Environment, hold.ID, hold.TargetKind, hold.TargetID, hold.Reason, hold.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) ReleaseSkillOrchestratorLegalHold(ctx context.Context, scope core.SkillOrchestratorScope, holdID string, at time.Time) error {
	if holdID == "" || at.IsZero() {
		return errors.New("hosted skill orchestrator hold release is invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_legal_holds SET state='released',released_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND id=$4::uuid AND state='active'`, scope.TenantID, scope.WorkspaceID, scope.Environment, holdID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillOrchestratorNotFound
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) IsSkillOrchestratorSignalTombstoned(ctx context.Context, scope core.SkillOrchestratorScope, kind, id string) (bool, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var found bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_skill_orchestrator_tombstones WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND record_kind=$4 AND record_id=$5)`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id).Scan(&found); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return found, nil
}

func (r *SkillOrchestratorRepository) DeleteSkillOrchestratorRecord(ctx context.Context, scope core.SkillOrchestratorScope, kind, id string, at time.Time) (core.SkillOrchestratorDeletionResult, error) {
	result := core.SkillOrchestratorDeletionResult{Scope: scope, RecordKind: kind, RecordID: id}
	if !validHostedOrchestratorCustodyKind(kind) || strings.TrimSpace(id) == "" || at.IsZero() {
		return result, errors.New("hosted skill orchestrator deletion is invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var held, tombstoned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_skill_orchestrator_legal_holds WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND state='active' AND ((target_kind=$4 AND target_id=$5) OR (target_kind='workspace' AND target_id=$2::text)))`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id).Scan(&held); err != nil {
		return result, err
	}
	if held {
		return result, ErrHostedSkillOrchestratorHeld
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_skill_orchestrator_tombstones WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND record_kind=$4 AND record_id=$5)`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id).Scan(&tombstoned); err != nil {
		return result, err
	}
	if tombstoned {
		result.Replayed = true
		return result, tx.Commit(ctx)
	}
	switch kind {
	case "workflow":
		tag, execErr := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET state='cancelled',lease_owner='',lease_expires_at=NULL,timeout_at=NULL,completed_at=$5,updated_at=$5,failure_class='cancellation',failure_code='evidence_deleted' WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND workflow_id=$4::uuid AND state IN ('queued','blocked','retry_wait')`, scope.TenantID, scope.WorkspaceID, scope.Environment, id, at)
		if execErr != nil {
			return result, execErr
		}
		result.JobsCancelled = tag.RowsAffected()
		if _, execErr = tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET cancel_requested_at=$5,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND workflow_id=$4::uuid AND state='running'`, scope.TenantID, scope.WorkspaceID, scope.Environment, id, at); execErr != nil {
			return result, execErr
		}
		tag, execErr = tx.Exec(ctx, `UPDATE saas_skill_orchestrator_workflows SET state='cancelled',generation=generation+1,updated_at=$5,terminal_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND id=$4::uuid AND state IN ('open','paused')`, scope.TenantID, scope.WorkspaceID, scope.Environment, id, at)
		if execErr != nil {
			return result, execErr
		}
		result.WorkflowsClosed = tag.RowsAffected()
	case "job":
		tag, execErr := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_jobs SET state=CASE WHEN state='running' THEN state ELSE 'cancelled' END,cancel_requested_at=CASE WHEN state='running' THEN $5 ELSE cancel_requested_at END,completed_at=CASE WHEN state='running' THEN completed_at ELSE $5 END,updated_at=$5,failure_class='cancellation',failure_code='evidence_deleted' WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND id=$4::uuid AND state IN ('queued','blocked','running','retry_wait')`, scope.TenantID, scope.WorkspaceID, scope.Environment, id, at)
		if execErr != nil {
			return result, execErr
		}
		result.JobsCancelled = tag.RowsAffected()
	case "configuration":
		version, parseErr := strconv.ParseInt(id, 10, 64)
		if parseErr != nil || version < 1 {
			return result, errors.New("configuration deletion id must be a positive version")
		}
		tag, execErr := tx.Exec(ctx, `DELETE FROM saas_skill_orchestrator_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND version=$4`, scope.TenantID, scope.WorkspaceID, scope.Environment, version)
		if execErr != nil {
			return result, execErr
		}
		result.RecordsDeleted = tag.RowsAffected()
	case "safety_signal":
		tag, execErr := tx.Exec(ctx, `DELETE FROM saas_skill_orchestrator_safety_signals WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND id=$4::uuid`, scope.TenantID, scope.WorkspaceID, scope.Environment, id)
		if execErr != nil {
			return result, execErr
		}
		result.RecordsDeleted = tag.RowsAffected()
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_tombstones(tenant_id,workspace_id,environment,record_kind,record_id,deleted_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6)`, scope.TenantID, scope.WorkspaceID, scope.Environment, kind, id, at); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) PruneSkillOrchestratorAttempts(ctx context.Context, scope core.SkillOrchestratorScope, before time.Time, limit int) (int64, error) {
	if before.IsZero() || limit < 1 || limit > 10_000 {
		return 0, errors.New("hosted attempt retention cutoff is required")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `WITH expired AS (SELECT attempt.tenant_id,attempt.workspace_id,attempt.id FROM saas_skill_orchestrator_job_attempts attempt JOIN saas_skill_orchestrator_jobs job ON attempt.tenant_id=job.tenant_id AND attempt.workspace_id=job.workspace_id AND attempt.job_id=job.id WHERE attempt.tenant_id=$1::uuid AND attempt.workspace_id=$2::uuid AND job.environment=$3 AND attempt.ended_at<$4 AND NOT EXISTS (SELECT 1 FROM saas_skill_orchestrator_legal_holds hold WHERE hold.tenant_id=job.tenant_id AND hold.workspace_id=job.workspace_id AND hold.environment=job.environment AND hold.state='active' AND ((hold.target_kind='workspace' AND hold.target_id=job.workspace_id::text) OR (hold.target_kind='workflow' AND hold.target_id=job.workflow_id::text) OR (hold.target_kind='job' AND hold.target_id=job.id::text))) ORDER BY attempt.ended_at,attempt.id LIMIT $5) DELETE FROM saas_skill_orchestrator_job_attempts attempt USING expired WHERE attempt.tenant_id=expired.tenant_id AND attempt.workspace_id=expired.workspace_id AND attempt.id=expired.id`, scope.TenantID, scope.WorkspaceID, scope.Environment, before, limit)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *SkillOrchestratorRepository) ExportSkillOrchestratorArchive(ctx context.Context, scope core.SkillOrchestratorScope) (map[string][]map[string]any, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tables := map[string]string{"orchestrator_workflows": "saas_skill_orchestrator_workflows", "orchestrator_jobs": "saas_skill_orchestrator_jobs", "orchestrator_attempts": "saas_skill_orchestrator_job_attempts", "orchestrator_signals": "saas_skill_orchestrator_safety_signals", "orchestrator_configs": "saas_skill_orchestrator_configurations", "orchestrator_holds": "saas_skill_orchestrator_legal_holds", "orchestrator_tombstones": "saas_skill_orchestrator_tombstones", "orchestrator_budget_accounts": "saas_skill_orchestrator_budget_accounts", "orchestrator_budget_reservations": "saas_skill_orchestrator_budget_reservations", "orchestrator_migration_controls": "saas_skill_orchestrator_reconciliation_partitions"}
	archive := make(map[string][]map[string]any, len(tables))
	for name, table := range tables {
		var encoded []byte
		var records []map[string]any
		query := fmt.Sprintf(`SELECT COALESCE(jsonb_agg(to_jsonb(record)),'[]'::jsonb) FROM (SELECT * FROM %s WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid%s) record`, table, hostedArchiveEnvironmentClause(table))
		if err := tx.QueryRow(ctx, query, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &records); err != nil {
			return nil, err
		}
		archive[name] = records
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return archive, nil
}

func (r *SkillOrchestratorRepository) RestoreSkillOrchestratorTombstones(ctx context.Context, scope core.SkillOrchestratorScope, archive map[string][]map[string]any) (int64, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var restored int64
	for _, item := range archive["orchestrator_tombstones"] {
		tenantID := hostedArchiveString(item["tenant_id"])
		workspaceID := hostedArchiveString(item["workspace_id"])
		environment := hostedArchiveString(item["environment"])
		kind := hostedArchiveString(item["record_kind"])
		id := hostedArchiveString(item["record_id"])
		deletedAt := hostedArchiveString(item["deleted_at"])
		if tenantID != scope.TenantID || workspaceID != scope.WorkspaceID || environment != scope.Environment || !validHostedOrchestratorCustodyKind(kind) || id == "" || deletedAt == "" {
			return 0, errors.New("hosted skill orchestrator tombstone archive scope is invalid")
		}
		tag, execErr := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_tombstones(tenant_id,workspace_id,environment,record_kind,record_id,deleted_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6::timestamptz) ON CONFLICT DO NOTHING`, tenantID, workspaceID, environment, kind, id, deletedAt)
		if execErr != nil {
			return 0, execErr
		}
		restored += tag.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return restored, nil
}

func hostedArchiveEnvironmentClause(table string) string {
	if table == "saas_skill_orchestrator_job_attempts" {
		return " AND job_id IN (SELECT id FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3)"
	}
	return " AND environment=$3"
}
func validHostedOrchestratorCustodyKind(kind string) bool {
	return kind == "workflow" || kind == "job" || kind == "configuration" || kind == "safety_signal"
}

func validHostedOrchestratorHoldKind(kind string) bool {
	return kind == "workspace" || validHostedOrchestratorCustodyKind(kind)
}

func hostedArchiveString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
