package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type lifecycleFlags struct {
	format  string
	baseDir string
}

func addLifecycleFlags(cmd *cobra.Command, f *lifecycleFlags) {
	cmd.Flags().StringVarP(&f.format, "format", "f", formatJSON, "Output format: json")
	cmd.Flags().StringVar(&f.baseDir, "base-dir", "", "Workspace registry base directory (default ~/.agent-memory)")
}

func newInitCommand() *cobra.Command {
	var f lifecycleFlags
	var projectName, rulePath string
	var ides []string
	var study, reuse, force, noRule bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize project workspace and registry entry",
		Aliases: []string{
			"i",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(f.format, false); err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			mgr, err := workspace.NewManager(f.baseDir)
			if err != nil {
				return err
			}
			out, err := mgr.Init(cmd.Context(), workspace.InitOptions{
				CWD:         cwd,
				ProjectName: projectName,
				Study:       study,
				Reuse:       reuse,
				Force:       force,
				NoRule:      noRule,
				RulePath:    rulePath,
				IDEs:        ides,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "init", out)
		},
	}
	addLifecycleFlags(cmd, &f)
	cmd.Flags().StringVarP(&projectName, "project-name", "n", "", "Project name (default: sanitized cwd basename)")
	cmd.Flags().BoolVar(&study, "study", false, "Run bootstrap study on standard local sources")
	cmd.Flags().BoolVar(&reuse, "reuse", false, "Reuse existing project if it already exists")
	cmd.Flags().BoolVar(&force, "force", false, "Archive and recreate project if it already exists")
	cmd.Flags().BoolVar(&noRule, "no-rule", false, "Do not write IDE rule files")
	cmd.Flags().StringVar(&rulePath, "rule-path", "", "Override Cursor rule output path (cursor only)")
	cmd.Flags().StringSliceVar(&ides, "ide", nil, "IDE rule targets (repeatable, default: auto-detect present IDE files, else cursor): cursor|antigravity|claude|zcode|aierules|cursorrules|trae|windsurfrules|generic|all")
	return cmd
}

func newReinstallCommand() *cobra.Command {
	var f lifecycleFlags
	var projectName string
	var ides []string
	var force bool
	cmd := &cobra.Command{
		Use:   "reinstall",
		Short: "Reinstall project agent files without changing DB or project name",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(f.format, false); err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			mgr, err := workspace.NewManager(f.baseDir)
			if err != nil {
				return err
			}
			out, err := mgr.Reinstall(cmd.Context(), workspace.ReinstallOptions{
				CWD:         cwd,
				ProjectName: projectName,
				Force:       force,
				IDEs:        ides,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "reinstall", out)
		},
	}
	addLifecycleFlags(cmd, &f)
	cmd.Flags().StringVarP(&projectName, "project-name", "n", "", "Project name (optional: auto-detect from cwd rule)")
	cmd.Flags().StringSliceVar(&ides, "ide", nil, "IDE rule targets to write during reinstall (repeatable): cursor|antigravity|claude|zcode|aierules|cursorrules|trae|windsurfrules|generic|all")
	cmd.Flags().BoolVar(&force, "force", true, "Overwrite IDE hook/rule files even if already present")
	return cmd
}

func newRenameCommand() *cobra.Command {
	var f lifecycleFlags
	var from, to string
	cmd := &cobra.Command{
		Use:   "rename",
		Short: "Rename an existing project workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(f.format, false); err != nil {
				return err
			}
			if strings.TrimSpace(to) == "" {
				return fmt.Errorf("to is required")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			mgr, err := workspace.NewManager(f.baseDir)
			if err != nil {
				return err
			}
			out, err := mgr.Rename(context.Background(), workspace.RenameOptions{
				CWD:  cwd,
				From: from,
				To:   to,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "rename", out)
		},
	}
	addLifecycleFlags(cmd, &f)
	cmd.Flags().StringVar(&from, "from", "", "Current project name (optional: auto-detect from cwd rule)")
	cmd.Flags().StringVar(&to, "to", "", "New project name")
	return cmd
}

func newListCommand() *cobra.Command {
	var f lifecycleFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.EqualFold(f.format, "text") {
				mgr, err := workspace.NewManager(f.baseDir)
				if err != nil {
					return err
				}
				rows, err := mgr.List(cmd.Context())
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "PROJECT\tSIZE\tMEMORIES\tDB")
				for _, r := range rows {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\t%s\n", r.Name, formatBytes(r.SizeBytes), r.MemoryCount, r.DBPath)
				}
				return nil
			}
			if err := validateOutputFormat(f.format, false); err != nil {
				return err
			}
			mgr, err := workspace.NewManager(f.baseDir)
			if err != nil {
				return err
			}
			rows, err := mgr.List(cmd.Context())
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "list", map[string]any{"projects": rows})
		},
	}
	addLifecycleFlags(cmd, &f)
	return cmd
}

func newDeleteCommand() *cobra.Command {
	var f lifecycleFlags
	var projectName string
	var keepData, yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a registered project workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(f.format, false); err != nil {
				return err
			}
			mgr, err := workspace.NewManager(f.baseDir)
			if err != nil {
				return err
			}
			out, err := mgr.Delete(cmd.Context(), workspace.DeleteOptions{
				ProjectName: projectName,
				KeepData:    keepData,
				Yes:         yes,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "delete", out)
		},
	}
	addLifecycleFlags(cmd, &f)
	cmd.Flags().StringVarP(&projectName, "project-name", "n", "", "Project name")
	cmd.Flags().BoolVar(&keepData, "keep-data", false, "Archive DB data instead of deleting")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("project-name")
	return cmd
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	value := float64(bytes)
	index := 0
	for value >= 1024 && index < len(units)-1 {
		value /= 1024
		index++
	}
	if value >= 100 || index == 0 {
		return fmt.Sprintf("%.0f %s", value, units[index])
	}
	return fmt.Sprintf("%.1f %s", value, units[index])
}
