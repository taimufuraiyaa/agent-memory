package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestRunSessionEndLifecycleRunsLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "session-end-lifecycle.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	transcript := "Always run database migrations before deploying the orders API to production." +
		" The deployment was successful after fixing the connection timeout configuration."
	out, err := RunSessionEndLifecycle(context.Background(), "ws", transcript, store, NewWritePipeline(store))
	if err != nil {
		t.Fatalf("run session-end lifecycle: %v", err)
	}
	if !out.LifecycleRan {
		t.Fatalf("expected lifecycle to run")
	}
	if out.TotalExtracted == 0 {
		t.Fatalf("expected extracted memories")
	}
	if out.LifecycleMetrics == nil {
		t.Fatalf("expected lifecycle metrics")
	}
	if out.LifecycleMetrics.DecayUpdated == 0 {
		t.Fatalf("expected lifecycle to update decay metrics")
	}
}
