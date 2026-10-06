package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSQLiteSkillEvaluationBudgetIsAtomicReplaySafeAndScoped(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	scope := core.SkillOrchestratorScope{TenantID: "tenant-a", WorkspaceID: "ws", Environment: "production"}
	request := budgetReservationRequest(scope, "job-budget-a", 6, now)
	first, err := store.ReserveSkillEvaluationBudget(ctx, request)
	if err != nil || first.State != "reserved" || first.ReservedUnits != 6 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := store.ReserveSkillEvaluationBudget(ctx, request)
	if err != nil || replay.JobID != first.JobID || replay.ReservedUnits != first.ReservedUnits {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	exhausted := budgetReservationRequest(scope, "job-budget-b", 5, now)
	if _, err := store.ReserveSkillEvaluationBudget(ctx, exhausted); !errors.Is(err, core.ErrSkillEvaluationBudgetExhausted) {
		t.Fatalf("expected exhaustion, got %v", err)
	}
	if err := store.ReleaseSkillEvaluationBudget(ctx, scope, request.JobID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveSkillEvaluationBudget(ctx, exhausted); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitSkillEvaluationBudget(ctx, scope, exhausted.JobID, 4, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitSkillEvaluationBudget(ctx, scope, exhausted.JobID, 4, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("idempotent commit failed: %v", err)
	}
	expiring := budgetReservationRequest(scope, "job-budget-expiring", 6, now.Add(4*time.Minute))
	expiring.ExpiresAt = now.Add(5 * time.Minute)
	if _, err := store.ReserveSkillEvaluationBudget(ctx, expiring); err != nil {
		t.Fatal(err)
	}
	afterExpiry := budgetReservationRequest(scope, "job-budget-after-expiry", 6, now.Add(6*time.Minute))
	if _, err := store.ReserveSkillEvaluationBudget(ctx, afterExpiry); err != nil {
		t.Fatalf("expired reservation did not release units: %v", err)
	}
	other := scope
	other.Environment = "staging"
	isolated := budgetReservationRequest(other, "job-budget-staging", 10, now)
	if _, err := store.ReserveSkillEvaluationBudget(ctx, isolated); err != nil {
		t.Fatalf("independent environment budget failed: %v", err)
	}
	var reserved, committed int64
	if err := store.db.QueryRowContext(ctx, `SELECT reserved_units,committed_units FROM skill_orchestrator_budget_accounts WHERE tenant_id=? AND workspace_id=? AND environment=? AND policy_version=? AND period_start=?`, scope.TenantID, scope.WorkspaceID, scope.Environment, request.PolicyVersion, formatSkillOrchestratorTime(request.PeriodStart)).Scan(&reserved, &committed); err != nil {
		t.Fatal(err)
	}
	if reserved != 6 || committed != 4 {
		t.Fatalf("account reserved=%d committed=%d", reserved, committed)
	}
}

func budgetReservationRequest(scope core.SkillOrchestratorScope, jobID string, units int64, now time.Time) core.SkillEvaluationBudgetReservationRequest {
	return core.SkillEvaluationBudgetReservationRequest{Scope: scope, JobID: jobID, PolicyVersion: 3, PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), LimitUnits: 10, Units: units, ExpiresAt: now.Add(10 * time.Minute), Now: now}
}
