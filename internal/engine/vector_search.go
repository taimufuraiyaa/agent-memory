package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
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
	cache    *QueryCache
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
	return &VectorSearcher{
		store:    store,
		provider: provider,
		cache:    NewQueryCache(DefaultQueryCacheConfig()),
	}
}

// Store returns the underlying SQLite store.
func (s *VectorSearcher) Store() *sqlite.Store {
	return s.store
}

// Provider returns the underlying embedding provider.
func (s *VectorSearcher) Provider() embeddings.Provider {
	return s.provider
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

	// Check embedding cache first to avoid re-embedding repeated queries
	var qv []float32
	if s.cache != nil {
		qv = s.cache.GetEmbedding(ctx, opt.Query)
	}

	// Cache miss - compute embedding and store it
	if qv == nil {
		var err error
		qv, err = s.provider.Embed(ctx, opt.Query)
		if err != nil {
			return nil, err
		}
		if s.cache != nil {
			s.cache.SetEmbedding(ctx, opt.Query, qv)
		}
	}
	activeProvider := strings.TrimSpace(s.provider.Name())

	// Fast path: Go-based vector search over binary SQLite blobs
	sqlScores, err := s.store.SearchMemoryVectorsGo(ctx, opt.Workspace, activeProvider, qv, opt.TopK, opt.Types, opt.Tiers)
	if err == nil && len(sqlScores) > 0 {
		ids := make([]string, len(sqlScores))
		for i, sc := range sqlScores {
			ids[i] = sc.MemoryID
		}

		matchingMemories, err := s.store.GetMemoriesByIDs(ctx, ids)
		if err == nil && len(matchingMemories) > 0 {
			out := make([]SearchHit, 0, len(sqlScores))
			for _, sc := range sqlScores {
				m, ok := matchingMemories[sc.MemoryID]
				if !ok {
					continue
				}
				out = append(out, SearchHit{Memory: m, Score: sc.Score})
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}

	// Fallback path: load all memories and compute similarity brute-force
	memories, err := s.store.ListMemoriesByWorkspace(ctx, opt.Workspace)
	if err != nil {
		return nil, err
	}
	cachedRows, err := s.store.ListMemoryVectorRowsByWorkspace(ctx, opt.Workspace)
	if err != nil {
		return nil, err
	}
	cachedByID := make(map[string]sqlite.MemoryVectorRow, len(cachedRows))
	for _, row := range cachedRows {
		cachedByID[row.MemoryID] = row
	}
	hits := make([]SearchHit, 0, len(memories))
	for _, m := range memories {
		if !matchMemoryFilters(m, opt.Types, opt.Tiers) {
			continue
		}
		row, ok := cachedByID[m.ID]
		mv := row.Embedding
		if !ok || len(mv) == 0 || strings.TrimSpace(row.EmbeddingProvider) != activeProvider {
			text := memoryVectorText(m)
			mv, err = s.provider.Embed(ctx, text)
			if err != nil {
				return nil, err
			}
			// Best-effort cache write; retrieval should still succeed without cache.
			_ = s.store.UpsertMemoryVector(ctx, m.ID, m.Workspace, activeProvider, s.provider.ModelVersion(), mv)
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
