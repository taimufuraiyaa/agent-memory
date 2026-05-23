package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
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
			if !engine.MemoryEnabled() {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "write", map[string]any{
					"skipped": true,
					"reason":  "disabled",
				})
			}
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
	var minSemanticScore float64
	var minTotalScore float64
	var relativeCutoff float64
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
			if !engine.MemoryEnabled() {
				store, _, err := openDeps(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				_ = store.AddTokenMetricV2(ctx, cfg.workspace, "search", 0, 0, engine.RunLabel(), false)
				return writeSuccessEnvelope(cmd.OutOrStdout(), "search", map[string]any{
					"disabled":  true,
					"workspace": cfg.workspace,
					"results":   []any{},
				})
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
				if minSemanticScore > 0 {
					filters["min_semantic_score"] = minSemanticScore
				}
				if minTotalScore > 0 {
					filters["min_total_score"] = minTotalScore
				}
				if relativeCutoff > 0 {
					filters["relative_cutoff"] = relativeCutoff
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
				Policy: engine.RetrievalPolicy{
					MinSemanticScore:    floatPtrIfPositive(minSemanticScore),
					MinTotalScore:       floatPtrIfPositive(minTotalScore),
					RelativeScoreCutoff: floatPtrIfPositive(relativeCutoff),
				},
			})
			if err != nil {
				return err
			}
			_ = store.AddTokenMetricV2(ctx, cfg.workspace, "search", sumHitTokens(res.Hits), sumHitTokens(res.Hits), engine.RunLabel(), true)
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
	cmd.Flags().Float64Var(&minSemanticScore, "min-semantic-score", 0, "Retrieval floor for semantic similarity (0..1)")
	cmd.Flags().Float64Var(&minTotalScore, "min-total-score", 0, "Retrieval floor for total score (0..1)")
	cmd.Flags().Float64Var(&relativeCutoff, "relative-cutoff", 0, "Relative cutoff against the strongest hit (0..1)")
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
	var includeObservations bool
	var observationLimit int
	var observationSessionID string
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
			if !engine.MemoryEnabled() {
				store, _, err := openDeps(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				_ = store.AddTokenMetricV2(ctx, cfg.workspace, "recall", 0, 0, engine.RunLabel(), false)
				contextBlock := engine.AssembleRecallSections(task, nil)
				if strings.EqualFold(flags.format, formatRaw) {
					_, err := fmt.Fprint(cmd.OutOrStdout(), contextBlock)
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "recall", map[string]any{
					"disabled":      true,
					"workspace":     cfg.workspace,
					"context_block": contextBlock,
				})
			}
			if cfg.apiURL != "" {
				var out map[string]any
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/recall", map[string]any{
					"workspace":              cfg.workspace,
					"task_description":       task,
					"top_k":                  topK,
					"token_budget":           budget,
					"format":                 flags.format,
					"include_observations":   includeObservations,
					"observation_limit":      observationLimit,
					"observation_session_id": strings.TrimSpace(observationSessionID),
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

			observationBlock := ""
			observationTokens := 0
			originalBudget := budget
			if includeObservations {
				block, _, _ := buildRecentObservationBlockCLI(ctx, store, cfg.workspace, strings.TrimSpace(observationSessionID), observationLimit)
				observationBlock = block
				observationTokens = len(strings.Fields(observationBlock)) + len(strings.Fields("## Recent Observations"))
				if budget-observationTokens > 0 {
					budget = budget - observationTokens
				} else {
					budget = 0
				}
			}

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
			_ = store.AddTokenMetricV2(ctx, cfg.workspace, "recall", meta.UsedTokens+observationTokens, recallBaselineTokens(rebalanced, observationTokens), engine.RunLabel(), true)
			payload := map[string]any{
				"mode":               retrieved.Mode,
				"weights":            retrieved.Weights,
				"hits":               included,
				"clipping":           meta,
				"workspace":          cfg.workspace,
				"requested_budget":   originalBudget,
				"observation_tokens": observationTokens,
			}
			if strings.EqualFold(flags.format, formatRaw) {
				_, err := fmt.Fprint(cmd.OutOrStdout(), engine.AssembleRecallSectionsWithObservations(task, observationBlock, included))
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "recall", payload)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&task, "task", "", "Task description")
	cmd.Flags().IntVar(&topK, "top-k", 50, "Candidate count")
	cmd.Flags().IntVar(&budget, "budget", 4000, "Token budget")
	cmd.Flags().BoolVar(&includeObservations, "include-observations", false, "Include recent observation summaries (if available)")
	cmd.Flags().IntVar(&observationLimit, "observation-limit", 10, "Recent observation count to include (max 50)")
	cmd.Flags().StringVar(&observationSessionID, "observation-session-id", "", "Session ID for observations (default: most recent)")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func newFeedbackCommand() *cobra.Command {
	var flags commonFlags
	var memoryID, outcome, validator, reasonCategory, occurredAt, reconsolidationAction, successorMemoryID string
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Record retrieval feedback for a memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			if strings.TrimSpace(memoryID) == "" {
				return errors.New("memory-id is required")
			}
			feedback := core.RetrievalFeedback(strings.ToLower(strings.TrimSpace(outcome)))
			switch feedback {
			case core.FeedbackHelpful, core.FeedbackIgnored, core.FeedbackRejected, core.FeedbackHarmful:
			default:
				return errors.New("outcome must be one of: helpful|ignored|rejected|harmful")
			}
			recon := core.ReconsolidationAction(strings.ToLower(strings.TrimSpace(reconsolidationAction)))
			switch recon {
			case "", core.ReconsolidateConfirmed, core.ReconsolidateClarified, core.ReconsolidateContradicted, core.ReconsolidateSuperseded:
			default:
				return errors.New("reconsolidation-action must be one of: confirmed|clarified|contradicted|superseded")
			}
			body := map[string]any{
				"workspace":       cfg.workspace,
				"memory_id":       strings.TrimSpace(memoryID),
				"outcome":         feedback,
				"validator":       strings.TrimSpace(validator),
				"reason_category": strings.TrimSpace(reasonCategory),
			}
			if strings.TrimSpace(occurredAt) != "" {
				body["occurred_at"] = strings.TrimSpace(occurredAt)
			}
			if recon != "" {
				body["reconsolidation_action"] = recon
			}
			if strings.TrimSpace(successorMemoryID) != "" {
				body["successor_memory_id"] = strings.TrimSpace(successorMemoryID)
			}
			if cfg.apiURL != "" {
				var out any
				if err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/feedback", body, &out); err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "feedback", out)
			}
			store, _, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			at := time.Now().UTC()
			if strings.TrimSpace(occurredAt) != "" {
				parsed, ok := parseTimeFlexibleCLI(occurredAt)
				if !ok {
					return errors.New("invalid occurred-at")
				}
				at = parsed
			}
			updated, err := store.ApplyRetrievalFeedback(ctx, strings.TrimSpace(memoryID), feedback, at)
			if err != nil {
				return err
			}
			if recon != "" {
				updated, err = store.ApplyReconsolidation(ctx, strings.TrimSpace(memoryID), recon, strings.TrimSpace(successorMemoryID), at)
				if err != nil {
					return err
				}
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "feedback", map[string]any{
				"workspace":              cfg.workspace,
				"memory_id":              strings.TrimSpace(memoryID),
				"outcome":                feedback,
				"validator":              strings.TrimSpace(validator),
				"reason_category":        strings.TrimSpace(reasonCategory),
				"reconsolidation_action": recon,
				"updated_memory":         updated,
			})
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&memoryID, "memory-id", "", "Memory ID to update")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Feedback outcome: helpful|ignored|rejected|harmful")
	cmd.Flags().StringVar(&validator, "validator", "", "Validator name (for example: ai-agent)")
	cmd.Flags().StringVar(&reasonCategory, "reason-category", "", "Optional reason category")
	cmd.Flags().StringVar(&occurredAt, "occurred-at", "", "Optional feedback timestamp (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&reconsolidationAction, "reconsolidation-action", "", "Optional reconsolidation: confirmed|clarified|contradicted|superseded")
	cmd.Flags().StringVar(&successorMemoryID, "successor-memory-id", "", "Optional successor memory ID for contradicted/superseded flows")
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

func floatPtrIfPositive(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func buildRecentObservationBlockCLI(ctx context.Context, store *sqlite.Store, workspace string, preferredSessionID string, limit int) (string, string, int) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	sessionID := strings.TrimSpace(preferredSessionID)
	if sessionID == "" {
		sessions, err := store.ListSessions(ctx, workspace, 1)
		if err != nil || len(sessions) == 0 {
			return "", "", 0
		}
		sessionID = sessions[0].SessionID
	}
	obs, err := store.ListObservations(ctx, workspace, sessionID, nil, nil, limit)
	if err != nil || len(obs) == 0 {
		return "", sessionID, 0
	}
	var b strings.Builder
	b.WriteString("Session: ")
	b.WriteString(sessionID)
	b.WriteString("\n")
	count := 0
	for _, o := range obs {
		if count >= limit {
			break
		}
		line := strings.TrimSpace(o.Summary)
		if line == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(o.OccurredAt.UTC().Format(time.RFC3339))
		b.WriteString(" ")
		b.WriteString(engine.ClipString(line, 240))
		b.WriteString("\n")
		count++
	}
	return strings.TrimSpace(b.String()), sessionID, count
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
			if !engine.MemoryEnabled() {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "session-end", map[string]any{
					"skipped": true,
					"reason":  "disabled",
				})
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

func openInBrowser(url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("url is required")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}

func apiURLForListenerAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost:3210"
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

func waitForHTTP(url string, timeout time.Duration) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("url is required")
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", url, nil)
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			return nil
		}
		time.Sleep(125 * time.Millisecond)
	}
	return errors.New("timeout waiting for server")
}

type dashboardPID struct {
	PID       int       `json:"pid"`
	VitePID   int       `json:"vite_pid,omitempty"`
	Workspace string    `json:"workspace"`
	Addr      string    `json:"addr"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"started_at"`
}

func dashboardPIDPath(cfg runtimeConfig) string {
	base := filepath.Dir(cfg.dbPath)
	name := "dashboard.pid"
	if ws := strings.TrimSpace(cfg.workspace); ws != "" {
		name = fmt.Sprintf("dashboard.%s.pid", ws)
	}
	return filepath.Join(base, name)
}

func readDashboardPID(path string) (dashboardPID, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return dashboardPID{}, err
	}
	var out dashboardPID
	if err := json.Unmarshal(b, &out); err == nil && out.PID > 0 {
		return out, nil
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid); err == nil && pid > 0 {
		return dashboardPID{PID: pid}, nil
	}
	return dashboardPID{}, errors.New("invalid dashboard pid file")
}

func writeDashboardPID(path string, v dashboardPID) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	}
	return false
}

func stopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("pid is required")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = p.Signal(syscall.SIGTERM)
		time.Sleep(250 * time.Millisecond)
		if isProcessAlive(pid) {
			_ = p.Kill()
		}
		return nil
	}
	if err := p.Kill(); err != nil {
		return err
	}
	return nil
}

func validateDashboardStartAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") && len(addr) > 1 && addr != ":0" {
			return nil
		}
		_ = host
		return fmt.Errorf("invalid addr: %s", addr)
	}
	if port == "0" {
		return errors.New("addr cannot use port 0 with --start (pick a fixed port)")
	}
	return nil
}

func buildDashboardProcessArgs(cfg runtimeConfig, addr string, dashDirFlag string, pidFile string) []string {
	args := []string{
		"dashboard",
		"--no-open",
		"--addr", addr,
		"--db", cfg.dbPath,
		"--model-dir", cfg.modelDir,
	}
	if ws := strings.TrimSpace(cfg.workspace); ws != "" {
		args = append(args, "--workspace", ws)
	}
	if strings.TrimSpace(dashDirFlag) != "" {
		args = append(args, "--dashboard-dir", dashDirFlag)
	}
	if strings.TrimSpace(pidFile) != "" {
		args = append(args, "--pid-file", pidFile)
	}
	return args
}

func startDashboardProcess(cfg runtimeConfig, addr string, dashDirFlag string, pidFile string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	args := buildDashboardProcessArgs(cfg, addr, dashDirFlag, pidFile)
	c := exec.Command(exe, args...)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	if err := c.Start(); err != nil {
		return 0, err
	}
	return c.Process.Pid, nil
}

func pickFreeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, err
	}
	if port <= 0 {
		return 0, errors.New("failed to allocate a free port")
	}
	return port, nil
}

func resolveDashboardRuntime(flags commonFlags) (runtimeConfig, error) {
	modelDir := strings.TrimSpace(flags.modelDir)
	if modelDir == "" {
		home, _ := os.UserHomeDir()
		modelDir = embeddings.DefaultModelDir(home)
	}
	apiURL := resolveAPIURL(flags.apiURL)
	dbPath := strings.TrimSpace(flags.dbPath)
	workspace, err := resolveWorkspace(flags.workspace)
	if err == nil {
		if dbPath == "" {
			dbPath, err = defaultDBPath(workspace)
			if err != nil {
				return runtimeConfig{}, err
			}
		}
		return runtimeConfig{
			workspace: workspace,
			dbPath:    dbPath,
			modelDir:  modelDir,
			apiURL:    apiURL,
		}, nil
	}
	if dbPath == "" {
		baseDir, baseErr := defaultDBBaseDir()
		if baseErr != nil {
			return runtimeConfig{}, baseErr
		}
		dbPath = filepath.Join(baseDir, ".dashboard-placeholder.db")
	}
	return runtimeConfig{
		workspace: "",
		dbPath:    dbPath,
		modelDir:  modelDir,
		apiURL:    apiURL,
	}, nil
}

func dashboardSourceDir(override string) (string, error) {
	if v := strings.TrimSpace(override); v != "" {
		dir := v
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", fmt.Errorf("dashboard dir not found: %s", dir)
		}
		if !fileExists(filepath.Join(dir, "package.json")) {
			return "", errors.New("dashboard package.json is missing")
		}
		return dir, nil
	}
	if v := strings.TrimSpace(os.Getenv("AGENT_MEMORY_DASHBOARD_DIR")); v != "" {
		return dashboardSourceDir(v)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root := findSourceRoot(cwd)
	if strings.TrimSpace(root) == "" {
		return "", errors.New("standalone dashboard sources not found (run from the repository)")
	}
	dir := filepath.Join(root, "tools", "agent-memory", "dashboard")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", errors.New("standalone dashboard directory is missing (tools/agent-memory/dashboard)")
	}
	if !fileExists(filepath.Join(dir, "package.json")) {
		return "", errors.New("standalone dashboard package.json is missing")
	}
	return dir, nil
}

func newDashboardCommand() *cobra.Command {
	var flags commonFlags
	var addr string
	var noOpen bool
	var start bool
	var stop bool
	var dashDirFlag string
	var pidFile string
	cmd := &cobra.Command{
		Use:     "dashboard",
		Short:   "Open the local dashboard (starts Go API + React dev server)",
		Aliases: []string{"ui"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			cfg, err := resolveDashboardRuntime(flags)
			if err != nil {
				return err
			}
			if cfg.apiURL != "" {
				url := strings.TrimRight(cfg.apiURL, "/") + "/dashboard/"
				if noOpen {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", url)
					return nil
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
				return openInBrowser(url)
			}
			if start && stop {
				return errors.New("only one of --start or --stop can be set")
			}
			pidPath := strings.TrimSpace(pidFile)
			if pidPath == "" {
				pidPath = dashboardPIDPath(cfg)
			}
			if stop {
				v, err := readDashboardPID(pidPath)
				if err != nil {
					return fmt.Errorf("dashboard stop: %w", err)
				}
				if v.VitePID > 0 {
					_ = stopProcess(v.VitePID)
				}
				if v.PID > 0 {
					_ = stopProcess(v.PID)
				}
				_ = os.Remove(pidPath)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "stopped dashboard (pid=%d)\n", v.PID)
				return nil
			}
			if start {
				if err := validateDashboardStartAddr(addr); err != nil {
					return err
				}
				if v, err := readDashboardPID(pidPath); err == nil && isProcessAlive(v.PID) {
					url := strings.TrimSpace(v.URL)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dashboard already running (pid=%d)\n", v.PID)
					if noOpen {
						if url != "" {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", url)
						}
						return nil
					}
					if url != "" {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
						return openInBrowser(url)
					}
					return nil
				}
				_ = os.Remove(pidPath)

				pid, err := startDashboardProcess(cfg, addr, dashDirFlag, pidPath)
				if err != nil {
					return err
				}
				_ = writeDashboardPID(pidPath, dashboardPID{
					PID:       pid,
					Workspace: cfg.workspace,
					Addr:      addr,
					URL:       "",
					StartedAt: time.Now().UTC(),
				})

				url := ""
				for i := 0; i < 40; i++ {
					time.Sleep(125 * time.Millisecond)
					v, err := readDashboardPID(pidPath)
					if err != nil {
						continue
					}
					if !isProcessAlive(v.PID) {
						break
					}
					if strings.TrimSpace(v.URL) != "" {
						url = strings.TrimSpace(v.URL)
						break
					}
				}

				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "started dashboard (pid=%d)\n", pid)
				if url == "" {
					return errors.New("dashboard failed to start (no URL reported)")
				}
				if noOpen {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", url)
					return nil
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
				_ = openInBrowser(url)
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
				return err
			}
			if err := os.MkdirAll(cfg.modelDir, 0o755); err != nil {
				return err
			}
			provider, err := embeddings.NewLocalProvider(cfg.modelDir)
			if err != nil {
				return err
			}

			svc := &api.Service{
				Workspace:         cfg.workspace,
				BaseDir:           filepath.Dir(cfg.dbPath),
				EmbeddingProvider: provider,
			}
			server := &http.Server{
				Addr:    addr,
				Handler: api.NewMux(svc),
			}

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			errCh := make(chan error, 1)
			go func() { errCh <- server.Serve(ln) }()

			apiURL := apiURLForListenerAddr(ln.Addr().String())
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "api serving on %s\n", apiURL)

			dashDir, err := dashboardSourceDir(dashDirFlag)
			if err != nil {
				_ = server.Shutdown(context.Background())
				return err
			}
			if _, err := exec.LookPath("npm"); err != nil {
				_ = server.Shutdown(context.Background())
				return errors.New("npm is required to run the standalone dashboard")
			}
			port, err := pickFreeLocalPort()
			if err != nil {
				_ = server.Shutdown(context.Background())
				return err
			}
			dashURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
			vite := exec.CommandContext(ctx, "npm", "run", "dev", "--", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port), "--strictPort")
			vite.Dir = dashDir
			vite.Env = append(os.Environ(), "VITE_API_TARGET="+apiURL)
			vite.Stdout = cmd.ErrOrStderr()
			vite.Stderr = cmd.ErrOrStderr()
			if err := vite.Start(); err != nil {
				_ = server.Shutdown(context.Background())
				return err
			}
			viteCh := make(chan error, 1)
			go func() { viteCh <- vite.Wait() }()

			if err := waitForHTTP(dashURL, 4*time.Second); err != nil {
				_ = server.Shutdown(context.Background())
				_ = ln.Close()
				_ = vite.Process.Kill()
				return err
			}

			if strings.TrimSpace(pidFile) != "" {
				_ = writeDashboardPID(pidPath, dashboardPID{
					PID:       os.Getpid(),
					VitePID:   vite.Process.Pid,
					Workspace: cfg.workspace,
					Addr:      addr,
					URL:       dashURL,
					StartedAt: time.Now().UTC(),
				})
				defer func() { _ = os.Remove(pidPath) }()
			}

			if noOpen {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", dashURL)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", dashURL)
				_ = openInBrowser(dashURL)
			}

			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = server.Shutdown(shutdownCtx)
				cancel()
				_ = ln.Close()
				_ = vite.Process.Kill()
				return nil
			case err := <-viteCh:
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = server.Shutdown(shutdownCtx)
				cancel()
				_ = ln.Close()
				if err != nil {
					return err
				}
				return nil
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				_ = vite.Process.Kill()
				return err
			}
		},
	}
	addCommonFlags(cmd, &flags)
	_ = cmd.Flags().MarkHidden("workspace")
	cmd.Flags().StringVar(&addr, "addr", ":3210", "HTTP listen address")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Do not open a browser; just print the URL")
	cmd.Flags().BoolVar(&start, "start", false, "Start dashboard server in the background and exit")
	cmd.Flags().BoolVar(&stop, "stop", false, "Stop the background dashboard server (started via --start)")
	cmd.Flags().StringVar(&dashDirFlag, "dashboard-dir", "", "Path to standalone dashboard folder (tools/agent-memory/dashboard)")
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Internal: pid file path")
	_ = cmd.Flags().MarkHidden("pid-file")
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
			tg, err := store.AggregateTokenMetricsByGroup(ctx, cfg.workspace)
			if err != nil {
				return err
			}
			tokenByOperation, err := store.AggregateTokenMetricsByOperation(ctx, cfg.workspace)
			if err != nil {
				return err
			}
			recallTokenTotals := tokenTotalsForOperation(tokenByOperation, "recall")
			typeCounts := map[string]int{}
			tierCounts := map[string]int{}
			diagramCount := 0
			pinnedCount := 0
			totalRetrieveCount := 0
			retrievedMemoryCount := 0
			var lastUpdatedAt time.Time
			var lastAccessedAt time.Time
			for _, m := range memories {
				typeCounts[string(m.Type)]++
				tierCounts[string(m.StorageTier)]++
				if m.Diagram != nil && strings.TrimSpace(m.Diagram.Code) != "" {
					diagramCount++
				}
				if m.Pinned {
					pinnedCount++
				}
				totalRetrieveCount += m.AccessCount
				if m.AccessCount > 0 {
					retrievedMemoryCount++
				}
				if m.UpdatedAt.After(lastUpdatedAt) {
					lastUpdatedAt = m.UpdatedAt
				}
				if m.LastAccessedAt.After(lastAccessedAt) {
					lastAccessedAt = m.LastAccessedAt
				}
			}
			enabledGroups := make([]sqlite.TokenMetricGroupTotals, 0, len(tg))
			disabledGroups := make([]sqlite.TokenMetricGroupTotals, 0, len(tg))
			for _, g := range tg {
				if g.MemoryEnabled {
					enabledGroups = append(enabledGroups, g)
				} else {
					disabledGroups = append(disabledGroups, g)
				}
			}
			llmTotals, err := store.AggregateLLMUsageTotals(ctx, cfg.workspace)
			if err != nil {
				return err
			}
			llmGroups, err := store.AggregateLLMUsageByGroup(ctx, cfg.workspace)
			if err != nil {
				return err
			}
			llmEnabledGroups := make([]sqlite.LLMUsageGroupTotals, 0, len(llmGroups))
			llmDisabledGroups := make([]sqlite.LLMUsageGroupTotals, 0, len(llmGroups))
			for _, g := range llmGroups {
				if g.MemoryEnabled {
					llmEnabledGroups = append(llmEnabledGroups, g)
				} else {
					llmDisabledGroups = append(llmDisabledGroups, g)
				}
			}
			lastActivity := lastUpdatedAt
			if lastAccessedAt.After(lastActivity) {
				lastActivity = lastAccessedAt
			}
			lastUpdated := ""
			if !lastUpdatedAt.IsZero() {
				lastUpdated = lastUpdatedAt.UTC().Format(time.RFC3339Nano)
			}
			lastAccessed := ""
			if !lastAccessedAt.IsZero() {
				lastAccessed = lastAccessedAt.UTC().Format(time.RFC3339Nano)
			}
			lastActivityStr := ""
			if !lastActivity.IsZero() {
				lastActivityStr = lastActivity.UTC().Format(time.RFC3339Nano)
			}
			neverReachedMemoryCount := len(memories) - retrievedMemoryCount
			retrievalCoveragePercent := 0.0
			neverReachedPercent := 0.0
			if len(memories) > 0 {
				retrievalCoveragePercent = (float64(retrievedMemoryCount) / float64(len(memories))) * 100
				neverReachedPercent = (float64(neverReachedMemoryCount) / float64(len(memories))) * 100
			}
			lowReachPercentile := 25
			lowReachThreshold, lowReachMemoryCount := computeLowReachStats(memories, lowReachPercentile)
			return writeSuccessEnvelope(cmd.OutOrStdout(), "stats", map[string]any{
				"workspace":                     cfg.workspace,
				"memory_count":                  len(memories),
				"memory_type_counts":            typeCounts,
				"storage_tier_counts":           tierCounts,
				"diagram_count":                 diagramCount,
				"pinned_count":                  pinnedCount,
				"retrieve_count_total":          totalRetrieveCount,
				"retrieved_memory_count":        retrievedMemoryCount,
				"never_reached_memory_count":    neverReachedMemoryCount,
				"retrieval_coverage_percent":    retrievalCoveragePercent,
				"never_reached_percent":         neverReachedPercent,
				"low_reach_percentile":          lowReachPercentile,
				"low_reach_threshold":           lowReachThreshold,
				"low_reach_memory_count":        lowReachMemoryCount,
				"top_retrieved_memories":        buildTopRetrievedMemories(memories, 5),
				"last_memory_updated_at":        lastUpdated,
				"last_memory_accessed_at":       lastAccessed,
				"last_activity":                 lastActivityStr,
				"token_metrics":                 tm,
				"token_metrics_by_operation":    tokenByOperation,
				"token_metrics_by_group":        enabledGroups,
				"raw_token_metrics_by_group":    disabledGroups,
				"token_metrics_by_group_all":    tg,
				"recall_token_metrics":          recallTokenTotals,
				"llm_usage_totals":              llmTotals,
				"llm_usage_by_group":            llmEnabledGroups,
				"raw_llm_usage_by_group":        llmDisabledGroups,
				"llm_usage_by_group_all":        llmGroups,
				"overall_token_savings_percent": percentSaved(tm.BaselineTokens, tm.SavedTokens),
				"recall_token_savings_percent":  percentSaved(recallTokenTotals.BaselineTokens, recallTokenTotals.SavedTokens),
				"token_savings_percent":         percentSaved(recallTokenTotals.BaselineTokens, recallTokenTotals.SavedTokens),
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

func recallBaselineTokens(hits []engine.RetrievalHit, observationTokens int) int {
	return sumHitTokens(hits) + observationTokens
}

func percentSaved(baseline, saved int) float64 {
	if baseline <= 0 || saved <= 0 {
		return 0
	}
	return (float64(saved) / float64(baseline)) * 100
}

func tokenTotalsForOperation(items []sqlite.TokenMetricOperationTotals, operation string) sqlite.TokenMetricTotals {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Operation), operation) {
			return item.TokenMetricTotals
		}
	}
	return sqlite.TokenMetricTotals{}
}

type topRetrievedMemory struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	StorageTier    string `json:"storage_tier"`
	AccessCount    int    `json:"access_count"`
	LastAccessedAt string `json:"last_accessed_at,omitempty"`
	Pinned         bool   `json:"pinned"`
	Preview        string `json:"preview"`
}

func buildTopRetrievedMemories(memories []core.MemoryEntry, limit int) []topRetrievedMemory {
	if limit <= 0 {
		limit = 5
	}
	items := make([]core.MemoryEntry, 0, len(memories))
	for _, m := range memories {
		if m.AccessCount <= 0 {
			continue
		}
		items = append(items, m)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].AccessCount != items[j].AccessCount {
			return items[i].AccessCount > items[j].AccessCount
		}
		if !items[i].LastAccessedAt.Equal(items[j].LastAccessedAt) {
			return items[i].LastAccessedAt.After(items[j].LastAccessedAt)
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]topRetrievedMemory, 0, len(items))
	for _, m := range items {
		lastAccessed := ""
		if !m.LastAccessedAt.IsZero() {
			lastAccessed = m.LastAccessedAt.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, topRetrievedMemory{
			ID:             m.ID,
			Type:           string(m.Type),
			StorageTier:    string(m.StorageTier),
			AccessCount:    m.AccessCount,
			LastAccessedAt: lastAccessed,
			Pinned:         m.Pinned,
			Preview:        memoryPreview(m.Content, 96),
		})
	}
	return out
}

func computeLowReachStats(memories []core.MemoryEntry, percentile int) (threshold int, count int) {
	if percentile <= 0 {
		percentile = 25
	}
	reached := make([]int, 0, len(memories))
	for _, m := range memories {
		if m.AccessCount > 0 {
			reached = append(reached, m.AccessCount)
		}
	}
	if len(reached) == 0 {
		return 0, 0
	}
	sort.Ints(reached)
	rank := int(math.Ceil((float64(percentile) / 100) * float64(len(reached))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(reached) {
		rank = len(reached)
	}
	threshold = reached[rank-1]
	for _, hits := range reached {
		if hits <= threshold {
			count++
		}
	}
	return threshold, count
}

func memoryPreview(content string, limit int) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if clean == "" {
		return "-"
	}
	return engine.ClipString(clean, limit)
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
