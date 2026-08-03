package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestReindexTermsCommandBackfillsAndPublishes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reindex-terms.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID:          "memory-a",
		Type:        core.SemanticMemory,
		Content:     "The #HotPath uses Orders.API",
		Workspace:   "ws",
		Entities:    []string{"Payment Gateway"},
		Source:      core.MemorySource{Type: core.SourceCodeAnalysis},
		StorageTier: core.TierVector,
		Confidence:  0.9,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}
	_ = store.Close()

	payload := runCLIJSON(t,
		"reindex-terms",
		"--db", dbPath,
		"--workspace", "ws",
		"--target-fpp", "0.01",
		"--format", "json",
	)
	if payload["command"] != "reindex-terms" {
		t.Fatalf("unexpected command envelope: %#v", payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data["workspace"] != "ws" || data["distinct_terms"].(float64) != 3 {
		t.Fatalf("unexpected rebuild report: %#v", data)
	}
}

func TestReindexTermsCommandStatusReportsSafeMetadata(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reindex-terms-status.db")
	payload := runCLIJSON(t,
		"reindex-terms",
		"--db", dbPath,
		"--workspace", "ws",
		"--status",
		"--format", "json",
	)
	data, _ := payload["data"].(map[string]any)
	if data["workspace"] != "ws" || data["rebuild_reason"] != "state_missing" {
		t.Fatalf("unexpected missing-index status: %#v", data)
	}
	if _, leaked := data["bitmap"]; leaked {
		t.Fatalf("status leaked raw Bloom bitmap: %#v", data)
	}
}
