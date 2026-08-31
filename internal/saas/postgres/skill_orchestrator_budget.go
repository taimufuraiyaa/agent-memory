package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (r *SkillOrchestratorRepository) ReserveSkillEvaluationBudget(ctx context.Context, request core.SkillEvaluationBudgetReservationRequest) (core.SkillEvaluationBudgetReservationRecord, error) {
	record := core.SkillEvaluationBudgetReservationRecord{Scope: request.Scope, JobID: request.JobID, PolicyVersion: request.PolicyVersion, PeriodStart: request.PeriodStart, ReservedUnits: request.Units, State: "reserved", ExpiresAt: request.ExpiresAt}
	if err := validateHostedSkillBudgetRequest(request); err != nil {
		return record, err
	}
	tx, err := r.begin(ctx, request.Scope)
	if err != nil {
		return record, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_budget_accounts(tenant_id,workspace_id,environment,policy_version,period_start,limit_units,updated_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, request.PeriodStart, request.LimitUnits, request.Now); err != nil {
		return record, err
	}
	var limit, reserved, committed int64
	if err := tx.QueryRow(ctx, `SELECT limit_units,reserved_units,committed_units FROM saas_skill_orchestrator_budget_accounts WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5 FOR UPDATE`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, request.PeriodStart).Scan(&limit, &reserved, &committed); err != nil {
		return record, err
	}
	if limit != request.LimitUnits {
		return record, errors.New("hosted skill evaluation budget limit binding mismatch")
	}
	var expired int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(reserved_units),0) FROM saas_skill_orchestrator_budget_reservations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5 AND state='reserved' AND expires_at<=$6`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, request.PeriodStart, request.Now).Scan(&expired); err != nil {
		return record, err
	}
	if expired > 0 {
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_reservations SET state='released',updated_at=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5 AND state='reserved' AND expires_at<=$6`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, request.PeriodStart, request.Now); err != nil {
			return record, err
		}
		reserved -= expired
		if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_accounts SET reserved_units=$6,updated_at=$7 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, request.PeriodStart, reserved, request.Now); err != nil {
			return record, err
		}
	}
	var existing core.SkillEvaluationBudgetReservationRecord
	err = tx.QueryRow(ctx, `SELECT policy_version,period_start,reserved_units,committed_units,state,expires_at FROM saas_skill_orchestrator_budget_reservations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid FOR UPDATE`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.JobID).Scan(&existing.PolicyVersion, &existing.PeriodStart, &existing.ReservedUnits, &existing.CommittedUnits, &existing.State, &existing.ExpiresAt)
	if err == nil {
		existing.Scope, existing.JobID = request.Scope, request.JobID
		if existing.State != "released" {
			if existing.PolicyVersion != request.PolicyVersion || !existing.PeriodStart.Equal(request.PeriodStart) || existing.ReservedUnits != request.Units {
				return record, errors.New("hosted skill evaluation budget reservation replay mismatch")
			}
			if err := tx.Commit(ctx); err != nil {
				return record, err
			}
			return existing, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return record, err
	}
	if reserved+committed+request.Units > limit {
		return record, core.ErrSkillEvaluationBudgetExhausted
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_accounts SET reserved_units=reserved_units+$6,updated_at=$7 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, request.PeriodStart, request.Units, request.Now); err != nil {
		return record, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_skill_orchestrator_budget_reservations(tenant_id,workspace_id,environment,job_id,policy_version,period_start,reserved_units,committed_units,state,expires_at,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7,0,'reserved',$8,$9,$9) ON CONFLICT(tenant_id,workspace_id,environment,job_id) DO UPDATE SET policy_version=excluded.policy_version,period_start=excluded.period_start,reserved_units=excluded.reserved_units,committed_units=0,state='reserved',expires_at=excluded.expires_at,updated_at=excluded.updated_at`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.JobID, request.PolicyVersion, request.PeriodStart, request.Units, request.ExpiresAt, request.Now); err != nil {
		return record, err
	}
	if err := tx.Commit(ctx); err != nil {
		return record, err
	}
	return record, nil
}

func (r *SkillOrchestratorRepository) CommitSkillEvaluationBudget(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, units int64, at time.Time) error {
	if err := scope.Validate(); err != nil || jobID == "" || units < 1 || at.IsZero() {
		return errors.New("hosted skill evaluation budget commit is invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var policy, reserved, committed int64
	var period time.Time
	var state string
	if err := tx.QueryRow(ctx, `SELECT policy_version,period_start FROM saas_skill_orchestrator_budget_reservations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID).Scan(&policy, &period); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT 1 FROM saas_skill_orchestrator_budget_accounts WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5 FOR UPDATE`, scope.TenantID, scope.WorkspaceID, scope.Environment, policy, period).Scan(new(int)); err != nil {
		return err
	}
	var lockedPolicy int64
	var lockedPeriod time.Time
	if err := tx.QueryRow(ctx, `SELECT policy_version,period_start,reserved_units,committed_units,state FROM saas_skill_orchestrator_budget_reservations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid FOR UPDATE`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID).Scan(&lockedPolicy, &lockedPeriod, &reserved, &committed, &state); err != nil {
		return err
	}
	if lockedPolicy != policy || !lockedPeriod.Equal(period) {
		return errors.New("hosted skill evaluation budget reservation changed during commit")
	}
	if state == "committed" && committed == units {
		return tx.Commit(ctx)
	}
	if state != "reserved" || units > reserved {
		return errors.New("hosted skill evaluation budget commit does not match reservation")
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_accounts SET reserved_units=reserved_units-$6,committed_units=committed_units+$7,updated_at=$8 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5`, scope.TenantID, scope.WorkspaceID, scope.Environment, policy, period, reserved, units, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_reservations SET committed_units=$5,state='committed',updated_at=$6 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID, units, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SkillOrchestratorRepository) ReleaseSkillEvaluationBudget(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, at time.Time) error {
	if err := scope.Validate(); err != nil || jobID == "" || at.IsZero() {
		return errors.New("hosted skill evaluation budget release is invalid")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var policy, reserved int64
	var period time.Time
	var state string
	if err := tx.QueryRow(ctx, `SELECT policy_version,period_start FROM saas_skill_orchestrator_budget_reservations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID).Scan(&policy, &period); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT 1 FROM saas_skill_orchestrator_budget_accounts WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5 FOR UPDATE`, scope.TenantID, scope.WorkspaceID, scope.Environment, policy, period).Scan(new(int)); err != nil {
		return err
	}
	var lockedPolicy int64
	var lockedPeriod time.Time
	if err := tx.QueryRow(ctx, `SELECT policy_version,period_start,reserved_units,state FROM saas_skill_orchestrator_budget_reservations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid FOR UPDATE`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID).Scan(&lockedPolicy, &lockedPeriod, &reserved, &state); err != nil {
		return err
	}
	if lockedPolicy != policy || !lockedPeriod.Equal(period) {
		return errors.New("hosted skill evaluation budget reservation changed during release")
	}
	if state != "reserved" {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_accounts SET reserved_units=reserved_units-$6,updated_at=$7 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND policy_version=$4 AND period_start=$5`, scope.TenantID, scope.WorkspaceID, scope.Environment, policy, period, reserved, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_skill_orchestrator_budget_reservations SET state='released',updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND environment=$3 AND job_id=$4::uuid`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validateHostedSkillBudgetRequest(request core.SkillEvaluationBudgetReservationRequest) error {
	if err := request.Scope.Validate(); err != nil || request.Scope.TenantID == "" || request.JobID == "" || request.PolicyVersion < 1 || request.LimitUnits < 1 || request.Units < 1 || request.Units > request.LimitUnits || request.PeriodStart.IsZero() || request.Now.IsZero() || !request.ExpiresAt.After(request.Now) {
		return errors.New("hosted skill evaluation budget reservation is invalid")
	}
	return nil
}
