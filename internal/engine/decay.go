package engine

import (
	"context"
	"math"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// DecayEngine computes and persists decay scores.
type DecayEngine struct {
	store *sqlite.Store
	clock func() time.Time
}

// NewDecayEngine creates a decay engine.
func NewDecayEngine(store *sqlite.Store) *DecayEngine {
	return &DecayEngine{
		store: store,
		clock: func() time.Time { return time.Now().UTC() },
	}
}

// Compute returns a bounded [0,1] decay score where 1 means highly decayed.
func ComputeDecayScore(now time.Time, m core.MemoryEntry) float64 {
	halfLifeHours := typeHalfLifeHours(m.Type)
	base := baseDecay(now, m.UpdatedAt, halfLifeHours)
	boost := boostFactor(m)
	score := base * (1 - boost)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

// UpdateWorkspaceDecay recomputes and persists scores for all workspace memories.
func (d *DecayEngine) UpdateWorkspaceDecay(ctx context.Context, workspace string) (int, error) {
	memories, err := d.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return 0, err
	}
	now := d.clock()
	byID := make(map[string]float64, len(memories))
	for _, m := range memories {
		byID[m.ID] = ComputeDecayScore(now, m)
	}
	if err := d.store.SetDecayScores(ctx, byID); err != nil {
		return 0, err
	}
	return len(byID), nil
}

func typeHalfLifeHours(mt core.MemoryType) float64 {
	switch mt {
	case core.ProceduralMemory:
		return 24 * 45 // stable rules decay slower
	case core.SemanticMemory:
		return 24 * 21
	case core.OutcomeMemory:
		return 24 * 14
	default: // episodic and unknowns
		return 24 * 7
	}
}

func baseDecay(now, updated time.Time, halfLifeHours float64) float64 {
	if updated.IsZero() || halfLifeHours <= 0 {
		return 0
	}
	ageHours := now.Sub(updated).Hours()
	if ageHours <= 0 {
		return 0
	}
	return 1 - math.Exp(-math.Ln2*ageHours/halfLifeHours)
}

func boostFactor(m core.MemoryEntry) float64 {
	accessBoost := math.Min(0.35, 0.06*math.Log1p(float64(maxInt(m.AccessCount, 0))))
	outcomeBoost := 0.0
	if m.Outcome != nil && m.Outcome.Result == core.OutcomeSuccess {
		outcomeBoost = 0.12
	}
	pinBoost := 0.0
	if m.Pinned {
		pinBoost = 0.25
	}
	total := accessBoost + outcomeBoost + pinBoost
	if total > 0.65 {
		return 0.65
	}
	return total
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

