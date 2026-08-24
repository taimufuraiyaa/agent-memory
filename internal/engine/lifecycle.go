package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type LifecycleMetrics struct {
	DecayUpdated   int `json:"decay_updated"`
	Consolidated   int `json:"consolidated"`
	ConflictsFound int `json:"conflicts_found"`
	Evicted        int `json:"evicted"`
	Promoted       int `json:"promoted"`
	Demoted        int `json:"demoted"`
	Summarized     int `json:"summarized"`
}

type LifecycleManager struct {
	store          *sqlite.Store
	decay          *DecayEngine
	consolidation  *ConsolidationEngine
	conflicts      *ConflictEngine
	pipeline       *WritePipeline
	summarizer     *ColdTierSummarizer
	archive        *ColdArchive // nil = archiving disabled
	maxEntries     int
	markdownBudget int

	// OnWorkspaceChange, when non-nil, is invoked at the end of Run with the
	// workspace that was maintained, after decay/consolidation/eviction have
	// mutated stored data. Wire it to drop stale retrieval-cache entries for
	// that workspace (e.g. retrievalEngine.InvalidateCache(workspace)); the
	// query-cache TTL remains the backstop for runs that never reach the hook.
	// TODO(engine): wire this at the LifecycleManager construction site in
	// internal/cli/serve_command.go (runWorkspace) once that file is editable.
	OnWorkspaceChange func(workspace string)
}

func NewLifecycleManager(store *sqlite.Store, pipeline *WritePipeline) *LifecycleManager {
	return &LifecycleManager{
		store:          store,
		decay:          NewDecayEngine(store),
		consolidation:  NewConsolidationEngine(store, pipeline),
		conflicts:      NewConflictEngine(store),
		pipeline:       pipeline,
		summarizer:     NewColdTierSummarizer(),
		maxEntries:     5000,
		markdownBudget: 4000,
	}
}

// WithArchive enables cold-tier archive storage.  dataDir is the root data
// directory (e.g. ~/.agent-memory); archives are stored in dataDir/archives/.
func (m *LifecycleManager) WithArchive(dataDir string) *LifecycleManager {
	m.archive = NewColdArchive(dataDir)
	return m
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
		// Notify listeners that maintenance ran (or partially ran, on error):
		// decay and consolidation may have changed scores even when a later
		// step failed, so stale caches should be dropped in either case.
		if m.OnWorkspaceChange != nil {
			m.OnWorkspaceChange(workspace)
		}
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

	evicted, promoted, demoted, summarized, err := m.applyEvictionPromotion(ctx, workspace)
	if err != nil {
		runErr = err
		return nil, err
	}
	metrics.Evicted = evicted
	metrics.Promoted = promoted
	metrics.Demoted = demoted
	metrics.Summarized = summarized
	return metrics, nil
}

func (m *LifecycleManager) applyEvictionPromotion(ctx context.Context, workspace string) (evicted int, promoted int, demoted int, summarized int, err error) {
	memories, err := m.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	// Promote successful outcomes and frequently accessed facts.
	for _, mm := range memories {
		if mm.StorageTier == core.TierVectorGraph {
			continue
		}
		if (mm.Outcome != nil && mm.Outcome.Result == core.OutcomeSuccess) || mm.AccessCount >= 5 {
			from := mm.StorageTier
			if err := m.store.UpdateTier(ctx, mm.ID, core.TierVectorGraph); err != nil {
				return evicted, promoted, demoted, summarized, err
			}
			_ = m.store.AddTierTransition(ctx, mm.ID, from, core.TierVectorGraph, "promoted by successful outcome or frequent access")
			_, _ = m.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: workspace, Operation: "promote", Outcome: "success", Actor: "lifecycle", Source: "lifecycle", TargetType: "memory", TargetIDs: []string{mm.ID}, Reason: "successful outcome or frequent access"})
			promoted++
		}
	}
	demoted, err = m.applyTierRebalance(ctx, workspace, memories)
	if err != nil {
		return evicted, promoted, demoted, summarized, err
	}

	if len(memories) <= m.maxEntries {
		return evicted, promoted, demoted, summarized, nil
	}
	over := len(memories) - m.maxEntries
	toDelete := make([]string, 0, over)
	toDeleteSet := make(map[string]struct{}, over)
	candidates := append([]core.MemoryEntry(nil), memories...)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].DecayScore > candidates[j].DecayScore })
	for _, mm := range candidates {
		if len(toDelete) >= over {
			break
		}
		if mm.Pinned || mm.Type == core.ProceduralMemory {
			continue
		}
		if mm.DecayScore < 0.65 {
			continue
		}
		toDelete = append(toDelete, mm.ID)
		toDeleteSet[mm.ID] = struct{}{}
	}

	// Archive original content (gzip) before eviction so it can be recovered.
	if m.archive != nil {
		for _, mm := range memories {
			if _, ok := toDeleteSet[mm.ID]; !ok {
				continue
			}
			rec := ArchiveRecord{
				MemoryID:    mm.ID,
				Workspace:   mm.Workspace,
				Type:        string(mm.Type),
				Content:     mm.Content,
				Entities:    mm.Entities,
				Tags:        mm.Tags,
				Confidence:  mm.Confidence,
				StorageTier: string(mm.StorageTier),
				ArchivedAt:  time.Now().UTC(),
			}
			// Non-fatal: if archiving fails, proceed with eviction anyway.
			_ = m.archive.Store(rec)
		}
	}

	// Before evicting, summarize eligible memories into the cold tier so key
	// facts survive even after the original is purged.
	if m.summarizer != nil {
		for _, mm := range memories {
			if _, ok := toDeleteSet[mm.ID]; !ok {
				continue
			}
			result, serr := m.summarizer.Summarize(ctx, mm)
			if serr != nil {
				// Non-fatal: proceed to evict without a cold summary.
				continue
			}
			wr, werr := m.pipeline.Write(ctx, WriteInput{
				Workspace: workspace,
				Type:      mm.Type,
				Content:   result.Summary,
				Source:    core.MemorySource{Type: core.SourceConsolidation},
				Tags:      mm.Tags,
				Entities:  mm.Entities,
				Mode:      ExtractFast,
			})
			if werr == nil {
				// Override router-assigned tier to cold.
				_ = m.store.UpdateTier(ctx, wr.ID, core.TierCold)
				_ = m.store.AddTierTransition(ctx, wr.ID, wr.StorageTier, core.TierCold, "cold summary created before eviction")
				_, _ = m.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: workspace, Operation: "archive", Outcome: "success", Actor: "lifecycle", Source: "cold_summary", TargetType: "memory", TargetIDs: []string{wr.ID}, Reason: "created before eviction"})
				summarized++
			}
		}
	}

	for _, mm := range memories {
		if _, ok := toDeleteSet[mm.ID]; !ok {
			continue
		}
		_ = m.store.AddTombstone(ctx, mm, "evict", "")
	}
	if err := m.store.DeleteByIDsAudited(ctx, toDelete, sqlite.AuditEventInput{Workspace: workspace, Operation: "retention_evict", Outcome: "success", Actor: "lifecycle", Source: "eviction", Reason: "workspace entry limit"}); err != nil {
		return evicted, promoted, demoted, summarized, err
	}
	evicted = len(toDelete)
	return evicted, promoted, demoted, summarized, nil
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
				_, _ = m.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: workspace, Operation: "demote", Outcome: "success", Actor: "lifecycle", Source: "tier_rebalance", TargetType: "memory", TargetIDs: []string{mm.ID}, Reason: "low access and staleness"})
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
			_, _ = m.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: workspace, Operation: "promote", Outcome: "success", Actor: "lifecycle", Source: "tier_rebalance", TargetType: "memory", TargetIDs: []string{mm.ID}, Reason: "markdown lifecycle policy"})
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
		_, _ = m.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{Workspace: workspace, Operation: "demote", Outcome: "success", Actor: "lifecycle", Source: "markdown_budget", TargetType: "memory", TargetIDs: []string{mm.ID}, Reason: "markdown token budget"})
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
