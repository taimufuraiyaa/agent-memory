package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/time/timebooks/agent-memory/internal/engine"
)

// newArchiveCommand returns the top-level `archive` command with subcommands
// for listing and restoring cold-tier archives.
func newArchiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Inspect and restore cold-tier archives of evicted memories",
	}
	cmd.AddCommand(newArchiveListCommand())
	cmd.AddCommand(newArchiveRestoreCommand())
	return cmd
}

// newArchiveListCommand lists archive IDs for a workspace.
func newArchiveListCommand() *cobra.Command {
	var flags commonFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memory IDs that have cold-tier archives",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if cfg.apiURL != "" {
				return errors.New("archive list is only supported in in-process mode (no --api)")
			}

			dataDir := filepath.Dir(cfg.dbPath)
			arch := engine.NewColdArchive(dataDir)
			ids, err := arch.ListIDs(cfg.workspace)
			if err != nil {
				return fmt.Errorf("list archives: %w", err)
			}

			if flags.format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{
					"workspace": cfg.workspace,
					"count":     len(ids),
					"ids":       ids,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Archives for workspace %q: %d\n", cfg.workspace, len(ids))
			for _, id := range ids {
				fmt.Fprintln(cmd.OutOrStdout(), " ", id)
			}
			return nil
		},
	}
	addCommonFlags(cmd, &flags)
	return cmd
}

// newArchiveRestoreCommand decompresses and prints a specific archive.
func newArchiveRestoreCommand() *cobra.Command {
	var flags commonFlags
	var memoryID string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Decompress and print the archived content of a memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if memoryID == "" && len(args) > 0 {
				memoryID = args[0]
			}
			if memoryID == "" {
				return errors.New("--id <memory-id> is required")
			}

			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if cfg.apiURL != "" {
				return errors.New("archive restore is only supported in in-process mode (no --api)")
			}

			dataDir := filepath.Dir(cfg.dbPath)
			arch := engine.NewColdArchive(dataDir)
			rec, err := arch.Load(cfg.workspace, memoryID)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no archive found for memory %q in workspace %q", memoryID, cfg.workspace)
				}
				return fmt.Errorf("restore archive: %w", err)
			}

			if flags.format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rec)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Memory ID:   %s\n", rec.MemoryID)
			fmt.Fprintf(cmd.OutOrStdout(), "Workspace:   %s\n", rec.Workspace)
			fmt.Fprintf(cmd.OutOrStdout(), "Type:        %s\n", rec.Type)
			fmt.Fprintf(cmd.OutOrStdout(), "Tier:        %s\n", rec.StorageTier)
			fmt.Fprintf(cmd.OutOrStdout(), "Confidence:  %.2f\n", rec.Confidence)
			fmt.Fprintf(cmd.OutOrStdout(), "Archived at: %s\n", rec.ArchivedAt.Format("2006-01-02 15:04:05 UTC"))
			if len(rec.Entities) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Entities:    %v\n", rec.Entities)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nContent:\n%s\n", rec.Content)
			return nil
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&memoryID, "id", "", "Memory ID to restore")
	return cmd
}
