package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/time/timebooks/agent-memory/internal/api"
	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type commonFlags struct {
	dbPath    string
	workspace string
	modelDir  string
	format    string
	apiURL    string
}

func addCommonFlags(cmd *cobra.Command, f *commonFlags) {
	cmd.Flags().StringVar(&f.dbPath, "db", "", "Path to SQLite database file")
	cmd.Flags().StringVarP(&f.workspace, "workspace", "w", "", "Workspace name")
	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&f.modelDir, "model-dir", embeddings.DefaultModelDir(home), "Path to local embedding model directory")
	cmd.Flags().StringVarP(&f.format, "format", "f", formatJSON, "Output format: json|raw")
	cmd.Flags().StringVar(&f.apiURL, "api", "", "HTTP API base URL (overrides in-process mode)")
}

func openDeps(ctx context.Context, cfg runtimeConfig) (*sqlite.Store, embeddings.Provider, error) {
	if strings.TrimSpace(cfg.dbPath) == "" || strings.TrimSpace(cfg.workspace) == "" {
		return nil, nil, errors.New("db path and workspace are required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
		return nil, nil, err
	}
	store, err := sqlite.Open(ctx, cfg.dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(cfg.modelDir, 0o755); err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	provider, err := embeddings.NewLocalProvider(cfg.modelDir)
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	return store, provider, nil
}

func newWriteCommand() *cobra.Command {
	var flags commonFlags
	var mType, content string
	cmd := &cobra.Command{
		Use:   "write",
		Short: "Write one memory entry",
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
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/write", map[string]any{
					"type":    mType,
					"content": content,
				}, &out)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "write", out)
			}
			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			p := engine.NewWritePipeline(store)
			mt := core.MemoryType(mType)
			res, err := p.Write(ctx, engine.WriteInput{
				Workspace: cfg.workspace,
				Type:      mt,
				Content:   content,
				Source:    core.MemorySource{Type: core.SourceUserInput},
				Mode:      engine.ExtractFast,
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "write", res)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&mType, "type", "semantic", "Memory type: episodic|semantic|procedural|outcome")
	cmd.Flags().StringVar(&content, "content", "", "Memory content")
	_ = cmd.MarkFlagRequired("content")
	return cmd
}

func newSearchCommand() *cobra.Command {
	var flags commonFlags
	var query string
	var topK int
	var mode string
	var explain bool
	var tiers []string
	var types []string
	var outcomeResult string
	var minConfidence float64
	var minDecayScore float64
	var entities []string
	var from, to string
	var tokenBudget int
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Semantic multi-signal search",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			typed := make([]core.MemoryType, 0, len(types))
			for _, v := range types {
				tv := core.MemoryType(strings.ToLower(strings.TrimSpace(v)))
				if tv == "" {
					continue
				}
				if !core.IsMemoryType(tv) {
					return fmt.Errorf("invalid memory type: %s", v)
				}
				typed = append(typed, tv)
			}
			tiered := make([]core.StorageTier, 0, len(tiers))
			for _, v := range tiers {
				tv := core.StorageTier(strings.ToLower(strings.TrimSpace(v)))
				if tv == "" {
					continue
				}
				if !core.IsStorageTier(tv) {
					return fmt.Errorf("invalid storage tier: %s", v)
				}
				tiered = append(tiered, tv)
			}
			var outcome *core.OutcomeResult
			if strings.TrimSpace(outcomeResult) != "" {
				v := core.OutcomeResult(strings.ToLower(strings.TrimSpace(outcomeResult)))
				switch v {
				case core.OutcomeSuccess, core.OutcomeFailure, core.OutcomePartial:
				default:
					return fmt.Errorf("invalid outcome result: %s", outcomeResult)
				}
				outcome = &v
			}
			var minC *float64
			if cmd.Flags().Changed("min-confidence") {
				if minConfidence < 0 || minConfidence > 1 {
					return fmt.Errorf("min-confidence must be between 0 and 1")
				}
				v := minConfidence
				minC = &v
			}
			var minD *float64
			if cmd.Flags().Changed("min-decay-score") {
				if minDecayScore < 0 || minDecayScore > 1 {
					return fmt.Errorf("min-decay-score must be between 0 and 1")
				}
				v := minDecayScore
				minD = &v
			}
			var dateFrom, dateTo *time.Time
			if strings.TrimSpace(from) != "" {
				t, ok := parseTimeFlexibleCLI(from)
				if !ok {
					return fmt.Errorf("invalid from date: %s", from)
				}
				dateFrom = &t
			}
			if strings.TrimSpace(to) != "" {
				t, ok := parseTimeFlexibleCLI(to)
				if !ok {
					return fmt.Errorf("invalid to date: %s", to)
				}
				dateTo = &t
			}
			if cfg.apiURL != "" {
				filters := map[string]any{}
				if len(typed) > 0 {
					filters["type"] = typed
				}
				if len(tiered) > 0 {
					filters["tiers"] = tiered
				}
				if outcome != nil {
					filters["outcome_result"] = string(*outcome)
				}
				if minC != nil {
					filters["min_confidence"] = *minC
				}
				if minD != nil {
					filters["min_decay_score"] = *minD
				}
				if len(entities) > 0 {
					filters["entities"] = entities
				}
				if strings.TrimSpace(from) != "" || strings.TrimSpace(to) != "" {
					filters["date_range"] = map[string]any{"from": from, "to": to}
				}
				body := map[string]any{
					"query":        query,
					"workspace":    cfg.workspace,
					"top_k":        topK,
					"token_budget": tokenBudget,
					"mode":         mode,
					"explain":      explain,
				}
				if len(filters) > 0 {
					body["filters"] = filters
				}
				var out any
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/search", body, &out)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "search", out)
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			searcher := engine.NewVectorSearcher(store, provider)
			retrieval := engine.NewRetrievalEngine(searcher)
			res, err := retrieval.Retrieve(ctx, engine.RetrievalOptions{
				Workspace: cfg.workspace,
				Query:     query,
				TopK:      topK,
				Mode:      engine.RetrievalMode(mode),
				Filters: engine.RetrievalFilters{
					Types:         typed,
					Tiers:         tiered,
					OutcomeResult: outcome,
					MinConfidence: minC,
					MinDecayScore: minD,
					Entities:      entities,
					DateFrom:      dateFrom,
					DateTo:        dateTo,
				},
			})
			if err != nil {
				return err
			}
			_ = store.AddTokenMetric(ctx, cfg.workspace, "search", sumHitTokens(res.Hits), sumHitTokens(res.Hits))
			return writeSuccessEnvelope(cmd.OutOrStdout(), "search", res)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&query, "query", "", "Search query")
	cmd.Flags().IntVar(&topK, "top-k", 10, "Top K results")
	cmd.Flags().StringVar(&mode, "mode", string(engine.ModeSearch), "Mode: search|recall|relate|outcomes")
	cmd.Flags().BoolVar(&explain, "explain", false, "Include per-signal score breakdown")
	cmd.Flags().StringSliceVar(&tiers, "tier", nil, "Filter by storage tier (repeatable): markdown|vector|vector+graph|document")
	cmd.Flags().StringSliceVar(&types, "type", nil, "Filter by memory type (repeatable): episodic|semantic|procedural|outcome")
	cmd.Flags().StringVar(&outcomeResult, "outcome-result", "", "Filter by outcome result: success|failure|partial")
	cmd.Flags().Float64Var(&minConfidence, "min-confidence", 0, "Filter by minimum confidence (0..1)")
	cmd.Flags().Float64Var(&minDecayScore, "min-decay-score", 0, "Filter by minimum decay score (0..1)")
	cmd.Flags().StringSliceVar(&entities, "entity", nil, "Filter by entity (repeatable)")
	cmd.Flags().StringVar(&from, "from", "", "Filter by updated_at from (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "Filter by updated_at to (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().IntVar(&tokenBudget, "token-budget", 0, "Token budget hint (API-only, optional)")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func newRecallCommand() *cobra.Command {
	var flags commonFlags
	var task string
	var topK, budget int
	cmd := &cobra.Command{
		Use:   "recall",
		Short: "Session-start recall block",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, true); err != nil {
				return err
			}
			if cfg.apiURL != "" {
				var out map[string]any
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/recall", map[string]any{
					"workspace":        cfg.workspace,
					"task_description": task,
					"top_k":            topK,
					"token_budget":     budget,
					"format":           flags.format,
				}, &out)
				if err != nil {
					return err
				}
				if strings.EqualFold(flags.format, formatRaw) {
					if text, _ := out["text"].(string); text != "" {
						_, err := fmt.Fprint(cmd.OutOrStdout(), text)
						return err
					}
					if text, _ := out["context_block"].(string); text != "" {
						_, err := fmt.Fprint(cmd.OutOrStdout(), text)
						return err
					}
					return errors.New("invalid raw recall response")
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "recall", out)
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			searcher := engine.NewVectorSearcher(store, provider)
			retrieval := engine.NewRetrievalEngine(searcher)
			retrieved, err := retrieval.Retrieve(ctx, engine.RetrievalOptions{
				Workspace: cfg.workspace,
				Query:     task,
				TopK:      topK,
				Mode:      engine.ModeRecall,
			})
			if err != nil {
				return err
			}
			clipper := engine.NewTokenClipper(nil)
			rebalanced := engine.RebalanceRecallHits(task, retrieved.Hits)
			included, meta := clipper.Clip(rebalanced, budget)
			_ = store.AddTokenMetric(ctx, cfg.workspace, "recall", meta.UsedTokens, sumHitTokens(rebalanced))
			payload := map[string]any{
				"mode":      retrieved.Mode,
				"weights":   retrieved.Weights,
				"hits":      included,
				"clipping":  meta,
				"workspace": cfg.workspace,
			}
			if strings.EqualFold(flags.format, formatRaw) {
				_, err := fmt.Fprint(cmd.OutOrStdout(), engine.AssembleRecallSections(task, included))
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "recall", payload)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&task, "task", "", "Task description")
	cmd.Flags().IntVar(&topK, "top-k", 50, "Candidate count")
	cmd.Flags().IntVar(&budget, "budget", 4000, "Token budget")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func parseTimeFlexibleCLI(s string) (time.Time, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func newSessionEndCommand() *cobra.Command {
	var flags commonFlags
	var transcript string
	cmd := &cobra.Command{
		Use:   "session-end",
		Short: "Extract and store memories from a session transcript",
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
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/session-end", map[string]any{
					"transcript": transcript,
				}, &out)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "session-end", out)
			}
			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			pipeline := engine.NewWritePipeline(store)
			extractor := engine.NewSessionEndExtractor(pipeline)
			out, err := extractor.ExtractAndStore(ctx, cfg.workspace, transcript)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "session-end", out)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&transcript, "transcript", "", "Transcript text")
	_ = cmd.MarkFlagRequired("transcript")
	return cmd
}

func newServeCommand() *cobra.Command {
	var flags commonFlags
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start local HTTP API",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			svc := &api.Service{
				Workspace: cfg.workspace,
				Writer:    engine.NewWritePipeline(store),
				Retrieval: engine.NewRetrievalEngine(engine.NewVectorSearcher(store, provider)),
				Clipper:   engine.NewTokenClipper(nil),
				Store:     store,
				BaseDir:   filepath.Dir(cfg.dbPath),
			}
			server := &http.Server{
				Addr:    addr,
				Handler: api.NewMux(svc),
			}
			host := addr
			if strings.HasPrefix(addr, ":") {
				host = "localhost" + addr
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "serving on http://%s\n", host)
			return server.ListenAndServe()
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&addr, "addr", ":3210", "HTTP listen address")
	return cmd
}

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
			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			study := engine.NewStudyEngine(engine.NewWritePipeline(store))
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
			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			re := engine.NewReconstructionEngine(store, engine.NewWritePipeline(store))
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
		Short: "Export workspace memories to json or markdown",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			format = strings.ToLower(strings.TrimSpace(format))
			if format != "json" && format != "markdown" {
				return errors.New("invalid export format: json|markdown")
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
			}
			if err := os.WriteFile(outFile, b, 0o644); err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "export", map[string]any{"file": outFile, "format": format})
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&format, "export-format", "json", "Export format: json|markdown")
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

func newStatsCommand() *cobra.Command {
	var flags commonFlags
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show workspace stats including token savings",
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
				if err := getAPI(ctx, cfg.apiURL, "/api/v1/stats", &out); err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "stats", out)
			}
			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			memories, err := store.ListMemoriesByWorkspace(ctx, cfg.workspace)
			if err != nil {
				return err
			}
			tm, err := store.AggregateTokenMetrics(ctx, cfg.workspace)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "stats", map[string]any{
				"workspace":             cfg.workspace,
				"memory_count":          len(memories),
				"token_metrics":         tm,
				"token_savings_percent": percentSaved(tm.BaselineTokens, tm.SavedTokens),
			})
		},
	}
	addCommonFlags(cmd, &flags)
	return cmd
}

func sumHitTokens(hits []engine.RetrievalHit) int {
	total := 0
	for _, h := range hits {
		total += len(strings.Fields(h.Memory.Content))
	}
	return total
}

func percentSaved(baseline, saved int) float64 {
	if baseline <= 0 || saved <= 0 {
		return 0
	}
	return (float64(saved) / float64(baseline)) * 100
}

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

			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			pipeline := engine.NewWritePipeline(store)

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
				"merged":    len(merged),
				"merged_ids": merged,
				"deep":      false,
				"dry_run":   dryRun,
			})
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().BoolVar(&deep, "deep", false, "Run cross-session deep consolidation")
	cmd.Flags().IntVar(&days, "days", 30, "Lookback window in days (--deep only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would happen without writing")
	return cmd
}
