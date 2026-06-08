package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// NewRootCommand returns the base CLI command for agent-memory.
func NewRootCommand() *cobra.Command {
	var toggleOn bool
	var toggleOff bool
	var runLabel string
	cmd := &cobra.Command{
		Use:   "agent-memory",
		Short: "Persistent memory layer for AI coding agents",
		Long:  "agent-memory is a local-first memory system for coding agents with CLI-first integration.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if toggleOn && toggleOff {
				return errors.New("only one of --toggle-on or --toggle-off can be set")
			}
			changed := false
			if toggleOn {
				_ = os.Setenv("AGENT_MEMORY_ENABLED", "1")
				changed = true
			}
			if toggleOff {
				_ = os.Setenv("AGENT_MEMORY_ENABLED", "0")
				changed = true
			}
			if strings.TrimSpace(runLabel) != "" {
				_ = os.Setenv("AGENT_MEMORY_RUN_LABEL", strings.TrimSpace(runLabel))
				changed = true
			}
			if !changed {
				return nil
			}

			envPath := filepath.Join(defaultAgentMemoryDataDir(), "agent-memory.env")
			if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
				return err
			}
			vars := map[string]string{}
			if toggleOn {
				vars["AGENT_MEMORY_ENABLED"] = "1"
			}
			if toggleOff {
				vars["AGENT_MEMORY_ENABLED"] = "0"
			}
			if strings.TrimSpace(runLabel) != "" {
				vars["AGENT_MEMORY_RUN_LABEL"] = strings.TrimSpace(runLabel)
			}
			updated, err := upsertEnvFile(envPath, vars)
			if err != nil {
				return err
			}
			if cmd.Parent() == nil && len(args) == 0 {
				if updated {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "updated: %s\n", envPath)
				} else {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ok: %s\n", envPath)
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if toggleOn || toggleOff || strings.TrimSpace(runLabel) != "" {
				return nil
			}
			return cmd.Help()
		},
	}

	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.PersistentFlags().Bool("pretty", false, "Pretty print JSON output")
	cmd.PersistentFlags().BoolVar(&toggleOn, "toggle-on", false, "Enable agent-memory globally (writes ~/.agent-memory/agent-memory.env)")
	cmd.PersistentFlags().BoolVar(&toggleOff, "toggle-off", false, "Disable agent-memory globally (writes ~/.agent-memory/agent-memory.env)")
	cmd.PersistentFlags().StringVar(&runLabel, "run-label", "", "Set AGENT_MEMORY_RUN_LABEL for token-metric grouping (writes env file)")
	cmd.AddCommand(newWriteCommand())
	cmd.AddCommand(newSearchCommand())
	cmd.AddCommand(newRecallCommand())
	cmd.AddCommand(newBenchmarkWorkerCommand())
	cmd.AddCommand(newReembedCommand())
	cmd.AddCommand(newFeedbackCommand())
	cmd.AddCommand(newSessionEndCommand())
	cmd.AddCommand(newConsolidateCommand())
	cmd.AddCommand(newStudyCommand())
	cmd.AddCommand(newReconstructCommand())
	cmd.AddCommand(newExportCommand())
	cmd.AddCommand(newImportCommand())
	cmd.AddCommand(newStatsCommand())
	cmd.AddCommand(newTuningCommand())
	cmd.AddCommand(newConfigCommand())
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newReinstallCommand())
	cmd.AddCommand(newRenameCommand())
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(newDashboardCommand())
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newUpgradeCommand())
	return cmd
}
