package skillorchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func RunReconciliationCursorContract(t *testing.T, repository contracts.SkillOrchestratorRepository, scope core.SkillOrchestratorScope) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	domain := core.SkillReconcileDependencyReadiness
	cursor, err := repository.LoadSkillReconciliationCursor(ctx, scope, domain, 1, now)
	if err != nil || cursor.Cursor != "" || cursor.ConfigurationVersion != 1 {
		t.Fatalf("initial cursor=%+v err=%v", cursor, err)
	}
	updated := cursor
	updated.Cursor = "page-1"
	updated.Counters = core.SkillReconciliationCounters{Scanned: 4, Repaired: 2, Skipped: 1, Blocked: 1}
	updated.UpdatedAt = now.Add(time.Minute)
	updated.LastCompletedAt = updated.UpdatedAt
	input := contracts.SkillReconciliationCursorUpdate{Cursor: updated, ExpectedUpdatedAt: cursor.UpdatedAt}
	if err := repository.SaveSkillReconciliationCursor(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveSkillReconciliationCursor(ctx, input); err == nil {
		t.Fatal("expected stale cursor compare-and-swap rejection")
	}
	loaded, err := repository.LoadSkillReconciliationCursor(ctx, scope, domain, 1, now.Add(2*time.Minute))
	if err != nil || loaded.Cursor != "page-1" || loaded.Counters.Repaired != 2 || loaded.LastCompletedAt.IsZero() {
		t.Fatalf("loaded cursor=%+v err=%v", loaded, err)
	}
	reset, err := repository.LoadSkillReconciliationCursor(ctx, scope, domain, 2, now.Add(3*time.Minute))
	if err != nil || reset.Cursor != "" || reset.ConfigurationVersion != 2 || reset.Counters.Scanned != 0 || !reset.LastCompletedAt.IsZero() {
		t.Fatalf("reset cursor=%+v err=%v", reset, err)
	}
}
