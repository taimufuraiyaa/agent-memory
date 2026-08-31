package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillReconciliationPartitionLease struct {
	Scope         core.SkillOrchestratorScope
	Owner         string
	Fence         int64
	LeaseExpires  time.Time
	RestorePaused bool
}

func (r *SkillOrchestratorRepository) ClaimSkillReconciliationPartition(ctx context.Context, scope core.SkillOrchestratorScope, owner string, lease time.Duration, now time.Time) (SkillReconciliationPartitionLease, bool, error) {
	if err := scope.Validate(); err != nil || scope.TenantID == "" || strings.TrimSpace(owner) == "" || lease <= 0 || now.IsZero() {
		return SkillReconciliationPartitionLease{}, false, errors.New("invalid skill reconciliation partition claim")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return SkillReconciliationPartitionLease{}, false, err
	}
	defer tx.Rollback(ctx)
	var admitted bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_workspaces w JOIN saas_tenants t ON t.id=w.tenant_id WHERE w.tenant_id=$1::uuid AND w.id=$2::uuid AND w.state='active' AND t.state='active')`, scope.TenantID, scope.WorkspaceID).Scan(&admitted); err != nil {
		return SkillReconciliationPartitionLease{}, false, err
	}
	if !admitted {
		return SkillReconciliationPartitionLease{}, false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_reconciliation_partitions(tenant_id,workspace_id,environment,updated_at) VALUES($1::uuid,$2::uuid,$3,$4) ON CONFLICT DO NOTHING`, scope.TenantID, scope.WorkspaceID, scope.Environment, now); err != nil {
		return SkillReconciliationPartitionLease{}, false, err
	}
	partition := SkillReconciliationPartitionLease{Scope: scope}
	var expires *time.Time
	if err := tx.QueryRow(ctx, `SELECT owner,fence,lease_expires_at,restore_paused FROM saas_skill_orchestrator_reconciliation_partitions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 FOR UPDATE SKIP LOCKED`, scope.TenantID, scope.WorkspaceID, scope.Environment).Scan(&partition.Owner, &partition.Fence, &expires, &partition.RestorePaused); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SkillReconciliationPartitionLease{Scope: scope}, false, tx.Commit(ctx)
		}
		return SkillReconciliationPartitionLease{}, false, err
	}
	if expires != nil {
		partition.LeaseExpires = *expires
	}
	if partition.RestorePaused || (partition.Owner != "" && partition.Owner != owner && partition.LeaseExpires.After(now)) {
		return partition, false, tx.Commit(ctx)
	}
	partition.Owner, partition.Fence, partition.LeaseExpires = owner, partition.Fence+1, now.Add(lease)
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_reconciliation_partitions SET owner=$4,fence=$5,lease_expires_at=$6,updated_at=$7 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3`, scope.TenantID, scope.WorkspaceID, scope.Environment, owner, partition.Fence, partition.LeaseExpires, now)
	if err != nil {
		return SkillReconciliationPartitionLease{}, false, err
	}
	if tag.RowsAffected() != 1 {
		return SkillReconciliationPartitionLease{}, false, ErrSkillOrchestratorStaleLease
	}
	if err := tx.Commit(ctx); err != nil {
		return SkillReconciliationPartitionLease{}, false, err
	}
	return partition, true, nil
}

func (r *SkillOrchestratorRepository) ReleaseSkillReconciliationPartition(ctx context.Context, lease SkillReconciliationPartitionLease, now time.Time) error {
	if lease.Fence < 1 || now.IsZero() {
		return errors.New("invalid skill reconciliation partition release")
	}
	tx, err := r.begin(ctx, lease.Scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_reconciliation_partitions SET owner='',lease_expires_at=NULL,updated_at=$7 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND owner=$4 AND fence=$5 AND lease_expires_at>$6`, lease.Scope.TenantID, lease.Scope.WorkspaceID, lease.Scope.Environment, lease.Owner, lease.Fence, now, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrSkillOrchestratorStaleLease
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) SetSkillReconciliationRestorePaused(ctx context.Context, scope core.SkillOrchestratorScope, paused bool, now time.Time) error {
	if err := scope.Validate(); err != nil || now.IsZero() {
		return errors.New("invalid skill reconciliation restore pause")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_reconciliation_partitions(tenant_id,workspace_id,environment,restore_paused,updated_at) VALUES($1::uuid,$2::uuid,$3,$4,$5) ON CONFLICT(tenant_id,workspace_id,environment) DO UPDATE SET restore_paused=EXCLUDED.restore_paused,updated_at=EXCLUDED.updated_at`, scope.TenantID, scope.WorkspaceID, scope.Environment, paused, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
