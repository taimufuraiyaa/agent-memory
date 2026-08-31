package application

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestPersistentSkillEvaluationBudgetBindsPeriodTTLAndActualCost(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 34, 56, 0, time.UTC)
	repository := &persistentBudgetFixture{}
	budget, err := NewPersistentSkillEvaluationBudget(repository, PersistentSkillEvaluationBudgetConfig{LimitUnits: 100, Period: 24 * time.Hour, ReservationTTL: 10 * time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := core.SkillOrchestratorScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", Environment: "production"}
	reservation, err := budget.Reserve(context.Background(), SkillEvaluationBudgetRequest{Scope: scope, JobID: "job-budget", PolicyVersion: 7, Units: 20})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.request.PeriodStart.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !repository.request.ExpiresAt.Equal(now.Add(10*time.Minute)) || repository.request.LimitUnits != 100 {
		t.Fatalf("request=%+v", repository.request)
	}
	if err := reservation.Commit(context.Background(), 15); err != nil {
		t.Fatal(err)
	}
	if repository.committed != 15 || repository.jobID != "job-budget" {
		t.Fatalf("commit units=%d job=%s", repository.committed, repository.jobID)
	}
}

type persistentBudgetFixture struct {
	request   core.SkillEvaluationBudgetReservationRequest
	jobID     string
	committed int64
}

func (f *persistentBudgetFixture) ReserveSkillEvaluationBudget(_ context.Context, request core.SkillEvaluationBudgetReservationRequest) (core.SkillEvaluationBudgetReservationRecord, error) {
	f.request = request
	return core.SkillEvaluationBudgetReservationRecord{Scope: request.Scope, JobID: request.JobID, PolicyVersion: request.PolicyVersion, PeriodStart: request.PeriodStart, ReservedUnits: request.Units, State: "reserved", ExpiresAt: request.ExpiresAt}, nil
}

func (f *persistentBudgetFixture) CommitSkillEvaluationBudget(_ context.Context, _ core.SkillOrchestratorScope, jobID string, units int64, _ time.Time) error {
	f.jobID, f.committed = jobID, units
	return nil
}

func (f *persistentBudgetFixture) ReleaseSkillEvaluationBudget(context.Context, core.SkillOrchestratorScope, string, time.Time) error {
	return nil
}
