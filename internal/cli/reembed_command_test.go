package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestReembedCommandBackfillsVectorsWithProvider(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reembed.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpsertMemory(context.Background(), &core.MemoryEntry{
		ID:          "m1",
		Type:        core.SemanticMemory,
		Content:     "orders service emits order.created event",
		Workspace:   "ws",
		Source:      core.MemorySource{Type: core.SourceUserInput},
		Confidence:  0.9,
		StorageTier: core.TierVector,
	}); err != nil {
		t.Fatalf("upsert memory: %v", err)
	}

	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"re-embed",
		"--db", dbPath,
		"--workspace", "ws",
		"--model-dir", modelDir,
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("re-embed execute: %v output=%s", err, out.String())
	}

	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v raw=%q", err, out.String())
	}
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("expected ok=true envelope, got %+v", env)
	}
	data, _ := env["data"].(map[string]any)
	if provider, _ := data["provider"].(string); provider == "" {
		t.Fatalf("expected provider in response, got %+v", data)
	}
	if reembedded, _ := data["re_embedded"].(float64); int(reembedded) != 1 {
		t.Fatalf("expected one re-embedded vector, got %+v", data)
	}

	rows, err := store.ListMemoryVectorRowsByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list vector rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one vector row, got %+v", rows)
	}
	if rows[0].EmbeddingProvider == "" {
		t.Fatalf("expected provider provenance on vector row, got %+v", rows[0])
	}
}
