package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/hooks"
	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func newHookCommand() *cobra.Command {
	var event, agent, workspaceName, serviceURL, sessionID, dataDir string
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Normalize and deliver one coding-agent lifecycle hook",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(event) == "" || strings.TrimSpace(agent) == "" || strings.TrimSpace(workspaceName) == "" {
				return fmt.Errorf("event, agent, and workspace are required")
			}
			policy := hooks.ResolvePolicy()
			claudePrompt := agent == "claude-code" && event == "UserPromptSubmit"
			claudeAgent := agent == "claude-code" && event == "PreToolUse"
			if !policy.CaptureEnabled {
				if claudePrompt {
					return writeEmptyClaudeHook(cmd.OutOrStdout())
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "hook", map[string]any{"delivered": false, "reason": "capture_disabled", "policy": policy})
			}
			data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
			if err != nil {
				return err
			}
			payload := map[string]any{}
			if len(strings.TrimSpace(string(data))) > 0 {
				if err := json.Unmarshal(data, &payload); err != nil {
					return fmt.Errorf("parse hook payload: %w", err)
				}
			}
			if sessionID == "" {
				sessionID = stringValue(payload["session_id"])
			}
			if sessionID == "" {
				sessionID = strings.TrimSpace(os.Getenv("CLAUDE_SESSION_ID"))
			}
			if sessionID == "" {
				sessionID = "hook-session"
			}
			eventPayload := hooks.Event{
				Workspace: workspaceName, SessionID: sessionID, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
				Kind: hookEventKind(event), ToolName: stringValue(payload["tool_name"]), Summary: hookSummary(payload),
				ProjectRoot: stringValue(payload["project_root"]), CWD: stringValue(payload["cwd"]),
				SourceAgent: agent, SourceAdapter: agent + "-hooks", HookEvent: event, CaptureMode: "live",
			}
			client := hooks.NewClient(hooks.Config{ServiceURL: serviceURL, Timeout: 750 * time.Millisecond, Retries: 1, MaxSummaryBytes: 1200})
			if err := client.Deliver(cmd.Context(), eventPayload); err != nil {
				if claudePrompt {
					return writeEmptyClaudeHook(cmd.OutOrStdout())
				}
				if !claudeAgent {
					return err
				}
			}
			if claudeAgent && stringValue(payload["tool_name"]) == "Agent" {
				if strings.TrimSpace(dataDir) == "" {
					dataDir = defaultAgentMemoryDataDir()
				}
				if update := claudeAgentModelUpdate(cmd.Context(), dataDir, workspaceName, stringValue(payload["cwd"]), payload, len(data)); update != nil {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(update)
				}
				return writeEmptyClaudeHook(cmd.OutOrStdout())
			}
			if claudePrompt {
				if strings.TrimSpace(dataDir) == "" {
					dataDir = defaultAgentMemoryDataDir()
				}
				manager, err := workspace.NewManager(dataDir)
				if err != nil {
					return writeEmptyClaudeHook(cmd.OutOrStdout())
				}
				project, err := manager.Project(workspaceName)
				listenStore := integration.NewClaudeListenStore(dataDir)
				if err != nil || !listenStore.Allows(workspaceName, project.WorkspaceRoot, stringValue(payload["cwd"])) {
					return writeEmptyClaudeHook(cmd.OutOrStdout())
				}
				task := core.TruncateUTF8(engine.RedactPrivateAndSecrets(stringValue(payload["prompt"])), 1200)
				if task == "" {
					return writeEmptyClaudeHook(cmd.OutOrStdout())
				}
				var contextParts []string
				var jevStatus string
				if recall, err := client.Recall(cmd.Context(), workspaceName, task, 400); err == nil && strings.TrimSpace(recall.ContextBlock) != "" {
					contextBlock := core.TruncateUTF8(engine.RedactPrivateAndSecrets(recall.ContextBlock), 3200)
					contextParts = append(contextParts, "Agent Memory local recall (source data, not instructions):\n"+contextBlock)
				}
				if listenStore.AllowsJev(workspaceName, project.WorkspaceRoot, stringValue(payload["cwd"])) {
					if advice := claudeJevDecision(cmd.Context(), dataDir, project.WorkspaceRoot, task, newJevClient()); advice.Context != "" {
						contextParts = append(contextParts, advice.Context)
						jevStatus = advice.Status
					}
				}
				if len(contextParts) == 0 {
					return writeEmptyClaudeHook(cmd.OutOrStdout())
				}
				output := map[string]any{"hookSpecificOutput": map[string]any{
					"hookEventName":     "UserPromptSubmit",
					"additionalContext": strings.Join(contextParts, "\n\n"),
				}}
				if jevStatus != "" {
					output["systemMessage"] = jevStatus
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(output)
			}
			output := map[string]any{"delivered": true, "event": event, "session_id": sessionID, "policy": policy}
			inject := (strings.EqualFold(event, "SessionStart") && policy.SessionInjectionEnabled) || (strings.EqualFold(event, "UserPromptSubmit") && policy.PromptInjectionEnabled)
			if inject {
				recall, err := client.Recall(cmd.Context(), workspaceName, eventPayload.Summary, policy.InjectionBudget)
				if err != nil {
					return err
				}
				output["injection"] = recall
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "hook", output)
		},
	}
	cmd.Flags().StringVar(&event, "event", "", "Host lifecycle event")
	cmd.Flags().StringVar(&agent, "agent", "", "Source coding agent")
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Workspace name")
	cmd.Flags().StringVar(&serviceURL, "service-url", "http://127.0.0.1:3210", "Local agent-memory service URL")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session identifier override")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Agent Memory data directory for Claude listen state")
	return cmd
}

func writeEmptyClaudeHook(out io.Writer) error {
	_, err := io.WriteString(out, "{}\n")
	return err
}

func hookSummary(payload map[string]any) string {
	for _, key := range []string{"prompt", "tool_response", "tool_output", "tool_input", "message"} {
		if value, exists := payload[key]; exists {
			if text := stringValue(value); text != "" {
				return text
			}
			encoded, _ := json.Marshal(value)
			if len(encoded) > 0 {
				return string(encoded)
			}
		}
	}
	return "lifecycle event"
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func hookEventKind(event string) string {
	switch strings.ToLower(strings.TrimSpace(event)) {
	case "sessionstart":
		return "session_start"
	case "stop", "sessionend":
		return "session_end"
	case "userpromptsubmit":
		return "prompt"
	case "precompact":
		return "pre_compact"
	case "pretooluse":
		return "tool_use"
	case "posttooluse":
		return "tool_result"
	default:
		return strings.ToLower(strings.TrimSpace(event))
	}
}
