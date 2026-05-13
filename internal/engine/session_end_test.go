package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestSessionEndExtractor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "session.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipeline := NewWritePipeline(store)
	ex := NewSessionEndExtractor(pipeline)

	transcript := "We found that retries should be exponential.\nThe fix was success after timeout tuning."
	out, err := ex.ExtractAndStore(context.Background(), "ws", transcript)
	if err != nil {
		t.Fatalf("extract and store: %v", err)
	}
	if out.TotalExtracted == 0 || len(out.WrittenIDs) == 0 {
		t.Fatalf("expected extracted entries")
	}
	memories, err := store.ListMemoriesByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	hasProcedural := false
	hasOutcome := false
	for _, m := range memories {
		if m.Type == core.ProceduralMemory {
			hasProcedural = true
		}
		if m.Type == core.OutcomeMemory {
			hasOutcome = true
		}
	}
	if !hasProcedural || !hasOutcome {
		t.Fatalf("expected procedural and outcome memories from transcript extraction")
	}
}
