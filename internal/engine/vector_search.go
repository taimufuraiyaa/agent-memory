package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

// SearchHit is a single ranked retrieval item.
type SearchHit struct {
	Memory core.MemoryEntry
	Score  float64
}

// VectorSearcher provides semantic retrieval; currently brute-force cosine.
type VectorSearcher struct {
	store    *sqlite.Store
	provider embeddings.Provider
}

// VectorSearchOptions controls semantic search behavior and filtering.
type VectorSearchOptions struct {
	Workspace string
	Query     string
	TopK      int
	Types     []core.MemoryType
	Tiers     []core.StorageTier
}

// NewVectorSearcher builds a semantic searcher.
func NewVectorSearcher(store *sqlite.Store, provider embeddings.Provider) *VectorSearcher {
	return &VectorSearcher{store: store, provider: provider}
}

// Search runs brute-force semantic search over workspace entries.
func (s *VectorSearcher) Search(ctx context.Context, workspace, query string, topK int) ([]SearchHit, error) {
	return s.SearchWithOptions(ctx, VectorSearchOptions{
		Workspace: workspace,
		Query:     query,
		TopK:      topK,
	})
}

// SearchWithOptions runs semantic search with optional memory filters.
func (s *VectorSearcher) SearchWithOptions(ctx context.Context, opt VectorSearchOptions) ([]SearchHit, error) {
	if opt.TopK <= 0 {
		opt.TopK = 5
	}
	qv, err := s.provider.Embed(ctx, opt.Query)
	if err != nil {
		return nil, err
	}
	memories, err := s.store.ListMemoriesByWorkspace(ctx, opt.Workspace)
	if err != nil {
		return nil, err
	}
	memoryByID := make(map[string]core.MemoryEntry, len(memories))
	for _, m := range memories {
		memoryByID[m.ID] = m
	}
	sqlScores, err := s.store.SearchMemoryVectorsSQL(ctx, opt.Workspace, qv, opt.TopK, opt.Types, opt.Tiers)
	if err == nil && len(sqlScores) > 0 {
		out := make([]SearchHit, 0, len(sqlScores))
		for _, sc := range sqlScores {
			m, ok := memoryByID[sc.MemoryID]
			if !ok {
				continue
			}
			out = append(out, SearchHit{Memory: m, Score: sc.Score})
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	cachedVectors, err := s.store.ListMemoryVectorsByWorkspace(ctx, opt.Workspace)
	if err != nil {
		return nil, err
	}
	hits := make([]SearchHit, 0, len(memories))
	for _, m := range memories {
		if !matchMemoryFilters(m, opt.Types, opt.Tiers) {
			continue
		}
		mv := cachedVectors[m.ID]
		if len(mv) == 0 {
			text := m.Content
			if m.Diagram != nil && strings.TrimSpace(m.Diagram.Code) != "" {
				text = strings.TrimSpace(text) + "\n" + m.Diagram.Code
			}
			mv, err = s.provider.Embed(ctx, text)
			if err != nil {
				return nil, err
			}
			// Best-effort cache write; retrieval should still succeed without cache.
			_ = s.store.UpsertMemoryVector(ctx, m.ID, m.Workspace, mv)
		}
		score, err := embeddings.Cosine(qv, mv)
		if err != nil {
			return nil, err
		}
		hits = append(hits, SearchHit{Memory: m, Score: score})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > opt.TopK {
		return hits[:opt.TopK], nil
	}
	return hits, nil
}

// MarkAccessed updates access counters for retrieved IDs.
func (s *VectorSearcher) MarkAccessed(ctx context.Context, ids []string) error {
	return s.store.MarkAccessed(ctx, ids, time.Now().UTC())
}

func matchMemoryFilters(m core.MemoryEntry, types []core.MemoryType, tiers []core.StorageTier) bool {
	if len(types) > 0 {
		found := false
		for _, t := range types {
			if m.Type == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(tiers) > 0 {
		found := false
		for _, t := range tiers {
			if m.StorageTier == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
