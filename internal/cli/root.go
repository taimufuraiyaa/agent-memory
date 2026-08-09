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
			// Load ~/.agent-memory/agent-memory.env into the process environment
			// before any engine configuration is resolved, so persisted toggles
			// (--toggle-on/off, --run-label) take effect on later invocations.
			// Precedence: command-line flags > env file > process environment,
			// so the flags below are applied after this load.
			loadEnvFile(cmd)

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
	cmd.AddCommand(newReindexTermsCommand())
	cmd.AddCommand(newFeedbackCommand())
	cmd.AddCommand(newSessionEndCommand())
	cmd.AddCommand(newConsolidateCommand())
	cmd.AddCommand(newStudyCommand())
	cmd.AddCommand(newReconstructCommand())
	cmd.AddCommand(newExportCommand())
	cmd.AddCommand(newImportCommand())
	cmd.AddCommand(newStatsCommand())
	cmd.AddCommand(newAdvisorCommand())
	cmd.AddCommand(newTuningCommand())
	cmd.AddCommand(newConfigCommand())
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newReinstallCommand())
	cmd.AddCommand(newRenameCommand())
	cmd.AddCommand(newListCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newArchiveCommand())
	cmd.AddCommand(newServeCommand())
	cmd.AddCommand(newDashboardCommand())
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newUpgradeCommand())
	cmd.AddCommand(newInstallCommand())
	cmd.AddCommand(newDistillCommand())
	cmd.AddCommand(newConnectCommand(false))
	cmd.AddCommand(newConnectCommand(true))
	cmd.AddCommand(newDoctorCommand())
	cmd.AddCommand(newDemoCommand())
	cmd.AddCommand(newHookCommand())
	cmd.AddCommand(newAuditCommand())
	cmd.AddCommand(newImportJSONLCommand())
	cmd.AddCommand(newHostedCommand())
	return cmd
}

// loadEnvFile reads KEY=VALUE assignments from ~/.agent-memory/agent-memory.env
// and applies them to the process environment. It is called during CLI startup,
// before engine configuration is resolved, so that persisted toggles affect
// every invocation even when the shell rc-autoload is not installed. Precedence
// is flags > env file > process environment: flags (--toggle-on/off,
// --run-label) are applied by the caller after this load. Missing files are
// ignored; malformed lines are skipped with a warning to stderr and do not
// abort the run.
func loadEnvFile(cmd *cobra.Command) {
	envPath := filepath.Join(defaultAgentMemoryDataDir(), "agent-memory.env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: cannot read %s: %v\n", envPath, err)
		}
		return
	}
	malformed := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		k, v, ok := parseEnvAssignmentLine(line)
		if !ok {
			if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				malformed++
			}
			continue
		}
		_ = os.Setenv(k, v)
	}
	if malformed > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %d malformed line(s) ignored\n", envPath, malformed)
	}
}
