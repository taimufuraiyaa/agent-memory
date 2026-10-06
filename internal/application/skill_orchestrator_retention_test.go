package application

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillAttemptRetentionSweepIsBoundedAndUsesApprovedCutoff(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := &skillAttemptRetentionFixture{removed: 25}
	sweep, err := NewSkillAttemptRetentionSweep(repository, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := SkillReconciliationRequest{Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, Domain: core.SkillReconcileTerminalCleanup, Limit: 25, Now: now}
	result, err := sweep.Sweep(context.Background(), request)
	if err != nil || result.Complete || result.Counters.Scanned != 25 || result.Counters.Repaired != 25 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if repository.limit != 25 || !repository.before.Equal(now.Add(-30*24*time.Hour)) || repository.scope != request.Scope {
		t.Fatalf("retention call scope=%+v before=%v limit=%d", repository.scope, repository.before, repository.limit)
	}
	repository.removed = 2
	result, err = sweep.Sweep(context.Background(), request)
	if err != nil || !result.Complete {
		t.Fatalf("terminal page result=%+v err=%v", result, err)
	}
}

type skillAttemptRetentionFixture struct {
	scope   core.SkillOrchestratorScope
	before  time.Time
	limit   int
	removed int64
}

func (f *skillAttemptRetentionFixture) PruneSkillOrchestratorAttempts(_ context.Context, scope core.SkillOrchestratorScope, before time.Time, limit int) (int64, error) {
	f.scope, f.before, f.limit = scope, before, limit
	return f.removed, nil
}
