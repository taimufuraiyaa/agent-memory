package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/observability"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type LifecycleMetrics struct {
	DecayUpdated   int `json:"decay_updated"`
	Consolidated   int `json:"consolidated"`
	ConflictsFound int `json:"conflicts_found"`
	Evicted        int `json:"evicted"`
	Promoted       int `json:"promoted"`
	Demoted        int `json:"demoted"`
}

type LifecycleManager struct {
	store          *sqlite.Store
	decay          *DecayEngine
	consolidation  *ConsolidationEngine
	conflicts      *ConflictEngine
	maxEntries     int
	markdownBudget int
}

func NewLifecycleManager(store *sqlite.Store, pipeline *WritePipeline) *LifecycleManager {
	return &LifecycleManager{
		store:          store,
		decay:          NewDecayEngine(store),
		consolidation:  NewConsolidationEngine(store, pipeline),
		conflicts:      NewConflictEngine(store),
		maxEntries:     5000,
		markdownBudget: 4000,
	}
}

func (m *LifecycleManager) Run(ctx context.Context, workspace string) (*LifecycleMetrics, error) {
	ctx, span := observability.StartSpan(ctx, "agent-memory.lifecycle")
	defer span.End()
	observability.SetSpanAttributes(ctx, observability.WorkspaceAttr(workspace))

	_start := time.Now()
	var runErr error
	defer func() {
		status := "success"
		if runErr != nil {
			status = "error"
			observability.RecordSpanError(ctx, runErr)
		}
		observability.GetRegistry().LifecycleDuration.WithLabelValues(workspace, status).Observe(time.Since(_start).Seconds())
	}()

	metrics := &LifecycleMetrics{}
	n, err := m.decay.UpdateWorkspaceDecay(ctx, workspace)
	if err != nil {
		runErr = err
		return nil, err
	}
	metrics.DecayUpdated = n

	ids, err := m.consolidation.Run(ctx, workspace, MergeFast)
	if err != nil {
		runErr = err
		return nil, err
	}
	metrics.Consolidated = len(ids)

	conflicts, err := m.conflicts.Resolve(ctx, workspace)
	if err != nil {
		runErr = err
		return nil, err
	}
	metrics.ConflictsFound = len(conflicts)

	evicted, promoted, demoted, err := m.applyEvictionPromotion(ctx, workspace)
	if err != nil {
		runErr = err
		return nil, err
	}
	metrics.Evicted = evicted
	metrics.Promoted = promoted
	metrics.Demoted = demoted
	return metrics, nil
}

func (m *LifecycleManager) applyEvictionPromotion(ctx context.Context, workspace string) (evicted int, promoted int, demoted int, err error) {
	memories, err := m.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return 0, 0, 0, err
	}
	// Promote successful outcomes and frequently accessed facts.
	for _, mm := range memories {
		if mm.StorageTier == core.TierVectorGraph {
			continue
		}
		if (mm.Outcome != nil && mm.Outcome.Result == core.OutcomeSuccess) || mm.AccessCount >= 5 {
			from := mm.StorageTier
			if err := m.store.UpdateTier(ctx, mm.ID, core.TierVectorGraph); err != nil {
				return evicted, promoted, demoted, err
			}
			_ = m.store.AddTierTransition(ctx, mm.ID, from, core.TierVectorGraph, "promoted by successful outcome or frequent access")
			promoted++
		}
	}
	demoted, err = m.applyTierRebalance(ctx, workspace, memories)
	if err != nil {
		return evicted, promoted, demoted, err
	}

	if len(memories) <= m.maxEntries {
		return evicted, promoted, demoted, nil
	}
	over := len(memories) - m.maxEntries
	toDelete := make([]string, 0, over)
	toDeleteSet := make(map[string]struct{}, over)
	for i := len(memories) - 1; i >= 0 && len(toDelete) < over; i-- {
		mm := memories[i]
		if mm.Pinned || mm.Type == core.ProceduralMemory {
			continue
		}
		if mm.DecayScore < 0.65 {
			continue
		}
		toDelete = append(toDelete, mm.ID)
		toDeleteSet[mm.ID] = struct{}{}
	}
	for _, mm := range memories {
		if _, ok := toDeleteSet[mm.ID]; !ok {
			continue
		}
		_ = m.store.AddTombstone(ctx, mm, "evict", "")
	}
	if err := m.store.DeleteByIDs(ctx, toDelete); err != nil {
		return evicted, promoted, demoted, err
	}
	evicted = len(toDelete)
	return evicted, promoted, demoted, nil
}

func (m *LifecycleManager) applyTierRebalance(ctx context.Context, workspace string, memories []core.MemoryEntry) (int, error) {
	now := time.Now().UTC()
	demoted := 0
	for _, mm := range memories {
		if mm.StorageTier == core.TierMarkdown && !mm.Pinned {
			stale := !mm.UpdatedAt.IsZero() && now.Sub(mm.UpdatedAt) > 60*24*time.Hour
			if mm.AccessCount < 2 && stale {
				from := mm.StorageTier
				if err := m.store.UpdateTier(ctx, mm.ID, core.TierVector); err != nil {
					return demoted, err
				}
				_ = m.store.AddTierTransition(ctx, mm.ID, from, core.TierVector, "demoted due to low access and staleness")
				demoted++
			}
			continue
		}
		if mm.StorageTier != core.TierMarkdown && shouldPromoteToMarkdown(mm) {
			from := mm.StorageTier
			if err := m.store.UpdateTier(ctx, mm.ID, core.TierMarkdown); err != nil {
				return demoted, err
			}
			_ = m.store.AddTierTransition(ctx, mm.ID, from, core.TierMarkdown, "promoted to markdown by lifecycle policy")
		}
	}

	if len(memories) == 0 {
		return demoted, nil
	}
	after, err := m.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil || len(after) == 0 {
		return demoted, err
	}
	total := 0
	markdown := make([]core.MemoryEntry, 0)
	for _, mm := range after {
		if mm.StorageTier != core.TierMarkdown {
			continue
		}
		toks := len(strings.Fields(mm.Content))
		total += toks
		markdown = append(markdown, mm)
	}
	if total <= m.markdownBudget {
		return demoted, nil
	}
	sort.Slice(markdown, func(i, j int) bool {
		if markdown[i].Pinned != markdown[j].Pinned {
			return !markdown[i].Pinned
		}
		if markdown[i].AccessCount != markdown[j].AccessCount {
			return markdown[i].AccessCount < markdown[j].AccessCount
		}
		return markdown[i].UpdatedAt.Before(markdown[j].UpdatedAt)
	})
	for _, mm := range markdown {
		if total <= m.markdownBudget {
			break
		}
		if mm.Pinned {
			continue
		}
		from := mm.StorageTier
		if err := m.store.UpdateTier(ctx, mm.ID, core.TierVector); err != nil {
			return demoted, err
		}
		_ = m.store.AddTierTransition(ctx, mm.ID, from, core.TierVector, "demoted to satisfy markdown token budget")
		total -= len(strings.Fields(mm.Content))
		demoted++
	}
	return demoted, nil
}

func shouldPromoteToMarkdown(m core.MemoryEntry) bool {
	if m.Pinned || m.Type == core.ProceduralMemory {
		return true
	}
	return m.AccessCount >= 10 && len(strings.Fields(m.Content)) <= 100
}
