package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
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

func newWriteCommand() *cobra.Command {
	var flags commonFlags
	var mType, content string
	var keywords []string
	var outcomeResult, outcomeApproach string
	cmd := &cobra.Command{
		Use:   "write",
		Short: "Write one memory entry",
		Long: `Write a memory entry to the workspace.

Memory types:
  episodic   - Raw observations and conversation turns (7 day half-life)
  semantic   - Facts and knowledge (30 day half-life)
  procedural - Checklists and workflows (90 day half-life)
  outcome    - Records of what worked or failed (60 day half-life)

The memory will be automatically:
  - Validated for safety and size limits
  - Embedded for semantic search
  - Routed to appropriate storage tier
  - Available for retrieval in future queries`,
		Example: `  # Write a semantic fact
  agent-memory write --workspace my-project --type semantic \
    --content "The API uses JWT tokens for authentication"

  # Write a procedural checklist
  agent-memory write --workspace my-project --type procedural \
    --content "Run 'make test' before committing changes"

  # Write an outcome record
  agent-memory write --workspace my-project --type outcome \
    --content "Database migration failed due to lock timeout"

  # Write to default workspace (from current directory)
  agent-memory write --type semantic \
    --content "Redis is used for session caching"`,
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
				body := map[string]any{
					"type":     mType,
					"content":  content,
					"keywords": keywords,
				}
				if strings.TrimSpace(outcomeResult) != "" {
					body["outcome_result"] = outcomeResult
				}
				if strings.TrimSpace(outcomeApproach) != "" {
					body["outcome_approach"] = outcomeApproach
				}
				var out any
				err := postAPI(ctx, cfg.apiURL, "/api/v1/memories/write", body, &out)
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "write", out)
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			// Check for model version mismatches and warn user
			checkAndWarnModelVersion(ctx, cfg.workspace, store, provider)

			p := engine.NewWritePipelineWithEmbedder(store, provider)
			app := application.NewMemoryService(store, p, nil)
			mt := core.MemoryType(mType)
			in := engine.WriteInput{
				Workspace: cfg.workspace,
				Type:      mt,
				Content:   content,
				Keywords:  keywords,
				Source:    core.MemorySource{Type: core.SourceUserInput},
				Mode:      engine.ExtractFast,
			}
			if mt == core.OutcomeMemory {
				o := &core.Outcome{}
				hasOutcome := false
				if strings.TrimSpace(outcomeResult) != "" {
					o.Result = core.OutcomeResult(strings.ToLower(strings.TrimSpace(outcomeResult)))
					hasOutcome = true
				}
				if strings.TrimSpace(outcomeApproach) != "" {
					o.Approach = strings.TrimSpace(outcomeApproach)
					hasOutcome = true
				}
				if hasOutcome {
					in.Outcome = o
				}
			}
			res, err := app.Write(ctx, in)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "write", res)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&mType, "type", "semantic", "Memory type: episodic|semantic|procedural|outcome")
	cmd.Flags().StringVar(&content, "content", "", "Memory content")
	cmd.Flags().StringSliceVar(&keywords, "keyword", nil, "Exact locator keyword (repeatable, maximum 3)")
	cmd.Flags().StringVar(&outcomeResult, "outcome-result", "", "Outcome result: success|failure|partial (for type=outcome)")
	cmd.Flags().StringVar(&outcomeApproach, "outcome-approach", "", "Approach description (for type=outcome)")
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
	var depth int
	var operator string
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Semantic multi-signal search",
		Long: `Search workspace memories using semantic similarity and multi-signal ranking.

The search uses multiple signals to rank results:
  - Semantic similarity (embedding-based)
  - Recency (recently updated items)
  - Outcome signal (successful outcomes boosted)
  - Decay penalty (old items fade)
  - Tier bias (markdown tier preferred)

Use --explain to see per-signal score breakdowns.`,
		Example: `  # Basic semantic search
  agent-memory search --workspace my-project \
    --query "how does authentication work"

  # Search with explanation of scores
  agent-memory search --workspace my-project \
    --query "database configuration" --explain

  # Filter by memory type
  agent-memory search --workspace my-project \
    --query "deployment process" --type procedural

  # Search for successful outcomes only
  agent-memory search --workspace my-project \
    --query "migration" --outcome-result success

  # Recall mode for session start (higher quality threshold)
  agent-memory search --workspace my-project \
    --query "continue previous work" --mode recall --top-k 5`,
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
				requestID := uuid.New().String()
				_ = store.LogRetrievalRequest(ctx, requestID, cfg.workspace, "search", query)
				_ = store.AddTokenMetricV2(ctx, cfg.workspace, "search", 0, 0, engine.RunLabel(), false)
				return writeSuccessEnvelope(cmd.OutOrStdout(), "search", map[string]any{
					"request_id": requestID,
					"disabled":   true,
					"workspace":  cfg.workspace,
					"results":    []any{},
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
			retrievalFilters := engine.RetrievalFilters{
				Types:         typed,
				Tiers:         tiered,
				OutcomeResult: outcome,
				MinConfidence: minC,
				MinDecayScore: minD,
				Entities:      entities,
				DateFrom:      dateFrom,
				DateTo:        dateTo,
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
				if cmd.Flags().Changed("min-semantic-score") {
					filters["min_semantic_score"] = minSemanticScore
				}
				if cmd.Flags().Changed("min-total-score") {
					filters["min_total_score"] = minTotalScore
				}
				if cmd.Flags().Changed("relative-cutoff") {
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
					"operator":     operator,
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
			if engine.RetrievalMode(strings.ToLower(strings.TrimSpace(mode))) == engine.ModeTerms {
				store, err := openStore(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				app := application.NewMemoryService(store, nil, nil)
				res, err := app.SearchTerms(ctx, application.TermSearchOptions{
					Workspace: cfg.workspace,
					Query:     query,
					TopK:      topK,
					Operator:  application.TermOperator(strings.ToLower(strings.TrimSpace(operator))),
					Filters:   retrievalFilters,
				})
				if err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "search", res)
			}
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			// Check for model version mismatches and warn user
			checkAndWarnModelVersion(ctx, cfg.workspace, store, provider)

			searcher := engine.NewVectorSearcher(store, provider)
			retrieval := engine.NewRetrievalEngine(searcher)
			app := application.NewMemoryService(store, nil, retrieval)
			res, err := app.Search(ctx, engine.RetrievalOptions{
				Workspace: cfg.workspace,
				Query:     query,
				TopK:      topK,
				Mode:      engine.RetrievalMode(mode),
				Depth:     depth,
				Filters:   retrievalFilters,
				Policy: engine.RetrievalPolicy{
					MinSemanticScore:    floatPtrIfChanged(cmd, "min-semantic-score", minSemanticScore),
					MinTotalScore:       floatPtrIfChanged(cmd, "min-total-score", minTotalScore),
					RelativeScoreCutoff: floatPtrIfChanged(cmd, "relative-cutoff", relativeCutoff),
				},
			})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "search", res)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&query, "query", "", "Search query")
	cmd.Flags().IntVar(&topK, "top-k", 10, "Top K results")
	cmd.Flags().StringVar(&mode, "mode", string(engine.ModeSearch), "Mode: search|recall|relate|outcomes|graph-expand|terms")
	cmd.Flags().StringVar(&operator, "operator", string(application.TermOperatorAND), "Term operator for mode=terms: and|or")
	cmd.Flags().IntVar(&depth, "depth", 2, "Graph expansion depth limit (mode=graph-expand only)")
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
	var useAdaptiveBudget bool
	var budgetPercentage float64

	cmd := &cobra.Command{
		Use:   "recall",
		Short: "Session-start recall block",
		Long: `Generate a context block for session start or continuation.

Recall assembles relevant memories within a token budget, perfect for
including in agent system prompts. It prioritizes:
  - Procedural knowledge (how-to)
  - Recent successful outcomes
  - Relevant semantic facts
  - Task-specific context

The output is formatted as a structured context block ready to paste
into your agent's system prompt or context.

Budget Configuration:
  The --budget flag sets a fixed token budget. Alternatively, use
  --adaptive-budget to scale the budget based on AGENT_CONTEXT_WINDOW
  (defaults to 10% of context window, configurable via --budget-percentage).`,
		Example: `  # Basic recall for continuing work
  agent-memory recall --workspace my-project \
    --task "continue working on authentication feature" --budget 1000

  # Quick recall with smaller budget
  agent-memory recall --workspace my-project \
    --task "fix database migration bug" --budget 500

  # Adaptive budget (10% of AGENT_CONTEXT_WINDOW)
  agent-memory recall --workspace my-project \
    --task "implement API endpoint" --adaptive-budget

  # Adaptive budget with custom percentage (15% of context window)
  agent-memory recall --workspace my-project \
    --task "deploy to production" --adaptive-budget --budget-percentage 0.15

  # Recall with observations from current session
  agent-memory recall --workspace my-project \
    --task "implement API endpoint" --budget 800 \
    --include-observations --observation-limit 5

  # Raw format (for piping to other tools)
  agent-memory recall --workspace my-project \
    --task "deploy to production" --format raw --budget 1000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, true); err != nil {
				return err
			}

			// Handle adaptive budget sizing
			if useAdaptiveBudget {
				adaptiveBudget := config.GetAdaptiveBudget(budget, budgetPercentage)
				if adaptiveBudget != budget {
					budget = adaptiveBudget
					// Note: Don't print to stdout in JSON mode, use stderr
					if strings.EqualFold(flags.format, formatRaw) || flags.format == "" {
						// Silent in raw mode to avoid polluting output
					}
				}
			}

			if !engine.MemoryEnabled() {
				store, _, err := openDeps(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				requestID := uuid.New().String()
				_ = store.LogRetrievalRequest(ctx, requestID, cfg.workspace, "recall", task)
				contextBlock := engine.AssembleRecallSections(task, nil)
				disabledTokens := len(strings.Fields(contextBlock))
				_ = store.AddTokenMetricV2(ctx, cfg.workspace, "recall", disabledTokens, disabledTokens, engine.RunLabel(), false)
				if strings.EqualFold(flags.format, formatRaw) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[agent-memory] request_id: "+requestID)
					_, err := fmt.Fprint(cmd.OutOrStdout(), contextBlock)
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "recall", map[string]any{
					"request_id":         requestID,
					"disabled":           true,
					"workspace":          cfg.workspace,
					"context_block":      contextBlock,
					"hits":               []any{},
					"tokens_used":        disabledTokens,
					"baseline_tokens":    disabledTokens,
					"observation_tokens": 0,
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
					if reqID, _ := out["request_id"].(string); reqID != "" {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[agent-memory] request_id: "+reqID)
					}
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
			request := recallRequest{
				Task:                 task,
				TopK:                 topK,
				Budget:               budget,
				IncludeObservations:  includeObservations,
				ObservationLimit:     observationLimit,
				ObservationSessionID: observationSessionID,
			}
			memoryEnabled := engine.MemoryEnabled()
			var store *sqlite.Store
			var provider embeddings.Provider
			if memoryEnabled {
				store, provider, err = openDeps(ctx, cfg)
			} else {
				store, err = openStore(ctx, cfg)
			}
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			// Check for model version mismatches and warn user (only if memory enabled)
			if memoryEnabled {
				checkAndWarnModelVersion(ctx, cfg.workspace, store, provider)
			}

			payload, contextBlock, err := executeRecall(ctx, cfg, store, provider, memoryEnabled, request)
			if err != nil {
				return err
			}
			if strings.EqualFold(flags.format, formatRaw) {
				if reqID, _ := payload["request_id"].(string); reqID != "" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[agent-memory] request_id: "+reqID)
				}
				_, err := fmt.Fprint(cmd.OutOrStdout(), contextBlock)
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "recall", payload)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&task, "task", "", "Task description")
	cmd.Flags().IntVar(&topK, "top-k", 50, "Candidate count")
	cmd.Flags().IntVar(&budget, "budget", 4000, "Token budget (or use --adaptive-budget)")
	cmd.Flags().BoolVar(&useAdaptiveBudget, "adaptive-budget", false, "Scale budget based on AGENT_CONTEXT_WINDOW")
	cmd.Flags().Float64Var(&budgetPercentage, "budget-percentage", 0.10, "Percentage of context window for adaptive budget (default: 0.10 = 10%)")
	cmd.Flags().BoolVar(&includeObservations, "include-observations", false, "Include recent observation summaries (if available)")
	cmd.Flags().IntVar(&observationLimit, "observation-limit", 10, "Recent observation count to include (max 50)")
	cmd.Flags().StringVar(&observationSessionID, "observation-session-id", "", "Session ID for observations (default: most recent)")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func newFeedbackCommand() *cobra.Command {
	var flags commonFlags
	var memoryID, outcome, validator, reasonCategory, occurredAt, reconsolidationAction, successorMemoryID, reason string
	var requestID string
	var score int
	var usefulCount, totalCount int
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
			if requestID != "" {
				if score < 0 || score > 5 {
					return errors.New("score must be between 0 and 5")
				}
				if score < 4 && strings.TrimSpace(reason) == "" {
					return errors.New("reason is required for scores below 4")
				}
				if usefulCount != -1 && totalCount != -1 && usefulCount > totalCount {
					return errors.New("useful-count cannot be greater than total-count")
				}
				if cfg.apiURL != "" {
					body := map[string]any{
						"workspace":    cfg.workspace,
						"request_id":   requestID,
						"score":        score,
						"reason":       reason,
						"useful_count": usefulCount,
						"total_count":  totalCount,
					}
					var out any
					if err := postAPI(ctx, cfg.apiURL, "/api/v1/requests/feedback", body, &out); err != nil {
						return err
					}
					return writeSuccessEnvelope(cmd.OutOrStdout(), "feedback", out)
				}
				store, _, err := openDeps(ctx, cfg)
				if err != nil {
					return err
				}
				defer func() { _ = store.Close() }()
				if err := store.RecordRequestFeedback(ctx, requestID, score, reason, usefulCount, totalCount); err != nil {
					return err
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "feedback", map[string]any{
					"workspace":    cfg.workspace,
					"request_id":   requestID,
					"score":        score,
					"reason":       reason,
					"useful_count": usefulCount,
					"total_count":  totalCount,
					"ok":           true,
				})
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
			app := application.NewMemoryService(store, nil, nil)
			updated, err := app.Feedback(ctx, application.FeedbackInput{
				MemoryID:              memoryID,
				Outcome:               feedback,
				OccurredAt:            at,
				ReconsolidationAction: recon,
				SuccessorMemoryID:     successorMemoryID,
			})
			if err != nil {
				return err
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
	cmd.Flags().StringVar(&requestID, "request-id", "", "Request ID for scoring feedback")
	cmd.Flags().IntVar(&score, "score", -1, "Feedback score: 0 (useless) to 5 (helpful)")
	cmd.Flags().StringVar(&reason, "reason", "", "Explanation / reason for the feedback score")
	cmd.Flags().IntVar(&usefulCount, "useful-count", -1, "Number of useful memories found in retrieval results")
	cmd.Flags().IntVar(&totalCount, "total-count", -1, "Total number of memories retrieved")
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

func floatPtrIfChanged(cmd *cobra.Command, name string, v float64) *float64 {
	if cmd == nil || !cmd.Flags().Changed(name) {
		return nil
	}
	return &v
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

			// Read from stdin when --transcript is empty and stdin is a pipe
			if strings.TrimSpace(transcript) == "" {
				stat, _ := os.Stdin.Stat()
				if (stat.Mode() & os.ModeCharDevice) == 0 {
					b, err := io.ReadAll(os.Stdin)
					if err != nil {
						return fmt.Errorf("read stdin: %w", err)
					}
					transcript = string(b)
				}
			}

			if strings.TrimSpace(transcript) == "" {
				return errors.New("transcript is required (use --transcript or pipe via stdin)")
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
			store, provider, err := openDeps(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			pipeline := engine.NewWritePipelineWithEmbedder(store, provider)
			out, err := engine.RunSessionEndLifecycle(ctx, cfg.workspace, transcript, store, pipeline)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "session-end", out)
		},
	}
	addCommonFlags(cmd, &flags)
	cmd.Flags().StringVar(&transcript, "transcript", "", "Transcript text (or omit to read from stdin)")
	return cmd
}
