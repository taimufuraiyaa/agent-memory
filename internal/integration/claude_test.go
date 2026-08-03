package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAdapterPreservesOtherMCPServersAndHooks(t *testing.T) {
	root := t.TempDir()
	settingsDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"custom":{"command":"custom-mcp"}}}`), 0o644); err != nil {
		t.Fatalf("seed mcp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"custom-hook"}]}]}}`), 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	adapter := NewClaudeAdapter()
	options := Options{Root: root, DataDir: t.TempDir(), Workspace: "ws"}
	for range 2 {
		result, err := adapter.Connect(context.Background(), options)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if !result.Verified {
			t.Fatalf("expected verified connect: %+v", result)
		}
	}
	mcp, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	settings, _ := os.ReadFile(filepath.Join(settingsDir, "settings.json"))
	if !strings.Contains(string(mcp), "custom-mcp") || strings.Count(string(mcp), `"agent-memory"`) != 1 {
		t.Fatalf("unexpected mcp config: %s", mcp)
	}
	if !strings.Contains(string(settings), "custom-hook") {
		t.Fatalf("custom hook was not preserved: %s", settings)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "Stop"} {
		if !strings.Contains(string(settings), `--event `+event) {
			t.Fatalf("missing managed %s hook: %s", event, settings)
		}
	}

	result, err := adapter.Disconnect(context.Background(), options)
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if !result.Verified {
		t.Fatalf("expected verified disconnect: %+v", result)
	}
	mcp, _ = os.ReadFile(filepath.Join(root, ".mcp.json"))
	settings, _ = os.ReadFile(filepath.Join(settingsDir, "settings.json"))
	if strings.Contains(string(mcp), `"agent-memory"`) || strings.Contains(string(settings), "agent-memory managed hook") || !strings.Contains(string(mcp), "custom-mcp") || !strings.Contains(string(settings), "custom-hook") {
		t.Fatalf("disconnect damaged user config: mcp=%s settings=%s", mcp, settings)
	}
}
