package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

// workspaceDashboardHandler implements GET /api/v1/dashboard: a minimal
// summary endpoint (memory count) for the dashboard landing view.
func workspaceDashboardHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		memories, err := assets.Store.ListMemoriesByWorkspace(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"totals": map[string]any{
				"memory_count": len(memories),
			},
		})
	}
}

// workspaceGraphHandler implements GET /api/v1/graph: returns memory nodes
// and relation edges for a workspace's knowledge graph.
func workspaceGraphHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		memories, err := assets.Store.ListMemoriesByWorkspace(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		relations, err := assets.Store.ListWorkspaceRelations(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}

		type GraphNode struct {
			ID          string           `json:"id"`
			Content     string           `json:"content"`
			Type        core.MemoryType  `json:"type"`
			StorageTier core.StorageTier `json:"storage_tier"`
		}

		nodes := make([]GraphNode, 0, len(memories))
		for _, m := range memories {
			nodes = append(nodes, GraphNode{
				ID:          m.ID,
				Content:     m.Content,
				Type:        m.Type,
				StorageTier: m.StorageTier,
			})
		}

		writeOK(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"nodes":     nodes,
			"edges":     relations,
		})
	}
}

// workspaceStatsHandler implements GET /api/v1/stats: aggregates per-workspace
// memory counts, token/LLM usage metrics, retrieval coverage, scheduler
// status, and cache statistics for the dashboard.
func workspaceStatsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		memories, err := assets.Store.ListMemoriesByWorkspace(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
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
		tokenTotals, err := assets.Store.AggregateTokenMetrics(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		tokenGroups, err := assets.Store.AggregateTokenMetricsByGroup(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		tokenByOperation, err := assets.Store.AggregateTokenMetricsByOperation(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		recallTokenTotals := tokenTotalsForOperation(tokenByOperation, "recall")
		enabledGroups := make([]sqlite.TokenMetricGroupTotals, 0, len(tokenGroups))
		disabledGroups := make([]sqlite.TokenMetricGroupTotals, 0, len(tokenGroups))
		for _, g := range tokenGroups {
			if g.MemoryEnabled {
				enabledGroups = append(enabledGroups, g)
			} else {
				disabledGroups = append(disabledGroups, g)
			}
		}
		llmTotals, err := assets.Store.AggregateLLMUsageTotals(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		llmGroups, err := assets.Store.AggregateLLMUsageByGroup(r.Context(), workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
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
		var dbSize int64
		if svc.BaseDir != "" {
			dbPath := filepath.Join(svc.BaseDir, workspace+".db")
			if st, statErr := os.Stat(dbPath); statErr == nil {
				dbSize = st.Size()
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
		topRetrievedMemories := buildTopRetrievedMemories(memories, 5)

		var schedulerSummary any
		if svc.Scheduler != nil {
			if status, err := svc.Scheduler.Status(r.Context()); err == nil && status != nil {
				schedulerSummary = schedulerSummaryForWorkspace(status, workspace)
			}
		} else {
			schedulerSummary = externalSchedulerSummary(r.Context(), svc.BaseDir, workspace)
		}
		cacheStats := assets.Retrieval.CacheStats()
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":                     workspace,
			"memory_count":                  len(memories),
			"db_size_bytes":                 dbSize,
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
			"top_retrieved_memories":        topRetrievedMemories,
			"last_memory_updated_at":        lastUpdated,
			"last_memory_accessed_at":       lastAccessed,
			"last_activity":                 lastActivityStr,
			"token_metrics":                 tokenTotals,
			"token_metrics_by_operation":    tokenByOperation,
			"token_metrics_by_group":        enabledGroups,
			"raw_token_metrics_by_group":    disabledGroups,
			"token_metrics_by_group_all":    tokenGroups,
			"recall_token_metrics":          recallTokenTotals,
			"llm_usage_totals":              llmTotals,
			"llm_usage_by_group":            llmEnabledGroups,
			"raw_llm_usage_by_group":        llmDisabledGroups,
			"llm_usage_by_group_all":        llmGroups,
			"overall_token_savings_percent": percentSaved(tokenTotals.BaselineTokens, tokenTotals.SavedTokens),
			"recall_token_savings_percent":  percentSaved(recallTokenTotals.BaselineTokens, recallTokenTotals.SavedTokens),
			"token_savings_percent":         percentSaved(recallTokenTotals.BaselineTokens, recallTokenTotals.SavedTokens),
			"scheduler":                     schedulerSummary,
			"cache": map[string]any{
				"enabled":            cacheStats.Enabled,
				"embedding_entries":  cacheStats.EmbeddingEntries,
				"result_entries":     cacheStats.ResultEntries,
				"embedding_hits":     cacheStats.EmbeddingHits,
				"embedding_misses":   cacheStats.EmbeddingMisses,
				"result_hits":        cacheStats.ResultHits,
				"result_misses":      cacheStats.ResultMisses,
				"embedding_hit_rate": cacheStats.EmbeddingHitRate(),
				"result_hit_rate":    cacheStats.ResultHitRate(),
			},
		})
	}
}
