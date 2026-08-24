package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestAuditCommandListsAndExportsEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = store.AppendAuditEvent(context.Background(), sqlite.AuditEventInput{Workspace: "ws", Operation: "delete", Outcome: "success"})
	_ = store.Close()
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"audit", "--db", dbPath, "--workspace", "ws", "--operation", "delete", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("audit: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	events := envelope["data"].(map[string]any)["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("unexpected events: %+v", events)
	}
}
