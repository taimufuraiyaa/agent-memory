package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type recallRequest struct {
	Task                 string `json:"task"`
	TopK                 int    `json:"top_k"`
	Budget               int    `json:"budget"`
	IncludeObservations  bool   `json:"include_observations,omitempty"`
	ObservationLimit     int    `json:"observation_limit,omitempty"`
	ObservationSessionID string `json:"observation_session_id,omitempty"`
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

	requestID := uuid.New().String()
	_ = store.LogRetrievalRequest(ctx, requestID, cfg.workspace, "recall", task)

	if !memoryEnabled {
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

	observationBlock := ""
	observationTokens := 0
	observationCount := 0
	selectedObservationSessionID := strings.TrimSpace(req.ObservationSessionID)
	originalBudget := req.Budget
	budget := req.Budget
	if req.IncludeObservations {
		block, sessionID, count := buildRecentObservationBlockCLI(ctx, store, cfg.workspace, selectedObservationSessionID, req.ObservationLimit)
		observationBlock = block
		selectedObservationSessionID = sessionID
		observationCount = count
		observationTokens = len(strings.Fields(observationBlock)) + len(strings.Fields("## Recent Observations"))
		if budget-observationTokens > 0 {
			budget = budget - observationTokens
		} else {
			budget = 0
		}
	}

	// Create shared cache for searcher, retrieval, and pipeline
	cache := engine.NewQueryCache(engine.DefaultQueryCacheConfig())
	searcher := engine.NewVectorSearcher(store, provider)
	retrieval := engine.NewRetrievalEngineWithSharedCache(searcher, cache)
	pipeline := engine.NewWritePipelineWithOptions(store, engine.WritePipelineOptions{
		Embedder: provider,
		Cache:    cache,
	})
	var (
		retrieved *engine.RetrievalResult
		decision  engine.RecallGateDecision
		err       error
	)
	if engine.IsContinuationPrompt(task) {
		decision = engine.DecideRecallGate(task, nil)
		retrieved, err = retrieval.Retrieve(ctx, engine.RetrievalOptions{
			Workspace: cfg.workspace,
			Query:     task,
			TopK:      req.TopK,
			Mode:      engine.ModeRecall,
		})
	} else {
		searchProbe, searchErr := retrieval.Retrieve(ctx, engine.RetrievalOptions{
			Workspace: cfg.workspace,
			Query:     task,
			TopK:      req.TopK,
			Mode:      engine.ModeSearch,
		})
		if searchErr != nil {
			return nil, "", searchErr
		}
		decision = engine.DecideRecallGate(task, searchProbe)
		if decision.SearchSufficient {
			retrieved = &engine.RetrievalResult{
				Mode:           engine.ModeSearch,
				Weights:        searchProbe.Weights,
				Policy:         searchProbe.Policy,
				Hits:           append([]engine.RetrievalHit(nil), searchProbe.StrongHits...),
				StrongHits:     append([]engine.RetrievalHit(nil), searchProbe.StrongHits...),
				WeakHits:       append([]engine.RetrievalHit(nil), searchProbe.WeakHits...),
				SuppressedHits: append([]engine.RetrievalHit(nil), searchProbe.SuppressedHits...),
			}
		} else {
			retrieved, err = retrieval.Retrieve(ctx, engine.RetrievalOptions{
				Workspace: cfg.workspace,
				Query:     task,
				TopK:      req.TopK,
				Mode:      engine.ModeRecall,
			})
		}
	}
	if err != nil {
		return nil, "", err
	}
	retrieved, reconstruction, err := engine.AugmentRecallWithReconstruction(ctx, cfg.workspace, task, retrieved, retrieval, store, pipeline, req.TopK)
	if err != nil {
		return nil, "", err
	}
	clipper := engine.NewTokenClipper(nil)
	rebalanced := engine.RebalanceRecallHits(task, retrieved.Hits)
	included, meta := clipper.Clip(rebalanced, budget)
	if err := store.AddTokenMetricV2(ctx, cfg.workspace, "recall", meta.UsedTokens+observationTokens, recallBaselineTokens(rebalanced, observationTokens), engine.RunLabel(), true); err != nil {
		return nil, "", err
	}
	contextBlock := engine.AssembleRecallSectionsWithObservations(task, observationBlock, included)
	payload := map[string]any{
		"request_id":             requestID,
		"mode":                   retrieved.Mode,
		"weights":                retrieved.Weights,
		"hits":                   included,
		"clipping":               meta,
		"context_block":          contextBlock,
		"tokens_used":            meta.UsedTokens + observationTokens,
		"tokens_budget":          meta.Budget + observationTokens,
		"workspace":              cfg.workspace,
		"requested_task":         task,
		"requested_top_k":        req.TopK,
		"requested_budget":       originalBudget,
		"observations_included":  req.IncludeObservations,
		"observation_session_id": selectedObservationSessionID,
		"observation_count":      observationCount,
		"observation_tokens":     observationTokens,
		"retrieval_mode":         retrieved.Mode,
		"retrieved_hit_count":    len(retrieved.Hits),
		"retrieval_strategy":     decision.Strategy,
		"recall_trigger":         decision.Trigger,
		"search_sufficient":      decision.SearchSufficient,
		"search_probe":           decision.Probe,
		"deep_recall_used":       decision.Strategy != engine.RecallStrategySearchSatisfied,
		"reconstruction":         reconstruction,
	}
	return payload, contextBlock, nil
}
