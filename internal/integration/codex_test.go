package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexAdapterConnectIsIdempotentAndDisconnectPreservesUserConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".codex", "config.toml")
	hooksPath := filepath.Join(root, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"gpt-test\"\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := os.WriteFile(hooksPath, []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"custom-hook"}]}]}}`), 0o644); err != nil {
		t.Fatalf("seed hooks: %v", err)
	}
	adapter := NewCodexAdapter()
	options := Options{Root: root, DataDir: t.TempDir(), Workspace: "ws"}
	for range 2 {
		result, err := adapter.Connect(context.Background(), options)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if !result.Verified {
			t.Fatalf("expected verified result: %+v", result)
		}
	}
	config, _ := os.ReadFile(configPath)
	hooks, _ := os.ReadFile(hooksPath)
	if strings.Count(string(config), "BEGIN agent-memory managed Codex sandbox") != 1 || !strings.Contains(string(config), "model = \"gpt-test\"") {
		t.Fatalf("unexpected connected config: %s", config)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "Stop"} {
		if !strings.Contains(string(hooks), `--event `+event) {
			t.Fatalf("missing managed %s hook: %s", event, hooks)
		}
	}

	result, err := adapter.Disconnect(context.Background(), options)
	if err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if !result.Verified {
		t.Fatalf("expected verified disconnect: %+v", result)
	}
	config, _ = os.ReadFile(configPath)
	hooks, _ = os.ReadFile(hooksPath)
	if strings.Contains(string(config), "agent-memory managed") || !strings.Contains(string(config), "model = \"gpt-test\"") {
		t.Fatalf("disconnect damaged config: %s", config)
	}
	if strings.Contains(string(hooks), "agent-memory managed") || !strings.Contains(string(hooks), "custom-hook") {
		t.Fatalf("disconnect damaged hooks: %s", hooks)
	}
}
