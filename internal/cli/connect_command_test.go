package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectDryRunAndClaudeRoundTrip(t *testing.T) {
	root := t.TempDir()
	mcpPath := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{"custom":{"command":"custom"}}}`), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	dry := NewRootCommand()
	var dryOut bytes.Buffer
	dry.SetOut(&dryOut)
	dry.SetArgs([]string{"connect", "claude-code", "--root", root, "--workspace", "ws", "--data-dir", t.TempDir(), "--dry-run"})
	if err := dry.Execute(); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	unchanged, _ := os.ReadFile(mcpPath)
	if bytes.Contains(unchanged, []byte("agent-memory")) {
		t.Fatalf("dry run mutated config: %s", unchanged)
	}

	connect := NewRootCommand()
	var connectOut bytes.Buffer
	connect.SetOut(&connectOut)
	connect.SetArgs([]string{"connect", "claude-code", "--root", root, "--workspace", "ws", "--data-dir", t.TempDir()})
	if err := connect.Execute(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(connectOut.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	data := envelope["data"].(map[string]any)
	backups := data["backups"].([]any)
	if len(backups) != 1 {
		t.Fatalf("expected backup: %+v", data)
	}
	info, err := os.Stat(backups[0].(string))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup permissions: info=%v err=%v", info, err)
	}

	disconnect := NewRootCommand()
	disconnect.SetOut(&bytes.Buffer{})
	disconnect.SetArgs([]string{"disconnect", "claude-code", "--root", root, "--workspace", "ws", "--data-dir", t.TempDir()})
	if err := disconnect.Execute(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	final, _ := os.ReadFile(mcpPath)
	if bytes.Contains(final, []byte(`"agent-memory"`)) || !bytes.Contains(final, []byte("custom")) {
		t.Fatalf("disconnect damaged config: %s", final)
	}
}
