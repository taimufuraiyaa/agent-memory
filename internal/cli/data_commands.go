package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

func newStudyCommand() *cobra.Command {
	var flags commonFlags
	var sources []string
	var depth string
	var dryRun bool
	var maxFiles int
	var ignore []string
	cmd := &cobra.Command{
		Use:   "study",
		Short: "Bootstrap memory by ingesting local files/directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			if cfg.apiURL != "" {
				return errors.New("study is only supported in in-process mode")
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			study := engine.NewStudyEngine(engine.NewWritePipelineWithEmbedder(store, provider))
			out, err := study.IngestWithOptions(ctx, engine.StudyOptions{
				Workspace: cfg.workspace,
				Sources:   sources,
				Depth:     depth,
				DryRun:    dryRun,
				MaxFiles:  maxFiles,
				Ignore:    ignore,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "study", out)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringArrayVar(&sources, "source", nil, "Source file/dir path (repeatable)")
	cmd.Flags().StringVar(&depth, "depth", "medium", "Extraction depth: shallow|medium|deep")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Walk and extract without writing memories")
	cmd.Flags().IntVar(&maxFiles, "max-files", 0, "Maximum study files to process (0 means no limit)")
	cmd.Flags().StringArrayVar(&ignore, "ignore", nil, "Glob pattern to ignore (repeatable)")
	return cmd
}

func newReconstructCommand() *cobra.Command {
	var flags commonFlags
	var query string
	var confirm bool
	cmd := &cobra.Command{
		Use:   "reconstruct",
		Short: "Attempt to reconstruct forgotten memory from tombstones",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			if cfg.apiURL != "" {
				var out any
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/reconstruct", map[string]any{"query": query, "confirm": confirm}, &out)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "reconstruct", out)
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			re := engine.NewReconstructionEngine(store, engine.NewWritePipelineWithEmbedder(store, provider))
			out, err := re.Reconstruct(ctx, cfg.workspace, query, confirm)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "reconstruct", out)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&query, "query", "", "Reconstruction query")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm a low-confidence reconstruction candidate")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func newExportCommand() *cobra.Command {
	var flags commonFlags
	var format string
	var outFile string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export workspace memories to json, markdown, or csv",
		Long: `Export all workspace memories to a file for backup, analysis, or migration.

Export formats:
  json     - Complete data with all fields (default)
  markdown - Human-readable grouped by memory type
  csv      - Tabular format for spreadsheet analysis (16 columns)

The export includes all memory metadata: content, type, confidence,
storage tier, access counts, decay scores, outcomes, and timestamps.`,
		Example: `  # Export to JSON (complete backup)
  agent-memory export --workspace my-project \
    --export-format json --out backup.json

  # Export to Markdown (human-readable)
  agent-memory export --workspace my-project \
    --export-format markdown --out memories.md

  # Export to CSV (for Excel/Google Sheets)
  agent-memory export --workspace my-project \
    --export-format csv --out data.csv

  # Print JSON to stdout
  agent-memory export --workspace my-project

  # Export from specific database file
  agent-memory export --workspace my-project \
    --db /path/to/memories.db --export-format json --out export.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			format = strings.ToLower(strings.TrimSpace(format))
			if format != "json" && format != "markdown" && format != "csv" {
				return errors.New("invalid export format: json|markdown|csv")
			}
			var payload any
			if cfg.apiURL != "" {
				endpoint := "/api/v1/memories/export?format=" + format
				if err := getAPI(ctx, cfg.apiURL, endpoint, &payload); err != nil {
					return err
				}
			} else {
				store, _, err := openDeps(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				memories, err := store.ListMemoriesByWorkspace(ctx, cfg.workspace)
				if err != nil {
					return err
				}
				if format == "markdown" {
					payload = map[string]any{"markdown": engine.BuildMarkdownExport(cfg.workspace, memories)}
				} else if format == "csv" {
					csvData, err := engine.BuildCSVExport(cfg.workspace, memories)
					if err != nil {
						return fmt.Errorf("failed to build CSV export: %w", err)
					}
					payload = map[string]any{"csv": csvData}
				} else {
					payload = engine.BuildExportBundle(cfg.workspace, memories)
				}
			}
			if strings.TrimSpace(outFile) == "" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "export", payload)
			}
			b, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return err
			}
			if format == "markdown" {
				if obj, ok := payload.(map[string]any); ok {
					if md, ok := obj["markdown"].(string); ok {
						b = []byte(md)
					}
				}
			} else if format == "csv" {
				if obj, ok := payload.(map[string]any); ok {
					if csvData, ok := obj["csv"].(string); ok {
						b = []byte(csvData)
					}
				}
			}
			if err := os.WriteFile(outFile, b, 0o644); err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "export", map[string]any{"file": outFile, "format": format})
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&format, "export-format", "json", "Export format: json|markdown|csv")
	cmd.Flags().StringVar(&outFile, "out", "", "Output file path (optional)")
	return cmd
}

func newImportCommand() *cobra.Command {
	var flags commonFlags
	var inFile string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import workspace memories from export JSON bundle",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if strings.TrimSpace(inFile) == "" {
				return errors.New("import file is required")
			}
			b, err := os.ReadFile(inFile)
			if err != nil {
				return err
			}
			var bundle engine.ExportBundle
			if err := json.Unmarshal(b, &bundle); err != nil {
				return err
			}
			var out any
			if cfg.apiURL != "" {
				if err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/import", bundle, &out); err != nil {
					return err
				}
			} else {
				store, _, err := openDeps(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				imported := 0
				for _, m := range bundle.Memories {
					mm := m
					if strings.TrimSpace(mm.Workspace) == "" {
						mm.Workspace = cfg.workspace
					}
					if err := store.UpsertMemory(ctx, &mm); err != nil {
						return err
					}
					imported++
				}
				out = map[string]any{"version": bundle.Version, "imported": imported}
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "import", out)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&inFile, "in", "", "Path to export JSON file")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}
