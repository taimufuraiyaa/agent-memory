package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestLibraryMigrationPreservesExistingMemories(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	written, err := engine.NewWritePipeline(store).Write(ctx, engine.WriteInput{Workspace: "existing", Type: core.SemanticMemory, Content: "existing project memory", Source: core.MemorySource{Type: core.SourceReflection}, Mode: engine.ExtractFast})
	if err != nil || written.Rejected {
		t.Fatalf("seed memory: %+v %v", written, err)
	}
	_ = store.Close()
	store, err = sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	memory, err := store.GetMemory(ctx, written.ID)
	if err != nil || memory.Content != "existing project memory" {
		t.Fatalf("migration changed existing memory: %+v %v", memory, err)
	}
}

func TestLibraryRecoveryQueuesInterruptedJobsAndRebuildsIndexes(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutLibraryImportJob(ctx, "job", "books", "running", `{"id":"job","state":"running"}`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	count, err := store.RecoverLibraryImportJobs(ctx)
	if err != nil || count != 1 {
		t.Fatalf("recovery failed count=%d err=%v", count, err)
	}
	payload, err := store.GetLibraryImportJob(ctx, "job")
	if err != nil || !strings.Contains(payload, `"state":"queued"`) {
		t.Fatalf("job is not resumable: %s %v", payload, err)
	}
	if err := store.RebuildLibraryIndexes(ctx); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
}
