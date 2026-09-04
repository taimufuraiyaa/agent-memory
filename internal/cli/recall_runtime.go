package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type recallRequest struct {
	Task                 string                        `json:"task"`
	TopK                 int                           `json:"top_k"`
	Budget               int                           `json:"budget"`
	IncludeObservations  bool                          `json:"include_observations,omitempty"`
	ObservationLimit     int                           `json:"observation_limit,omitempty"`
	ObservationSessionID string                        `json:"observation_session_id,omitempty"`
	GraphMode            graphretrieval.GraphQueryMode `json:"graph_mode,omitempty"`
	GraphRequired        bool                          `json:"graph_required,omitempty"`
	GraphAllowStale      bool                          `json:"graph_allow_stale,omitempty"`
}

func openStore(ctx context.Context, cfg runtimeConfig) (*sqlite.Store, error) {
	if strings.TrimSpace(cfg.dbPath) == "" || strings.TrimSpace(cfg.workspace) == "" {
		return nil, errors.New("db path and workspace are required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
		return nil, err
	}
	return sqlite.Open(ctx, cfg.dbPath)
}

func openDeps(ctx context.Context, cfg runtimeConfig) (*sqlite.Store, embeddings.Provider, error) {
	store, err := openStore(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(cfg.modelDir, 0o755); err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	provider, err := embeddings.NewProvider(cfg.modelDir)
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	return store, provider, nil
}

// checkAndWarnModelVersion checks for model version mismatches and prints warnings to stderr.
// This is called after openDeps to alert users about embedding version inconsistencies.
// If AGENT_MEMORY_AUTO_REEMBED=1 is set and a significant mismatch is detected, it will
// automatically trigger a reembed operation.
func checkAndWarnModelVersion(ctx context.Context, workspace string, store *sqlite.Store, provider embeddings.Provider) {
	if store == nil || provider == nil || strings.TrimSpace(workspace) == "" {
		return
	}

	check, err := engine.CheckModelVersion(ctx, workspace, store, provider)
	if err != nil {
		// Don't fail the command, just skip the check
		return
	}

	if check == nil || !check.ShouldWarnAboutVersionMismatch() {
		return
	}

	// Print warning to stderr
	_, _ = os.Stderr.WriteString("\n" + check.FormatWarningMessage() + "\n")

	// Check for auto-reembed flag
	if strings.TrimSpace(os.Getenv("AGENT_MEMORY_AUTO_REEMBED")) != "1" {
		return
	}

	// Auto-trigger reembed if enabled and reembed is required
	if !check.ReembedRequired {
		return
	}

	_, _ = os.Stderr.WriteString("\n🔄 Auto-reembed triggered (AGENT_MEMORY_AUTO_REEMBED=1)\n")
	_, _ = os.Stderr.WriteString("Re-embedding workspace: " + workspace + "\n\n")

	// Get the database path from the store
	dbPath := getDBPathFromWorkspace(workspace)
	if dbPath == "" {
		_, _ = os.Stderr.WriteString("⚠️  Failed to determine database path, skipping auto-reembed\n\n")
		return
	}

	// Run reembed
	result, reembedErr := runReembedWorkspace(ctx, workspace, dbPath, provider, false)
	if reembedErr != nil {
		_, _ = os.Stderr.WriteString("⚠️  Auto-reembed failed: " + reembedErr.Error() + "\n\n")
		return
	}

	_, _ = os.Stderr.WriteString(fmt.Sprintf("✅ Auto-reembed complete:\n"))
	_, _ = os.Stderr.WriteString(fmt.Sprintf("   - Re-embedded: %d memories\n", result.ReEmbedded))
	_, _ = os.Stderr.WriteString(fmt.Sprintf("   - Skipped: %d memories\n\n", result.Skipped))
}

func getDBPathFromWorkspace(workspace string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	baseDir := filepath.Join(home, ".agent-memory")
	return filepath.Join(baseDir, workspace+".db")
}

func executeRecall(
	ctx context.Context,
	cfg runtimeConfig,
	store *sqlite.Store,
	provider embeddings.Provider,
	memoryEnabled bool,
	req recallRequest,
) (map[string]any, string, error) {
	if store == nil {
		return nil, "", errors.New("store is required")
	}
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return nil, "", errors.New("task is required")
	}

	if !memoryEnabled {
		requestID := uuid.New().String()
		_ = store.LogRetrievalRequest(ctx, requestID, cfg.workspace, "recall", task)
		contextBlock := engine.AssembleRecallSections(task, nil)
		disabledTokens := len(strings.Fields(contextBlock))
		if err := store.AddTokenMetricV2(ctx, cfg.workspace, "recall", disabledTokens, disabledTokens, engine.RunLabel(), false); err != nil {
			return nil, "", err
		}
		payload := map[string]any{
			"request_id":         requestID,
			"disabled":           true,
			"workspace":          cfg.workspace,
			"context_block":      contextBlock,
			"hits":               []any{},
			"tokens_used":        disabledTokens,
			"baseline_tokens":    disabledTokens,
			"observation_tokens": 0,
		}
		return payload, contextBlock, nil
	}

	if provider == nil {
		return nil, "", errors.New("provider is required when memory is enabled")
	}

	// Reset per-recall benchmark instrumentation counters.
	engine.ResetBenchmarkMetrics()

	// Create shared cache for searcher, retrieval, and pipeline
	cache := engine.NewQueryCache(engine.DefaultQueryCacheConfig())
	searcher := engine.NewVectorSearcher(store, provider)
	retrieval := engine.NewRetrievalEngineWithSharedCache(searcher, cache)
	pipeline := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{
		Embedder: provider,
		Cache:    cache,
	})
	app := application.NewMemoryService(store, pipeline, retrieval)
	result, err := app.Recall(ctx, application.RecallOptions{
		Workspace:           cfg.workspace,
		Task:                task,
		TopK:                req.TopK,
		Budget:              req.Budget,
		IncludeObservations: req.IncludeObservations,
		ObservationSession:  req.ObservationSessionID,
		ObservationLimit:    req.ObservationLimit,
		GraphMode:           req.GraphMode,
		GraphRequired:       req.GraphRequired,
		GraphPolicy: graphretrieval.GraphRoutePolicy{
			GraphEnabled: req.GraphMode != "" && req.GraphMode != graphretrieval.GraphQueryBasic,
			AllowLocal:   true, AllowGlobal: true, AllowStale: req.GraphAllowStale,
		},
	})
	if err != nil {
		return nil, "", err
	}
	var howResult *engine.HowRecallResult
	if engine.IsHowOrientedTask(task) {
		how, howErr := engine.NewHowRecallService(store).Recall(ctx, engine.HowRecallInput{
			Workspace: cfg.workspace, Task: task, TokenBudget: req.Budget,
			SessionID: req.ObservationSessionID,
		})
		if howErr != nil {
			return nil, "", howErr
		}
		result.ContextBlock = engine.AppendHowRecallContext(result.ContextBlock, how)
		howResult = &how
	}
	// Flush benchmark instrumentation metrics after each recall.
	_ = engine.FlushBenchmarkMetrics()
	payload := map[string]any{
		"request_id":             result.RequestID,
		"mode":                   result.Retrieved.Mode,
		"weights":                result.Retrieved.Weights,
		"hits":                   result.Included,
		"clipping":               result.Clip,
		"context_block":          result.ContextBlock,
		"tokens_used":            result.Clip.UsedTokens + result.ObservationTokens,
		"tokens_budget":          result.Clip.Budget + result.ObservationTokens,
		"workspace":              cfg.workspace,
		"requested_task":         task,
		"requested_top_k":        req.TopK,
		"requested_budget":       result.OriginalBudget,
		"observations_included":  req.IncludeObservations,
		"observation_session_id": result.ObservationSessionID,
		"observation_count":      result.ObservationCount,
		"observation_tokens":     result.ObservationTokens,
		"retrieval_mode":         result.Retrieved.Mode,
		"retrieved_hit_count":    len(result.Retrieved.Hits),
		"retrieval_strategy":     result.Decision.Strategy,
		"recall_trigger":         result.Decision.Trigger,
		"search_sufficient":      result.Decision.SearchSufficient,
		"search_probe":           result.Decision.Probe,
		"deep_recall_used":       result.Decision.Strategy != engine.RecallStrategySearchSatisfied,
		"reconstruction":         result.Reconstruction,
		"graph_route":            result.GraphRoute,
		"graph_context":          result.GraphContext,
		"benchmark_metrics":      engine.SnapshotBenchmarkMetrics(),
	}
	if howResult != nil {
		payload["how_recall"] = howResult
		payload["how_request_id"] = howResult.RequestID
	}
	return payload, result.ContextBlock, nil
}
