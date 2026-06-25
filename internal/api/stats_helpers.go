package api

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

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
