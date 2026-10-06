package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func (s *Store) ReserveSkillEvaluationBudget(ctx context.Context, request core.SkillEvaluationBudgetReservationRequest) (core.SkillEvaluationBudgetReservationRecord, error) {
	record := core.SkillEvaluationBudgetReservationRecord{Scope: request.Scope, JobID: request.JobID, PolicyVersion: request.PolicyVersion, PeriodStart: request.PeriodStart, ReservedUnits: request.Units, State: "reserved", ExpiresAt: request.ExpiresAt}
	if err := validateSkillBudgetRequest(request); err != nil {
		return record, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer tx.Rollback()
	periodStart, now, expiresAt := formatSkillOrchestratorTime(request.PeriodStart), formatSkillOrchestratorTime(request.Now), formatSkillOrchestratorTime(request.ExpiresAt)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO skill_orchestrator_budget_accounts(tenant_id,workspace_id,environment,policy_version,period_start,limit_units,updated_at) VALUES(?,?,?,?,?,?,?)`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, periodStart, request.LimitUnits, now); err != nil {
		return record, err
	}
	var limit, reserved, committed int64
	if err := tx.QueryRowContext(ctx, `SELECT limit_units,reserved_units,committed_units FROM skill_orchestrator_budget_accounts WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=?`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, periodStart).Scan(&limit, &reserved, &committed); err != nil {
		return record, err
	}
	if limit != request.LimitUnits {
		return record, errors.New("skill evaluation budget limit binding mismatch")
	}
	var expired int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(reserved_units),0) FROM skill_orchestrator_budget_reservations WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=? AND state='reserved' AND expires_at<=?`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, periodStart, now).Scan(&expired); err != nil {
		return record, err
	}
	if expired > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_reservations SET state='released',updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=? AND state='reserved' AND expires_at<=?`, now, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, periodStart, now); err != nil {
			return record, err
		}
		reserved -= expired
		if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_accounts SET reserved_units=?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=?`, reserved, now, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, periodStart); err != nil {
			return record, err
		}
	}
	var existing core.SkillEvaluationBudgetReservationRecord
	var existingPeriod, existingExpiry string
	err = tx.QueryRowContext(ctx, `SELECT policy_version,period_start,reserved_units,committed_units,state,expires_at FROM skill_orchestrator_budget_reservations WHERE tenant_id=? AND workspace_id=? AND environment=? AND job_id=?`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.JobID).Scan(&existing.PolicyVersion, &existingPeriod, &existing.ReservedUnits, &existing.CommittedUnits, &existing.State, &existingExpiry)
	if err == nil {
		existing.Scope, existing.JobID = request.Scope, request.JobID
		existing.PeriodStart, _ = parseSkillOrchestratorTime(existingPeriod)
		existing.ExpiresAt, _ = parseSkillOrchestratorTime(existingExpiry)
		if existing.State != "released" {
			if existing.PolicyVersion != request.PolicyVersion || !existing.PeriodStart.Equal(request.PeriodStart) || existing.ReservedUnits != request.Units {
				return record, errors.New("skill evaluation budget reservation replay mismatch")
			}
			if err := tx.Commit(); err != nil {
				return record, err
			}
			return existing, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return record, err
	}
	if reserved+committed+request.Units > limit {
		return record, core.ErrSkillEvaluationBudgetExhausted
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_accounts SET reserved_units=reserved_units+?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=?`, request.Units, now, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.PolicyVersion, periodStart); err != nil {
		return record, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO skill_orchestrator_budget_reservations(tenant_id,workspace_id,environment,job_id,policy_version,period_start,reserved_units,committed_units,state,expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,0,'reserved',?,?,?) ON CONFLICT(tenant_id,workspace_id,environment,job_id) DO UPDATE SET policy_version=excluded.policy_version,period_start=excluded.period_start,reserved_units=excluded.reserved_units,committed_units=0,state='reserved',expires_at=excluded.expires_at,updated_at=excluded.updated_at`, request.Scope.TenantID, request.Scope.WorkspaceID, request.Scope.Environment, request.JobID, request.PolicyVersion, periodStart, request.Units, expiresAt, now, now); err != nil {
		return record, err
	}
	if err := tx.Commit(); err != nil {
		return record, err
	}
	return record, nil
}

func (s *Store) CommitSkillEvaluationBudget(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, units int64, at time.Time) error {
	if err := scope.Validate(); err != nil || jobID == "" || units < 1 || at.IsZero() {
		return errors.New("skill evaluation budget commit is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var policy, reserved, committed int64
	var period, state string
	if err := tx.QueryRowContext(ctx, `SELECT policy_version,period_start,reserved_units,committed_units,state FROM skill_orchestrator_budget_reservations WHERE tenant_id=? AND workspace_id=? AND environment=? AND job_id=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID).Scan(&policy, &period, &reserved, &committed, &state); err != nil {
		return err
	}
	if state == "committed" && committed == units {
		return tx.Commit()
	}
	if state != "reserved" || units > reserved {
		return errors.New("skill evaluation budget commit does not match reservation")
	}
	now := formatSkillOrchestratorTime(at)
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_accounts SET reserved_units=reserved_units-?,committed_units=committed_units+?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=?`, reserved, units, now, scope.TenantID, scope.WorkspaceID, scope.Environment, policy, period); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_reservations SET committed_units=?,state='committed',updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND job_id=?`, units, now, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReleaseSkillEvaluationBudget(ctx context.Context, scope core.SkillOrchestratorScope, jobID string, at time.Time) error {
	if err := scope.Validate(); err != nil || jobID == "" || at.IsZero() {
		return errors.New("skill evaluation budget release is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var policy, reserved int64
	var period, state string
	if err := tx.QueryRowContext(ctx, `SELECT policy_version,period_start,reserved_units,state FROM skill_orchestrator_budget_reservations WHERE tenant_id=? AND workspace_id=? AND environment=? AND job_id=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID).Scan(&policy, &period, &reserved, &state); err != nil {
		return err
	}
	if state != "reserved" {
		return tx.Commit()
	}
	now := formatSkillOrchestratorTime(at)
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_accounts SET reserved_units=reserved_units-?,updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=?`, reserved, now, scope.TenantID, scope.WorkspaceID, scope.Environment, policy, period); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_orchestrator_budget_reservations SET state='released',updated_at=? WHERE tenant_id=? AND workspace_id=? AND environment=? AND job_id=?`, now, scope.TenantID, scope.WorkspaceID, scope.Environment, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func validateSkillBudgetRequest(request core.SkillEvaluationBudgetReservationRequest) error {
	if err := request.Scope.Validate(); err != nil || request.JobID == "" || request.PolicyVersion < 1 || request.LimitUnits < 1 || request.Units < 1 || request.Units > request.LimitUnits || request.PeriodStart.IsZero() || request.Now.IsZero() || !request.ExpiresAt.After(request.Now) {
		return errors.New("skill evaluation budget reservation is invalid")
	}
	return nil
}
