package cli

import (
	"context"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

// claudeAgentModelUpdate changes only the model of a consented Agent call.
// Returning nil leaves Claude Code's original tool input and permissions intact.
func claudeAgentModelUpdate(ctx context.Context, dataDir, workspaceName, cwd string, payload map[string]any, inputBytes int) map[string]any {
	if inputBytes > 32768 || stringValue(payload["tool_name"]) != "Agent" {
		return nil
	}
	input, ok := payload["tool_input"].(map[string]any)
	if !ok || strings.TrimSpace(stringValue(input["prompt"])) == "" {
		return nil
	}
	manager, err := workspace.NewManager(dataDir)
	if err != nil {
		return nil
	}
	project, err := manager.Project(workspaceName)
	if err != nil || !integration.NewClaudeListenStore(dataDir).AllowsJev(workspaceName, project.WorkspaceRoot, cwd) {
		return nil
	}
	model, ok := jevModelChoice(ctx, dataDir, "claude", stringValue(input["prompt"]), newJevClient())
	if !ok {
		return nil
	}
	updated := make(map[string]any, len(input)+1)
	for key, value := range input {
		updated[key] = value
	}
	updated["model"] = model
	return map[string]any{
		"systemMessage": "[Jev] Requested subagent model: " + model + ".",
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse",
			"updatedInput":  updated,
		},
	}
}
