package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/validation"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type ClaudeAdapter struct{}

const claudeHookMarker = "agent-memory managed hook"
const claudeSkillMarker = "agent-memory managed Claude skill"

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
	if err := validation.ValidateWorkspaceName(options.Workspace); err != nil {
		return Result{}, err
	}
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
	if err := workspace.WriteManagedProjectRule(filepath.Join(options.Root, "CLAUDE.md"), options.Workspace, options.Force); err != nil {
		return Result{}, err
	}
	if err := writeClaudeSkill(options.Root, options.Workspace); err != nil {
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
	if err := workspace.RemoveManagedProjectRule(filepath.Join(options.Root, "CLAUDE.md")); err != nil {
		return Result{}, err
	}
	if err := removeClaudeSkill(options.Root); err != nil {
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
	rules, readErr := os.ReadFile(filepath.Join(rootPath, "CLAUDE.md"))
	hasContract := readErr == nil && strings.Contains(string(rules), workspace.MemoryContractMarker)
	skill, skillErr := os.ReadFile(claudeSkillPath(rootPath))
	hasSkill := skillErr == nil && strings.Contains(string(skill), claudeSkillMarker)
	return exists == connected && hasHooks == connected && hasContract == connected && hasSkill == connected, nil
}

func claudePaths(root string) []string {
	return []string{filepath.Join(root, ".mcp.json"), filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, "CLAUDE.md"), claudeSkillPath(root)}
}

func claudeSkillPath(root string) string {
	return filepath.Join(root, ".claude", "skills", "am", "SKILL.md")
}

func writeClaudeSkill(root, workspaceName string) error {
	path := claudeSkillPath(root)
	if existing, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(existing), claudeSkillMarker) {
			return fmt.Errorf("Claude /am skill already exists and is not managed by agent-memory: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`---
name: am
description: Control Agent Memory's opt-in Claude Code prompt listener
disable-model-invocation: true
---

<!-- %s -->

Interpret $ARGUMENTS strictly:
- "listen" or "listen on": run `+"`agent-memory listen on --workspace %s`"+`.
- "listen all": this explicitly opts into sending bounded, redacted future prompts and project skill names/descriptions to TypeSafe Jev as well as local recall. Run `+"`agent-memory listen on --jev --workspace %s`"+` only for this explicit form.
- "listen off": run `+"`agent-memory listen off --workspace %s`"+`.
- "listen status": run `+"`agent-memory listen status --workspace %s`"+`.
- "jev on": compatibility alias for enabling Jev on an already-running local listener; explain remote prompt egress, then run `+"`agent-memory listen jev-on --workspace %s`"+` only if explicitly requested.
- "jev off": compatibility alias for stopping only Jev advice; run `+"`agent-memory listen jev-off --workspace %s`"+`.
- For anything else, explain the supported forms; do not run a command.

Report the command result. Plain listen is local-only unless Jev was already separately enabled; listen off stops both. With Jev listening and a private Claude model catalog, Agent Memory may route Claude Code Agent subtask calls to Jev's selected model. It does not switch the main session, automatically load another skill, or authorize an action.
`, claudeSkillMarker, workspaceName, workspaceName, workspaceName, workspaceName, workspaceName, workspaceName)
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeClaudeSkill(root string) error {
	path := claudeSkillPath(root)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(content), claudeSkillMarker) {
		return nil
	}
	return os.Remove(path)
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
		command := fmt.Sprintf("agent-memory hook --event %s --agent claude-code --workspace %s # %s | %s", event, workspaceName, claudeHookMarker, workspace.MemoryContractMarker)
		timeout := 2
		if event == "UserPromptSubmit" {
			timeout = 7 // observe, local recall, and optional Jev each have deadlines
		} else if event == "PreToolUse" {
			timeout = 4 // Agent subtasks may make one bounded Jev decision
		}
		kept = append(kept, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": timeout}}})
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
