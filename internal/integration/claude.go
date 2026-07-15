package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ClaudeAdapter struct{}

const claudeHookMarker = "agent-memory managed hook"

func NewClaudeAdapter() ClaudeAdapter { return ClaudeAdapter{} }
func (ClaudeAdapter) Name() string    { return "claude-code" }

func (ClaudeAdapter) Detect(_ context.Context, options Options) (bool, error) {
	if _, err := os.Stat(filepath.Join(options.Root, ".claude")); err == nil {
		return true, nil
	}
	_, err := os.Stat(filepath.Join(options.Root, "CLAUDE.md"))
	return err == nil, nil
}

func (ClaudeAdapter) Plan(_ context.Context, options Options) (Result, error) {
	return Result{Agent: "claude-code", Planned: claudePaths(options.Root)}, nil
}

func (ClaudeAdapter) Connect(_ context.Context, options Options) (Result, error) {
	path := filepath.Join(options.Root, ".mcp.json")
	root, err := readJSONObject(path)
	if err != nil {
		return Result{}, err
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		root["mcpServers"] = servers
	}
	servers["agent-memory"] = map[string]any{
		"command": "agent-memory-mcp",
		"env": map[string]any{
			"AGENT_MEMORY_URL": "http://127.0.0.1:3210",
		},
	}
	if err := writeJSONObject(path, root); err != nil {
		return Result{}, err
	}
	settingsPath := filepath.Join(options.Root, ".claude", "settings.json")
	if err := writeClaudeHooks(settingsPath, options.Workspace); err != nil {
		return Result{}, err
	}
	verified, err := verifyClaude(options.Root, true)
	return Result{Agent: "claude-code", Applied: claudePaths(options.Root), Verified: verified}, err
}

func (ClaudeAdapter) Disconnect(_ context.Context, options Options) (Result, error) {
	path := filepath.Join(options.Root, ".mcp.json")
	root, err := readJSONObject(path)
	if err != nil {
		return Result{}, err
	}
	if servers, ok := root["mcpServers"].(map[string]any); ok {
		delete(servers, "agent-memory")
	}
	if err := writeJSONObject(path, root); err != nil {
		return Result{}, err
	}
	settingsPath := filepath.Join(options.Root, ".claude", "settings.json")
	if err := removeClaudeHooks(settingsPath); err != nil {
		return Result{}, err
	}
	verified, err := verifyClaude(options.Root, false)
	return Result{Agent: "claude-code", Removed: claudePaths(options.Root), Verified: verified}, err
}

func (ClaudeAdapter) Verify(_ context.Context, options Options) (Result, error) {
	verified, err := verifyClaude(options.Root, true)
	return Result{Agent: "claude-code", Verified: verified}, err
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

func writeJSONObject(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".agent-memory.tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func verifyClaude(rootPath string, connected bool) (bool, error) {
	root, err := readJSONObject(filepath.Join(rootPath, ".mcp.json"))
	if err != nil {
		return false, err
	}
	servers, _ := root["mcpServers"].(map[string]any)
	_, exists := servers["agent-memory"]
	settings, err := readJSONObject(filepath.Join(rootPath, ".claude", "settings.json"))
	if err != nil {
		return false, err
	}
	hasHooks := strings.Contains(fmt.Sprint(settings["hooks"]), claudeHookMarker)
	return exists == connected && hasHooks == connected, nil
}

func claudePaths(root string) []string {
	return []string{filepath.Join(root, ".mcp.json"), filepath.Join(root, ".claude", "settings.json")}
}

func writeClaudeHooks(path, workspaceName string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooksMap, _ := root["hooks"].(map[string]any)
	if hooksMap == nil {
		hooksMap = map[string]any{}
		root["hooks"] = hooksMap
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "Stop"} {
		groups, _ := hooksMap[event].([]any)
		kept := make([]any, 0, len(groups)+1)
		for _, group := range groups {
			if !strings.Contains(fmt.Sprint(group), claudeHookMarker) {
				kept = append(kept, group)
			}
		}
		command := fmt.Sprintf("agent-memory hook --event %s --agent claude-code --workspace %s # %s", event, workspaceName, claudeHookMarker)
		kept = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 2}}})
		hooksMap[event] = kept
	}
	return writeJSONObject(path, root)
}

func removeClaudeHooks(path string) error {
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooksMap, _ := root["hooks"].(map[string]any)
	for event, raw := range hooksMap {
		groups, _ := raw.([]any)
		kept := make([]any, 0, len(groups))
		for _, group := range groups {
			if !strings.Contains(fmt.Sprint(group), claudeHookMarker) {
				kept = append(kept, group)
			}
		}
		hooksMap[event] = kept
	}
	return writeJSONObject(path, root)
}
