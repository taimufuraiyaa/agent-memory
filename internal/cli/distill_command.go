package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func newDistillCommand() *cobra.Command {
	var skillName string
	var description string
	var targetWorkspace string
	var dataDir string
	var force bool
	var format string

	cmd := &cobra.Command{
		Use:   "distill",
		Short: "Distill workspace memories into a custom agent skill",
		Long: `Query the workspace memory database and format procedural, semantic, and successful outcome
memories into an Antigravity-compatible Custom Agent Skill (.agents/skills/<name>/SKILL.md).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := validateTextOrJSONFormat(format)
			if err != nil {
				return err
			}

			if skillName == "" {
				return fmt.Errorf("skill name is required (use --name)")
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			if dataDir == "" {
				dataDir = defaultAgentMemoryDataDir()
			}

			mgr, err := workspace.NewManager(dataDir)
			if err != nil {
				return err
			}

			res, err := mgr.Distill(cmd.Context(), cwd, workspace.DistillOptions{
				Workspace:   targetWorkspace,
				SkillName:   skillName,
				Description: description,
				Force:       force,
			})
			if err != nil {
				return err
			}

			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "distill", res)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Distilled skill %s successfully!\n", res.SkillName)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Written to: %s\n", res.SkillPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&skillName, "name", "", "Name of the custom skill to create")
	cmd.Flags().StringVar(&description, "description", "", "Description of the custom skill")
	cmd.Flags().StringVarP(&targetWorkspace, "workspace", "w", "", "Workspace name to distill from (default: auto-detect)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Registry data directory (default: ~/.agent-memory)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite the skill files if they already exist")
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}
