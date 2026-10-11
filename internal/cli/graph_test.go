package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestGraphIndexOperationCLIUsesIdempotentStoreAndExposesStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "graph.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	configuration := core.GraphConfiguration{ID: "default", Scope: core.GraphScope{WorkspaceID: "ws"}, Version: 1, Enabled: true, AdapterName: "agent-memory-graphrag-adapter", AdapterVersion: contracts.SupportedGraphAdapterVersion, IndexMethod: core.GraphIndexStandard, ProjectionVersion: "v1", ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1, PromptFingerprint: "sha256:test", ModelRoute: "local", CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertGraphConfiguration(context.Background(), configuration); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	run := func(args ...string) map[string]any {
		t.Helper()
		cmd := NewRootCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs(append([]string{"graph-index", "--workspace", "ws", "--db", dbPath}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute %v: %v (%s)", args, err, output.String())
		}
		var envelope map[string]any
		if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v: %s", err, output.String())
		}
		return envelope
	}
	first := run("--idempotency-key", "request-1", "update")
	data := first["data"].(map[string]any)
	if data["accepted"] != true || data["coalesced"] != false {
		t.Fatalf("unexpected first operation: %#v", data)
	}
	second := run("--idempotency-key", "request-1", "update")
	if second["data"].(map[string]any)["coalesced"] != true {
		t.Fatalf("expected coalesced replay: %#v", second)
	}
	status := run("status")["data"].(map[string]any)
	if status["state"] != "queued" || status["current_job"] == nil {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestGraphIndexOperationCLIHasNoArtifactRootOverride(t *testing.T) {
	command := newGraphCommand()
	for _, forbidden := range []string{"artifact-root", "job-root", "output-root", "input-root"} {
		if command.PersistentFlags().Lookup(forbidden) != nil {
			t.Fatalf("unsafe storage override exposed: %s", forbidden)
		}
	}
}
