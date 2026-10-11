package cli

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func newModelChoiceCommand() *cobra.Command {
	var workspaceName, dataDir, host string
	var allowJev bool
	cmd := &cobra.Command{
		Use:   "model-choice",
		Short: "Get a Jev model recommendation for one configured host; read task from stdin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !allowJev {
				return errors.New("model choice requires explicit --allow-jev approval for task egress")
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
				return errors.New("run model choice from the registered project")
			}
			content, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4001))
			if err != nil || len(content) > 4000 || strings.TrimSpace(string(content)) == "" {
				return errors.New("model choice requires a bounded task on stdin")
			}
			modelID, ok := jevModelChoice(cmd.Context(), dataDir, host, string(content), newJevClient())
			if !ok {
				return errors.New("no verified Jev model recommendation is available")
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "model-choice", map[string]any{
				"workspace": workspaceName, "host": host, "model": modelID,
				"advisory": true, "model_switched": false,
			})
		},
	}
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Registered workspace name")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Agent Memory data directory")
	cmd.Flags().StringVar(&host, "host", "", "Model host: claude, chatgpt, or openai_api")
	cmd.Flags().BoolVar(&allowJev, "allow-jev", false, "Explicitly permit sending the bounded task and host candidates to TypeSafe Jev")
	return cmd
}
