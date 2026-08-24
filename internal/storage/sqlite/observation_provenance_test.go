package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestObservationProvenanceSurvivesInsertAndQuery(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, _, err = store.InsertObservationDedupWindow(ctx, ObservationInsert{
		Workspace: "ws", SessionID: "s1", OccurredAt: time.Now().UTC(), Kind: "tool_result", Summary: "tests passed",
		SourceAgent: "codex", SourceAdapter: "codex-hooks", HookEvent: "PostToolUse", ExternalEventID: "evt-1", SchemaVersion: "v1", CaptureMode: "live",
	}, time.Minute)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	observations, err := store.ListObservations(ctx, "ws", "s1", nil, nil, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(observations) != 1 || observations[0].SourceAgent != "codex" || observations[0].HookEvent != "PostToolUse" || observations[0].ExternalEventID != "evt-1" || observations[0].SchemaVersion != "v1" {
		t.Fatalf("missing provenance: %+v", observations)
	}
}
