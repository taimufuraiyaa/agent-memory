package engine

import (
	"context"
	"strings"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type MergeMode string

const (
	MergeFast MergeMode = "fast"
	MergeLLM  MergeMode = "llm-assisted"
)

// ConsolidationEngine merges related episodic memories into semantic summaries.
type ConsolidationEngine struct {
	store    *sqlite.Store
	pipeline *WritePipeline
}

func NewConsolidationEngine(store *sqlite.Store, pipeline *WritePipeline) *ConsolidationEngine {
	return &ConsolidationEngine{store: store, pipeline: pipeline}
}

// Run performs one consolidation pass and returns created summary IDs.
func (e *ConsolidationEngine) Run(ctx context.Context, workspace string, mode MergeMode) ([]string, error) {
	memories, err := e.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return nil, err
	}
	episodes := make([]core.MemoryEntry, 0)
	for _, m := range memories {
		if m.Type == core.EpisodicMemory && m.SupersededBy == nil {
			episodes = append(episodes, m)
		}
	}
	clusters := clusterEpisodes(episodes)
	out := make([]string, 0)
	for _, c := range clusters {
		if len(c) < 2 {
			continue
		}
		summary := mergeCluster(c, mode)
		wr, err := e.pipeline.Write(ctx, WriteInput{
			Workspace: workspace,
			Type:      core.SemanticMemory,
			Content:   summary,
			Source:    core.MemorySource{Type: core.SourceConsolidation},
			Mode:      ExtractFast,
		})
		if err != nil {
			return nil, err
		}
		newID := wr.ID
		oldIDs := make([]string, 0, len(c))
		for _, m := range c {
			oldIDs = append(oldIDs, m.ID)
			_ = e.store.AddRelation(ctx, m.ID, newID, core.RelSupersedes, 1, map[string]string{"reason": "consolidation"})
			_ = e.store.AddRelation(ctx, newID, m.ID, core.RelDerivedFrom, 1, map[string]string{"mode": string(mode)})
		}
		if err := e.store.MarkSuperseded(ctx, oldIDs, newID); err != nil {
			return nil, err
		}
		out = append(out, newID)
	}
	return out, nil
}

func clusterEpisodes(in []core.MemoryEntry) [][]core.MemoryEntry {
	if len(in) == 0 {
		return nil
	}
	used := make([]bool, len(in))
	clusters := make([][]core.MemoryEntry, 0)
	for i := 0; i < len(in); i++ {
		if used[i] {
			continue
		}
		cluster := []core.MemoryEntry{in[i]}
		used[i] = true
		for j := i + 1; j < len(in); j++ {
			if used[j] {
				continue
			}
			if overlap(in[i].Content, in[j].Content) >= 0.35 {
				cluster = append(cluster, in[j])
				used[j] = true
			}
		}
		clusters = append(clusters, cluster)
	}
	return clusters
}

func mergeCluster(cluster []core.MemoryEntry, mode MergeMode) string {
	parts := make([]string, 0, len(cluster))
	for _, m := range cluster {
		parts = append(parts, strings.TrimSpace(m.Content))
	}
	base := "Consolidated memory: " + strings.Join(parts, " | ")
	if mode == MergeLLM {
		// Placeholder path for future LLM-assisted merge.
		return base
	}
	return base
}

func overlap(a, b string) float64 {
	ta := tokenSet(a)
	tb := tokenSet(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	union := map[string]struct{}{}
	for k := range ta {
		union[k] = struct{}{}
		if _, ok := tb[k]; ok {
			inter++
		}
	}
	for k := range tb {
		union[k] = struct{}{}
	}
	return float64(inter) / float64(len(union))
}

func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, t := range strings.Fields(strings.ToLower(s)) {
		if len(t) <= 2 {
			continue
		}
		out[t] = struct{}{}
	}
	return out
}

