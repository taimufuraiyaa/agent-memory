package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) SetSkillOrchestratorMigrationRestorePaused(ctx context.Context, scope core.SkillOrchestratorScope, paused bool, at time.Time) error {
	if err := scope.Validate(); err != nil || at.IsZero() {
		return errors.New("skill orchestrator migration restore pause is invalid")
	}
	value := 0
	if paused {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO skill_orchestrator_migration_controls(tenant_id,workspace_id,environment,restore_paused,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(tenant_id,workspace_id,environment) DO UPDATE SET restore_paused=excluded.restore_paused,updated_at=excluded.updated_at`, scope.TenantID, scope.WorkspaceID, scope.Environment, value, formatSkillOrchestratorTime(at))
	return err
}

func (s *Store) InspectSkillOrchestratorMigration(ctx context.Context, scope core.SkillOrchestratorScope, limit int) (core.SkillMigrationInventory, error) {
	inventory := core.SkillMigrationInventory{Scope: scope, ConfigurationMode: core.SkillOrchestratorDisabled, Items: []core.SkillMigrationInventoryItem{}, UnsupportedContracts: []string{}}
	if err := scope.Validate(); err != nil || limit < 1 || limit > 10_000 {
		return inventory, errors.New("skill orchestrator migration inventory scope is invalid")
	}
	if err := s.db.QueryRowContext(ctx, `SELECT CAST(MAX(version) AS TEXT) FROM schema_migrations`).Scan(&inventory.SchemaVersion); err != nil {
		return inventory, err
	}
	var paused int
	err := s.db.QueryRowContext(ctx, `SELECT restore_paused FROM skill_orchestrator_migration_controls WHERE tenant_id=? AND workspace_id=? AND environment=?`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&paused)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return inventory, err
	}
	inventory.RestorePaused = paused != 0
	var mode string
	err = s.db.QueryRowContext(ctx, `SELECT mode FROM skill_orchestrator_configurations WHERE tenant_id=? AND workspace_id=? AND environment=? ORDER BY version DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&mode)
	if err == nil {
		inventory.ConfigurationMode = core.SkillOrchestratorMode(mode)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return inventory, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT kind,id,skill_id,state,evidence_digest FROM (
			SELECT 'candidate' AS kind,id,'' AS skill_id,state,deduplication_hash AS evidence_digest FROM skill_candidates WHERE workspace=? AND state IN ('proposed','accepted')
			UNION ALL SELECT 'testing_revision',id,skill_id,state,bundle_digest FROM skill_revisions WHERE workspace=? AND state='testing'
			UNION ALL SELECT 'canary',canary_revision_id,skill_id,'canary',canary_digest FROM skill_activations WHERE workspace=? AND environment=? AND canary_revision_id<>''
			UNION ALL SELECT 'activation_operation',id,skill_id,state,idempotency_key FROM skill_activation_operations WHERE workspace=? AND environment=? AND state IN ('reserved','materializing')
		) ORDER BY kind,id LIMIT ?`, scope.WorkspaceID, scope.WorkspaceID, scope.WorkspaceID, scope.Environment, scope.WorkspaceID, scope.Environment, limit+1)
	if err != nil {
		return inventory, err
	}
	discovered := make([]core.SkillMigrationInventoryItem, 0, limit)
	for rows.Next() {
		var item core.SkillMigrationInventoryItem
		if err := rows.Scan(&item.Kind, &item.ID, &item.SkillID, &item.State, &item.EvidenceDigest); err != nil {
			return inventory, err
		}
		if len(inventory.Items) == limit {
			inventory.Truncated = true
			continue
		}
		discovered = append(discovered, item)
		inventory.Items = append(inventory.Items, item)
	}
	if err := rows.Close(); err != nil {
		return inventory, err
	}
	if err := rows.Err(); err != nil {
		return inventory, err
	}
	for index := range discovered {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_orchestrator_workflows WHERE tenant_id=? AND workspace_id=? AND environment=? AND origin_id=? AND state IN ('open','paused'))`, scope.TenantID, scope.WorkspaceID, scope.Environment, discovered[index].ID).Scan(&exists); err != nil {
			return inventory, err
		}
		inventory.Items[index].ExistingOpenWorkflow = exists != 0
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_orchestrator_workflows WHERE tenant_id=? AND workspace_id=? AND environment=?`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&inventory.ExistingWorkflows); err != nil {
		return inventory, err
	}
	contractRows, err := s.db.QueryContext(ctx, `SELECT contract_version FROM skill_orchestrator_workflows WHERE tenant_id=? AND workspace_id=? AND environment=? AND contract_version<>? UNION SELECT contract_version FROM skill_orchestrator_jobs WHERE tenant_id=? AND workspace_id=? AND environment=? AND contract_version<>? ORDER BY contract_version`, scope.TenantID, scope.WorkspaceID, scope.Environment, core.SkillOrchestratorContractVersion, scope.TenantID, scope.WorkspaceID, scope.Environment, core.SkillOrchestratorContractVersion)
	if err != nil {
		return inventory, err
	}
	defer contractRows.Close()
	for contractRows.Next() {
		var contract string
		if err := contractRows.Scan(&contract); err != nil {
			return inventory, err
		}
		inventory.UnsupportedContracts = append(inventory.UnsupportedContracts, contract)
	}
	return inventory, contractRows.Err()
}
