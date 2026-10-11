package integration

import (
	"context"
	"encoding/json"
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
	rules, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	skill, _ := os.ReadFile(claudeSkillPath(root))
	if !strings.Contains(string(mcp), "custom-mcp") || strings.Count(string(mcp), `"agent-memory"`) != 1 {
		t.Fatalf("unexpected mcp config: %s", mcp)
	}
	if !strings.Contains(string(settings), "custom-hook") {
		t.Fatalf("custom hook was not preserved: %s", settings)
	}
	if !strings.Contains(string(rules), "agent-memory operating contract:") {
		t.Fatalf("Claude connection did not install the memory contract: %s", rules)
	}
	if !strings.Contains(string(skill), "agent-memory listen on --workspace ws") {
		t.Fatalf("missing /am listen skill: %s", skill)
	}
	if !strings.Contains(string(skill), `"listen all"`) || !strings.Contains(string(skill), "agent-memory listen on --jev --workspace ws") {
		t.Fatalf("missing unified local and Jev listen form: %s", skill)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "Stop"} {
		if !strings.Contains(string(settings), `--event `+event) {
			t.Fatalf("missing managed %s hook: %s", event, settings)
		}
	}
	var settingsObject map[string]any
	if err := json.Unmarshal(settings, &settingsObject); err != nil {
		t.Fatal(err)
	}
	preTool := settingsObject["hooks"].(map[string]any)["PreToolUse"].([]any)
	if timeout := preTool[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"]; timeout != float64(4) {
		t.Fatalf("PreToolUse timeout cannot cover bounded Jev call: %v", timeout)
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
	rules, _ = os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if _, err := os.Stat(claudeSkillPath(root)); !os.IsNotExist(err) {
		t.Fatalf("managed Claude skill remains after disconnect: %v", err)
	}
	if strings.Contains(string(mcp), `"agent-memory"`) || strings.Contains(string(settings), "agent-memory managed hook") || !strings.Contains(string(mcp), "custom-mcp") || !strings.Contains(string(settings), "custom-hook") {
		t.Fatalf("disconnect damaged user config: mcp=%s settings=%s", mcp, settings)
	}
	if strings.Contains(string(rules), "## agent-memory (MANDATORY)") {
		t.Fatalf("disconnect left managed Claude rules: %s", rules)
	}
}

func TestClaudeAdapterDoesNotOverwriteUserAMSkill(t *testing.T) {
	root := t.TempDir()
	path := claudeSkillPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClaudeAdapter().Connect(context.Background(), Options{Root: root, Workspace: "ws"}); err == nil {
		t.Fatal("expected collision error")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "user skill\n" {
		t.Fatalf("user skill was modified: %q, %v", content, err)
	}
}
