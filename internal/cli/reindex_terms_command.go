package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
)

func newReindexTermsCommand() *cobra.Command {
	var flags commonFlags
	var targetFPP float64
	var pageSize int
	var statusOnly bool
	cmd := &cobra.Command{
		Use:   "reindex-terms",
		Short: "Backfill exact locator terms and rebuild the project Bloom filter",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			if targetFPP <= 0 || targetFPP >= 1 {
				return errors.New("target-fpp must be between 0 and 1")
			}
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			store, err := openStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			service := application.NewMemoryService(store, nil, nil)
			if statusOnly {
				status, err := service.TermIndexStatus(cmd.Context(), cfg.workspace)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "reindex-terms", status)
			}
			report, err := service.RebuildTermIndex(cmd.Context(), application.RebuildTermIndexOptions{
				Workspace: cfg.workspace,
				TargetFPP: targetFPP,
				PageSize:  pageSize,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "reindex-terms", report)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().Float64Var(&targetFPP, "target-fpp", 0.01, "Target Bloom false-positive probability")
	cmd.Flags().IntVar(&pageSize, "page-size", 200, "Backfill and rebuild page size")
	cmd.Flags().BoolVar(&statusOnly, "status", false, "Inspect project term-index health without rebuilding")
	return cmd
}
