package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestReplayEventsExposeMemoryObservationProvenance(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	observation, _, err := store.InsertObservationDedupWindow(ctx, ObservationInsert{Workspace: "ws", SessionID: "s1", OccurredAt: time.Now().UTC(), Kind: "tool_result", ToolName: "go test", Summary: "tests passed", SourceAgent: "codex", SchemaVersion: "v1"}, time.Minute)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := store.LinkMemoryObservations(ctx, "m1", []string{observation.ID}); err != nil {
		t.Fatalf("link: %v", err)
	}
	events, err := store.LoadReplayEvents(ctx, "ws", "s1", 100, "")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(events) != 1 || events[0].RelatedMemoryIDs[0] != "m1" || events[0].Actor != "codex" || events[0].Summary != "tests passed" {
		t.Fatalf("unexpected replay events: %+v", events)
	}
}
