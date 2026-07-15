package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestAuditAppendListFilterAndBoundedTargets(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	targets := make([]string, 150)
	for i := range targets {
		targets[i] = "memory"
	}
	event, err := store.AppendAuditEvent(ctx, AuditEventInput{
		Workspace: "ws", Operation: "delete", Outcome: "success", Actor: "cli", Source: "connect", RequestID: "r1", TargetType: "memory", TargetIDs: targets, Reason: "user request", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if event.TargetCount != 150 || len(event.TargetIDs) != 100 {
		t.Fatalf("targets not bounded: %+v", event)
	}
	events, err := store.ListAuditEvents(ctx, AuditFilter{Workspace: "ws", Operation: "delete", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 || events[0].RequestID != "r1" {
		t.Fatalf("unexpected events: %+v", events)
	}
	memories, err := store.ListRecentMemoriesByWorkspace(ctx, "ws", 10)
	if err != nil || len(memories) != 0 {
		t.Fatalf("audit leaked into memories: %+v err=%v", memories, err)
	}
}

func TestAuditedDeleteRollsBackWhenAuditInsertFails(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	memory := core.MemoryEntry{ID: "m1", Workspace: "ws", Type: core.SemanticMemory, Content: "keep on audit failure", Confidence: 0.9, StorageTier: core.TierVector, Source: core.MemorySource{Type: core.SourceUserInput}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.InsertMemoryByHash(ctx, &memory, "h1"); err != nil {
		t.Fatalf("insert memory: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatalf("trigger: %v", err)
	}

	err = store.DeleteByIDsAudited(ctx, []string{"m1"}, AuditEventInput{Workspace: "ws", Operation: "delete", Outcome: "success", Actor: "test"})

	if err == nil {
		t.Fatal("expected audit failure")
	}
	got, err := store.GetMemory(ctx, "m1")
	if err != nil || got.ID != "m1" {
		t.Fatalf("delete was not rolled back: got=%+v err=%v", got, err)
	}
}
