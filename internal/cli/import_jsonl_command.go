package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/replay"
)

func newImportJSONLCommand() *cobra.Command {
	var flags commonFlags
	cmd := &cobra.Command{
		Use:   "import-jsonl <path>",
		Short: "Import a sanitized replay timeline from JSONL transcripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if strings.TrimSpace(args[0]) == "" {
				return errors.New("import path is required")
			}
			if cfg.apiURL != "" {
				var output any
				if err := postAPI(cmd.Context(), cfg.apiURL, "/api/v1/replay/import-jsonl", map[string]any{"workspace": cfg.workspace, "path": args[0]}, &output); err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "import-jsonl", output)
			}
			store, err := openStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := replay.NewImporter(store).Import(cmd.Context(), replay.ImportOptions{Workspace: cfg.workspace, Path: args[0]})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "import-jsonl", result)
		},
	}
	addCommonFlags(cmd, &flags)
	return cmd
}
