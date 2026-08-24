package engine

import (
	"context"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type GapDetectionResult struct {
	Score      float64                `json:"score"`
	Tombstones []core.MemoryTombstone `json:"tombstones"`
	Triggered  bool                   `json:"triggered"`
	Reason     string                 `json:"reason"`
}

type GapDetector struct {
	store *sqlite.Store
	clock func() time.Time
}

func NewGapDetector(store *sqlite.Store) *GapDetector {
	return &GapDetector{
		store: store,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// Detect computes a bounded forgotten-signal score from tombstones.
func (g *GapDetector) Detect(ctx context.Context, workspace, query string) (*GapDetectionResult, error) {
	ts, err := g.store.ListTombstones(ctx, workspace, "")
	if err != nil {
		return nil, err
	}
	now := g.clock()
	score := 0.0
	active := make([]core.MemoryTombstone, 0, len(ts))
	for _, t := range ts {
		if !t.CooldownUntil.IsZero() && now.Before(t.CooldownUntil) {
			continue
		}
		age := now.Sub(t.EvictedAt)
		if age < 0 {
			age = 0 // clamp future evictions so the recency factor never exceeds 1
		}
		ageDays := age.Hours() / 24
		recency := 1 / (1 + ageDays/30)
		score += 0.35 * recency
		if strings.Contains(strings.ToLower(t.FragmentSummary), strings.ToLower(query)) {
			score += 0.25
		}
		active = append(active, t)
	}
	if len(active) >= 2 {
		score += 0.2
	}
	if score > 1 {
		score = 1
	}
	reason := "insufficient tombstone evidence"
	if len(active) == 0 && len(ts) > 0 {
		reason = "all matching tombstones are in cooldown"
	}
	res := &GapDetectionResult{
		Score:      score,
		Tombstones: active,
		Triggered:  score >= 0.4 && len(active) >= 2,
		Reason:     reason,
	}
	if res.Triggered {
		res.Reason = "tombstone signal indicates likely forgotten knowledge gap"
	}
	return res, nil
}
