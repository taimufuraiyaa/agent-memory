package cli

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

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
