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
	var sourceMemoryIDs, sourceToolLessonIDs []string

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
				Workspace:           targetWorkspace,
				SkillName:           skillName,
				Description:         description,
				Force:               force,
				SourceMemoryIDs:     sourceMemoryIDs,
				SourceToolLessonIDs: sourceToolLessonIDs,
			})
			if err != nil {
				return err
			}

			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "distill", res)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Created immutable draft %s revision %d.\n", res.SkillName, res.RevisionNumber)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Draft: %s\n", res.SkillPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", res.CompatibilityMessage)
			return nil
		},
	}

	cmd.Flags().StringVar(&skillName, "name", "", "Name of the custom skill to create")
	cmd.Flags().StringVar(&description, "description", "", "Description of the custom skill")
	cmd.Flags().StringVarP(&targetWorkspace, "workspace", "w", "", "Workspace name to distill from (default: auto-detect)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Registry data directory (default: ~/.agent-memory)")
	cmd.Flags().BoolVar(&force, "force", false, "Compatibility flag; never overwrites the active skill and creates a draft")
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")
	cmd.Flags().StringSliceVar(&sourceMemoryIDs, "memory-id", nil, "Source memory ID for a focused skill seed (repeatable)")
	cmd.Flags().StringSliceVar(&sourceToolLessonIDs, "tool-lesson-id", nil, "Source tool lesson ID recorded in provenance (repeatable)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}
