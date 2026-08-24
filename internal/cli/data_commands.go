package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/portable"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
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
	var selectedSources []string
	var passphraseStdin bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export workspace data to json, markdown, csv, or encrypted portable format",
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
			if format != "json" && format != "markdown" && format != "csv" && format != "portable" {
				return errors.New("invalid export format: json|markdown|csv|portable")
			}
			if format == "portable" {
				if cfg.apiURL != "" {
					return errors.New("portable export is local-only; use the hosted export API for hosted data")
				}
				if strings.TrimSpace(outFile) == "" {
					return errors.New("portable export requires --out")
				}
				if !passphraseStdin {
					return errors.New("portable export requires --passphrase-stdin")
				}
				sources, err := parsePortableSourceSelections(selectedSources)
				if err != nil {
					return err
				}
				passphraseBytes, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1025))
				if err != nil {
					return fmt.Errorf("read portable passphrase: %w", err)
				}
				if len(passphraseBytes) > 1024 {
					return errors.New("portable passphrase exceeds 1024 bytes")
				}
				passphrase := strings.TrimSpace(string(passphraseBytes))
				store, err := openStore(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				bundle, err := portable.BuildLocal(ctx, store, portable.Selection{Workspace: cfg.workspace, SourceFiles: sources})
				if err != nil {
					return err
				}
				plain, err := json.Marshal(bundle)
				if err != nil {
					return err
				}
				sealed, err := exportservice.EncryptPortable(passphrase, plain)
				if err != nil {
					return err
				}
				if err := writePrivateFile(outFile, sealed); err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "export", map[string]any{"file": outFile, "format": format, "memories": len(bundle.Memories), "notes": len(bundle.Notes), "sources": len(bundle.Sources), "source_bytes_included": bundle.SourceBytesIncluded})
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
	cmd.Flags().StringVar(&format, "export-format", "json", "Export format: json|markdown|csv|portable")
	cmd.Flags().StringVar(&outFile, "out", "", "Output file path (optional)")
	cmd.Flags().StringArrayVar(&selectedSources, "include-source", nil, "Portable-only explicit source selection: source-asset-id=/path/to/original (repeatable)")
	cmd.Flags().BoolVar(&passphraseStdin, "passphrase-stdin", false, "Read the portable bundle passphrase from stdin")
	return cmd
}

func parsePortableSourceSelections(values []string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, value := range values {
		id, path, ok := strings.Cut(value, "=")
		id, path = strings.TrimSpace(id), strings.TrimSpace(path)
		if !ok || id == "" || path == "" {
			return nil, fmt.Errorf("invalid --include-source %q; expected source-asset-id=/path/to/original", value)
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("source %q was selected more than once", id)
		}
		result[id] = path
	}
	return result, nil
}

func writePrivateFile(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
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

				filter := engine.NewRegexSecurityFilter()
				imported := 0
				skipped := make([]map[string]any, 0)
				failed := make([]map[string]any, 0)

				for _, m := range bundle.Memories {
					mm := m
					if strings.TrimSpace(mm.Workspace) == "" {
						mm.Workspace = cfg.workspace
					}
					if reason := api.SanitizeImportedMemory(ctx, &mm, filter); reason != "" {
						skipped = append(skipped, map[string]any{
							"id":     mm.ID,
							"reason": reason,
						})
						continue
					}
					if err := store.UpsertMemory(ctx, &mm); err != nil {
						failed = append(failed, map[string]any{
							"id":     mm.ID,
							"reason": err.Error(),
						})
						continue
					}
					imported++
				}

				auditOutcome := "success"
				if imported == 0 && len(bundle.Memories) > 0 {
					auditOutcome = "failure"
				}
				_, _ = store.AppendAuditEvent(ctx, sqlite.AuditEventInput{
					Workspace:   cfg.workspace,
					Operation:   "import",
					Outcome:     auditOutcome,
					Actor:       "cli",
					Source:      "bundle",
					TargetType:  "memory",
					TargetCount: imported,
					Reason:      "memory bundle import",
				})

				out = map[string]any{
					"version":  bundle.Version,
					"imported": imported,
					"skipped":  skipped,
					"failed":   failed,
				}

				// Exit non-zero only when ALL items failed (and there were items)
				if imported == 0 && len(skipped)+len(failed) == len(bundle.Memories) && len(bundle.Memories) > 0 {
					return fmt.Errorf("import failed: all %d items rejected", len(bundle.Memories))
				}
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "import", out)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&inFile, "in", "", "Path to export JSON file")
	_ = cmd.MarkFlagRequired("in")
	return cmd
}
