package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (r *SkillOrchestratorRepository) InspectSkillOrchestratorMigration(ctx context.Context, scope core.SkillOrchestratorScope, limit int) (core.SkillMigrationInventory, error) {
	inventory := core.SkillMigrationInventory{Scope: scope, ConfigurationMode: core.SkillOrchestratorDisabled, Items: []core.SkillMigrationInventoryItem{}, UnsupportedContracts: []string{}}
	if err := scope.Validate(); err != nil || scope.TenantID == "" || limit < 1 || limit > 10_000 {
		return inventory, errors.New("hosted skill orchestrator migration inventory scope is invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return inventory, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT version FROM saas_schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&inventory.SchemaVersion); err != nil {
		return inventory, err
	}
	err = tx.QueryRow(ctx, `SELECT restore_paused FROM saas_skill_orchestrator_reconciliation_partitions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&inventory.RestorePaused)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return inventory, err
	}
	var mode string
	err = tx.QueryRow(ctx, `SELECT mode FROM saas_skill_orchestrator_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 ORDER BY version DESC LIMIT 1`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&mode)
	if err == nil {
		inventory.ConfigurationMode = core.SkillOrchestratorMode(mode)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return inventory, err
	}
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&inventory.ExistingWorkflows); err != nil {
		return inventory, err
	}
	rows, err := tx.Query(ctx, `SELECT contract_version FROM saas_skill_orchestrator_workflows WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND contract_version<>$4 UNION SELECT contract_version FROM saas_skill_orchestrator_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND contract_version<>$4 ORDER BY contract_version`, scope.TenantID, scope.WorkspaceID, scope.Environment, core.SkillOrchestratorContractVersion)
	if err != nil {
		return inventory, err
	}
	for rows.Next() {
		var contract string
		if err := rows.Scan(&contract); err != nil {
			rows.Close()
			return inventory, err
		}
		inventory.UnsupportedContracts = append(inventory.UnsupportedContracts, contract)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return inventory, err
	}
	if err := tx.Commit(ctx); err != nil {
		return inventory, err
	}
	return inventory, nil
}
