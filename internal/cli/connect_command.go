package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/integration"
)

func newConnectCommand(disconnect bool) *cobra.Command {
	var root, dataDir, workspaceName string
	var dryRun, force bool
	verb := "connect"
	if disconnect {
		verb = "disconnect"
	}
	cmd := &cobra.Command{
		Use:   verb + " <agent>",
		Short: strings.Title(verb) + " agent-memory configuration for a coding agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry, err := integration.NewDefaultRegistry()
			if err != nil {
				return err
			}
			if strings.TrimSpace(root) == "" {
				root, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			if strings.TrimSpace(dataDir) == "" {
				dataDir = defaultAgentMemoryDataDir()
			}
			if strings.TrimSpace(workspaceName) == "" {
				workspaceName = filepath.Base(root)
			}
			options := integration.Options{Root: root, DataDir: dataDir, Workspace: workspaceName, DryRun: dryRun, Force: force}
			if dryRun {
				var result integration.Result
				if disconnect {
					result, err = registry.Disconnect(cmd.Context(), args[0], options)
				} else {
					result, err = registry.Connect(cmd.Context(), args[0], options)
				}
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), verb, result)
			}
			backups, err := backupAgentFiles(args[0], root, dataDir)
			if err != nil {
				return err
			}
			var result integration.Result
			if disconnect {
				result, err = registry.Disconnect(cmd.Context(), args[0], options)
			} else {
				result, err = registry.Connect(cmd.Context(), args[0], options)
			}
			if err != nil {
				return err
			}
			result.Backups = backups
			return writeSuccessEnvelope(cmd.OutOrStdout(), verb, result)
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Project root to configure")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "agent-memory data directory")
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Workspace name")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview managed changes without writing")
	cmd.Flags().BoolVar(&force, "force", false, "Replace stale agent-memory managed entries")
	return cmd
}

func backupAgentFiles(agent, root, dataDir string) ([]string, error) {
	var paths []string
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "codex":
		paths = []string{filepath.Join(root, ".codex", "config.toml"), filepath.Join(root, ".codex", "hooks.json")}
	case "claude", "claude-code":
		paths = []string{filepath.Join(root, ".mcp.json"), filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, "CLAUDE.md")}
	case "cursor":
		paths = []string{filepath.Join(root, ".cursor", "rules", "agent-memory.mdc")}
	case "kiro":
		paths = []string{filepath.Join(root, ".kiro", "hooks", "memory-recall-gate.json"), filepath.Join(root, ".kiro", "hooks", "memory-consolidation-gate.json")}
	default:
		return nil, fmt.Errorf("unknown agent adapter: %s", agent)
	}
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backups := []string{}
	for _, source := range paths {
		data, err := os.ReadFile(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		name := strings.ReplaceAll(strings.TrimPrefix(source, root), string(filepath.Separator), "_")
		destination := filepath.Join(backupDir, strings.Trim(name, "_")+"-"+stamp+".bak")
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return nil, err
		}
		backups = append(backups, destination)
	}
	return backups, nil
}
