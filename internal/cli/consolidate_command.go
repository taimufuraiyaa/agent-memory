package cli

import (
	"github.com/spf13/cobra"

	"github.com/time/timebooks/agent-memory/internal/engine"
)

func newConsolidateCommand() *cobra.Command {
	var flags commonFlags
	var deep bool
	var days int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "consolidate",
		Short: "Run memory consolidation (REM cycle)",
		Long: `Run the memory consolidation cycle.

Without --deep: runs the standard within-session REM cycle (decay, cluster, merge, evict, promote).
With --deep: runs a cross-session pass that finds patterns across multiple sessions — repeated failures
become procedural rules, large episodic clusters merge into semantic facts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			if !engine.MemoryEnabled() {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "consolidate", map[string]any{
					"skipped": true,
					"reason":  "disabled",
				})
			}

			if cfg.apiURL != "" {
				var out any
				err := postAPI(ctx, cfg.apiURL, "/api/v1/consolidation/run", map[string]any{
					"deep":    deep,
					"days":    days,
					"dry_run": dryRun,
				}, &out)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "consolidate", out)
			}

			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			pipeline := engine.NewWritePipelineWithEmbedder(store, provider)

			if deep {
				dc := engine.NewDeepConsolidationEngine(store, pipeline)
				result, err := dc.Run(ctx, engine.DeepConsolidationOptions{
					Workspace: cfg.workspace,
					DaysBack:  days,
					DryRun:    dryRun,
					Mode:      engine.MergeFast,
				})
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "consolidate", result)
			}

			// Standard REM cycle.
			ce := engine.NewConsolidationEngine(store, pipeline)
			merged, err := ce.Run(ctx, cfg.workspace, engine.MergeFast)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "consolidate", map[string]any{
				"merged":     len(merged),
				"merged_ids": merged,
				"deep":       false,
				"dry_run":    dryRun,
			})
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().BoolVar(&deep, "deep", false, "Run cross-session deep consolidation")
	cmd.Flags().IntVar(&days, "days", 30, "Lookback window in days (--deep only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would happen without writing")
	return cmd
}
