package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

// DeepConsolidationResult summarises a cross-session consolidation run.
type DeepConsolidationResult struct {
	SessionsScanned    int           `json:"sessions_scanned"`
	MemoriesMerged     int           `json:"memories_merged"`
	ProceduralPromoted int           `json:"procedural_promoted"`
	ConflictsResolved  int           `json:"conflicts_resolved"`
	DurationMs         int64         `json:"duration_ms"`
	DryRun             bool          `json:"dry_run"`
}

// DeepConsolidationOptions controls the deep consolidation pass.
type DeepConsolidationOptions struct {
	Workspace string
	DaysBack  int  // lookback window in days (default 30)
	DryRun    bool // print what would happen without writing
	Mode      MergeMode
}

// DeepConsolidationEngine performs cross-session consolidation.
type DeepConsolidationEngine struct {
	store    *sqlite.Store
	pipeline *WritePipeline
}

// NewDeepConsolidationEngine constructs the engine.
func NewDeepConsolidationEngine(store *sqlite.Store, pipeline *WritePipeline) *DeepConsolidationEngine {
	return &DeepConsolidationEngine{store: store, pipeline: pipeline}
}

// Run executes a deep consolidation pass across sessions.
func (e *DeepConsolidationEngine) Run(ctx context.Context, opts DeepConsolidationOptions) (*DeepConsolidationResult, error) {
	if opts.DaysBack <= 0 {
		opts.DaysBack = 30
	}
	if opts.Mode == "" {
		opts.Mode = MergeFast
	}

	start := time.Now()
	result := &DeepConsolidationResult{DryRun: opts.DryRun}

	cutoff := time.Now().AddDate(0, 0, -opts.DaysBack)

	all, err := e.store.ListMemoriesByWorkspace(ctx, opts.Workspace)
	if err != nil {
		return nil, fmt.Errorf("deep consolidation: list memories: %w", err)
	}

	// Filter to memories within the lookback window that are not superseded.
	recent := make([]core.MemoryEntry, 0, len(all))
	sessionSet := map[string]struct{}{}
	for _, m := range all {
		if m.SupersededBy != nil {
			continue
		}
		if m.CreatedAt.After(cutoff) {
			recent = append(recent, m)
			if m.SessionID != nil && *m.SessionID != "" {
				sessionSet[*m.SessionID] = struct{}{}
			}
		}
	}
	result.SessionsScanned = len(sessionSet)

	// --- Pass 1: Cross-session episodic clustering → semantic merge ---
	episodics := filterByType(recent, core.EpisodicMemory)
	clusters := clusterEpisodes(episodics)
	for _, cluster := range clusters {
		if len(cluster) < 5 {
			// Cross-session merge requires a stronger signal than within-session.
			continue
		}
		if opts.DryRun {
			result.MemoriesMerged += len(cluster)
			continue
		}
		summary := mergeCluster(cluster, opts.Mode)
		wr, err := e.pipeline.Write(ctx, WriteInput{
			Workspace: opts.Workspace,
			Type:      core.SemanticMemory,
			Content:   summary,
			Source:    core.MemorySource{Type: core.SourceConsolidation},
			Mode:      ExtractFast,
		})
		if err != nil {
			return nil, err
		}
		ids := memoryIDs(cluster)
		_ = e.store.MarkSuperseded(ctx, ids, wr.ID)
		result.MemoriesMerged += len(cluster)
	}

	// --- Pass 2: Repeated failure pattern → procedural rule ---
	outcomes := filterByType(recent, core.OutcomeMemory)
	failureGroups := groupFailuresByApproach(outcomes)
	for approach, group := range failureGroups {
		if len(group) < 3 {
			continue
		}
		reason := commonReason(group)
		content := fmt.Sprintf("Avoid approach: %s. Reason: %s (failed %d times across sessions)", approach, reason, len(group))
		if opts.DryRun {
			result.ProceduralPromoted++
			continue
		}
		_, err := e.pipeline.Write(ctx, WriteInput{
			Workspace: opts.Workspace,
			Type:      core.ProceduralMemory,
			Content:   content,
			Source:    core.MemorySource{Type: core.SourceConsolidation},
			Tags:      []string{"promoted-from-failures"},
			Mode:      ExtractFast,
		})
		if err != nil {
			return nil, err
		}
		result.ProceduralPromoted++
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// filterByType returns memories of a specific type.
func filterByType(memories []core.MemoryEntry, t core.MemoryType) []core.MemoryEntry {
	out := make([]core.MemoryEntry, 0)
	for _, m := range memories {
		if m.Type == t {
			out = append(out, m)
		}
	}
	return out
}

// memoryIDs extracts IDs from a slice of memories.
func memoryIDs(memories []core.MemoryEntry) []string {
	ids := make([]string, 0, len(memories))
	for _, m := range memories {
		ids = append(ids, m.ID)
	}
	return ids
}

// groupFailuresByApproach groups outcome memories by their approach field.
func groupFailuresByApproach(outcomes []core.MemoryEntry) map[string][]core.MemoryEntry {
	groups := map[string][]core.MemoryEntry{}
	for _, m := range outcomes {
		if m.Outcome == nil || m.Outcome.Result != core.OutcomeFailure {
			continue
		}
		approach := strings.TrimSpace(m.Outcome.Approach)
		if approach == "" {
			approach = truncate(m.Content, 60)
		}
		groups[approach] = append(groups[approach], m)
	}
	return groups
}

// commonReason picks the most frequent reason string from a group.
func commonReason(group []core.MemoryEntry) string {
	freq := map[string]int{}
	for _, m := range group {
		if m.Outcome != nil && m.Outcome.Reason != "" {
			freq[m.Outcome.Reason]++
		}
	}
	best, bestCount := "", 0
	for r, c := range freq {
		if c > bestCount {
			best, bestCount = r, c
		}
	}
	if best == "" {
		return "repeated failures"
	}
	return best
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
