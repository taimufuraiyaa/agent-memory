package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCommand returns the base CLI command for agent-memory.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent-memory",
		Short: "Persistent memory layer for AI coding agents",
		Long:  "agent-memory is a local-first memory system for coding agents with CLI-first integration.",
	}

	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.PersistentFlags().Bool("pretty", false, "Pretty print JSON output")
	cmd.AddCommand(newWriteCommand())
	cmd.AddCommand(newSearchCommand())
	cmd.AddCommand(newRecallCommand())
	cmd.AddCommand(newSessionEndCommand())
	cmd.AddCommand(newConsolidateCommand())
	cmd.AddCommand(newStudyCommand())
	cmd.AddCommand(newReconstructCommand())
	cmd.AddCommand(newExportCommand())
	cmd.AddCommand(newImportCommand())
	cmd.AddCommand(newStatsCommand())
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newReinstallCommand())
	cmd.AddCommand(newRenameCommand())
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newDashboardCommand())
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newUpgradeCommand())
	return cmd
}
