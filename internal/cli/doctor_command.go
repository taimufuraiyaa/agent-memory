package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/doctor"
)

const defaultServiceURL = "http://127.0.0.1:3211"

func newDoctorCommand() *cobra.Command {
	var root, dataDir, workspaceName, serviceURL, modelDir, format string
	var repair, dryRun bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose agent-memory runtime and agent integration",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if root == "" {
				root, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			if dataDir == "" {
				dataDir = defaultAgentMemoryDataDir()
			}
			if workspaceName == "" {
				workspaceName = filepath.Base(root)
			}
			if modelDir == "" {
				modelDir = filepath.Join(dataDir, "models", "all-MiniLM-L6-v2")
			}
			loadedConfig, _ := config.Load(root)
			var connectorConfigs []config.ConnectorConfig
			if loadedConfig != nil {
				connectorConfigs = loadedConfig.Connectors
			}
			repaired := false
			planned := []string{}
			if repair {
				planned = append(planned, "ensure writable data directory: "+dataDir)
				if !dryRun {
					if err := os.MkdirAll(dataDir, 0o700); err != nil {
						return err
					}
					if err := os.Chmod(dataDir, 0o700); err != nil {
						return err
					}
					repaired = true
				}
			}
			results := doctor.NewRunner(doctor.DefaultChecks(doctor.Options{
				Root: root, DataDir: dataDir, Workspace: workspaceName, ServiceURL: serviceURL, ModelDir: modelDir, Connectors: connectorConfigs,
			})...).Run(cmd.Context())
			if strings.EqualFold(format, "json") {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "doctor", map[string]any{"checks": results, "repaired": repaired, "repairs_planned": planned, "dry_run": dryRun})
			}
			for _, result := range results {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s", result.Status, result.Name)
				if result.Evidence != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), ": %s", result.Evidence)
				}
				if result.Message != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), ": %s", result.Message)
				}
				if result.NextAction != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), " -> %s", result.NextAction)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Project root to inspect")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "agent-memory data directory")
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Workspace name")
	cmd.Flags().StringVar(&serviceURL, "service-url", defaultServiceURL, "Local service URL")
	cmd.Flags().StringVar(&modelDir, "model-dir", "", "Embedding model directory")
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text|json")
	cmd.Flags().BoolVar(&repair, "repair", false, "Apply only safe, scoped repairs")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview repairs without changing files")
	return cmd
}
