package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/openaiapi"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type openAIModelClient interface {
	ProbeModel(context.Context, string, string) error
	Generate(context.Context, string, string, string, int) (openaiapi.Result, error)
}

var newOpenAIClient = func() openAIModelClient { return openaiapi.NewClient() }

func newModelRunCommand() *cobra.Command {
	var workspaceName, dataDir string
	var allowJev, allowAPI bool
	var maxOutputTokens int
	cmd := &cobra.Command{
		Use:   "model-run",
		Short: "Route one bounded text task through Jev to the OpenAI Responses API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !allowJev || !allowAPI {
				return errors.New("model run requires both --allow-jev and --allow-openai-api")
			}
			apiKey := os.Getenv("OPENAI_API_KEY")
			if apiKey == "" {
				return errors.New("OPENAI_API_KEY is not configured")
			}
			if maxOutputTokens < 32 || maxOutputTokens > 1024 {
				return errors.New("max-output-tokens must be 32..1024")
			}
			if dataDir == "" {
				dataDir = defaultAgentMemoryDataDir()
			}
			manager, err := workspace.NewManager(dataDir)
			if err != nil {
				return err
			}
			project, err := manager.Project(workspaceName)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil || !integration.ProjectDirectoryWithin(project.WorkspaceRoot, cwd) {
				return errors.New("run model task from the registered project")
			}
			content, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4001))
			if err != nil || len(content) > 4000 {
				return errors.New("task must be at most 4000 bytes")
			}
			task := core.TruncateUTF8(engine.RedactPrivateAndSecrets(string(content)), 4000)
			if strings.TrimSpace(task) == "" {
				return errors.New("task is empty after redaction")
			}
			selected, ok := jevModelChoice(cmd.Context(), dataDir, "openai_api", task, newJevClient())
			if !ok {
				return errors.New("no verified Jev OpenAI API model choice is available")
			}
			client := newOpenAIClient()
			if err := client.ProbeModel(cmd.Context(), apiKey, selected); err != nil {
				return errors.New("selected OpenAI API model is unavailable")
			}
			result, err := client.Generate(cmd.Context(), apiKey, selected, task, maxOutputTokens)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "model-run", map[string]any{
				"workspace": workspaceName, "host": "openai_api", "model": result.Model,
				"text": result.Text, "input_tokens": result.InputTokens, "output_tokens": result.OutputTokens,
				"chatgpt_app_switched": false, "tools_executed": false,
			})
		},
	}
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Registered workspace name")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Agent Memory data directory")
	cmd.Flags().BoolVar(&allowJev, "allow-jev", false, "Permit task and model-candidate egress to TypeSafe Jev")
	cmd.Flags().BoolVar(&allowAPI, "allow-openai-api", false, "Permit a separate billable OpenAI API request")
	cmd.Flags().IntVar(&maxOutputTokens, "max-output-tokens", 512, "Bound OpenAI output tokens (32..1024)")
	return cmd
}
