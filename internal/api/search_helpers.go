package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

func sumHitTokens(hits []engine.RetrievalHit) int {
	total := 0
	for _, hit := range hits {
		total += len(strings.Fields(hit.Memory.Content))
	}
	return total
}

func parseIntOrDefault(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func workspaceFromRequest(r *http.Request, fallback string) string {
	ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if ws == "" {
		return fallback
	}
	return ws
}

type searchResult struct {
	core.MemoryEntry
	Tier             string                   `json:"tier,omitempty"`
	Score            float64                  `json:"score,omitempty"`
	ScoreBreakdown   map[string]float64       `json:"score_breakdown,omitempty"`
	MatchReason      string                   `json:"match_reason,omitempty"`
	TombstoneHint    any                      `json:"tombstone_hint,omitempty"`
	Band             string                   `json:"band,omitempty"`
	ExclusionReasons []engine.ExclusionReason `json:"exclusion_reasons,omitempty"`
}

func renderSearchResults(hits []engine.RetrievalHit, explain bool) []searchResult {
	out := make([]searchResult, 0, len(hits))
	for _, h := range hits {
		item := searchResult{
			MemoryEntry: h.Memory,
		}
		if explain {
			item.Tier = string(h.Memory.StorageTier)
			item.Score = h.Score
			item.ScoreBreakdown = scoreBreakdownForHit(h)
			item.MatchReason = matchReasonForHit(h)
			item.TombstoneHint = nil
			item.Band = string(h.Band)
			item.ExclusionReasons = h.ExclusionReasons
		}
		out = append(out, item)
	}
	return out
}

func scoreBreakdownForHit(h engine.RetrievalHit) map[string]float64 {
	return map[string]float64{
		"semantic_similarity": h.Breakdown.Semantic,
		"recency":             h.Breakdown.Recency,
		"outcome_boost":       h.Breakdown.Outcome,
		"decay_weight":        h.Breakdown.Decay,
		"tier_bias":           h.Breakdown.TierBias,
		"salience":            h.Breakdown.Salience,
		"suppression":         h.Breakdown.Suppression,
		"activation":          h.Breakdown.Activation,
		"relative_to_best":    h.Breakdown.RelativeToBest,
		"total":               h.Breakdown.Total,
	}
}

func matchReasonForHit(h engine.RetrievalHit) string {
	sb := scoreBreakdownForHit(h)
	bestK := ""
	bestV := -1.0
	for k, v := range sb {
		if v > bestV {
			bestV = v
			bestK = k
		}
	}
	switch bestK {
	case "semantic_similarity":
		return "High semantic similarity"
	case "recency":
		return "Recently updated"
	case "outcome_boost":
		return "Outcome memory boost"
	case "decay_weight":
		return "Low decay (still relevant)"
	case "tier_bias":
		return "Tier bias applied"
	default:
		return "Ranked by combined signals"
	}
}

func parseTiers(in []string) ([]core.StorageTier, error) {
	out := make([]core.StorageTier, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(strings.ToLower(raw))
		if v == "" {
			continue
		}
		t := core.StorageTier(v)
		if !core.IsStorageTier(t) {
			return nil, fmt.Errorf("invalid tier: %s", v)
		}
		out = append(out, t)
	}
	return out, nil
}

func parseTimeFlexible(s string) (time.Time, bool) {
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

func renderClipped(meta engine.ClipMetadata) []map[string]any {
	out := make([]map[string]any, 0, len(meta.ClippedDetails))
	for _, c := range meta.ClippedDetails {
		out = append(out, map[string]any{
			"id":               c.ID,
			"reason":           string(c.Reason),
			"would_add_tokens": c.Tokens,
		})
	}
	return out
}
