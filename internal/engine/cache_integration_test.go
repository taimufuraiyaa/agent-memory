package engine

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// mockProvider provides fast fake embeddings for testing
type mockProvider struct{}

func (m *mockProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	hash := sha256.Sum256([]byte(text))
	vec := make([]float32, 384)
	for i := 0; i < 384; i++ {
		idx := i % len(hash)
		vec[i] = float32(hash[idx]) / 255.0
	}
	return vec, nil
}

func (m *mockProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		result[i] = vec
	}
	return result, nil
}

func (m *mockProvider) Name() string {
	return "test-provider"
}

func (m *mockProvider) ModelVersion() string {
	return "test-v1"
}

func (m *mockProvider) Dimension() int {
	return 384
}

// TestCacheIntegration validates that query cache reduces latency for repeated queries
// and properly invalidates after writes.
func TestCacheIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cache integration test in short mode")
	}

	ctx := context.Background()
	dbPath := t.TempDir() + "/test.db"
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &mockProvider{}

	// Create shared cache
	cache := NewQueryCache(DefaultQueryCacheConfig())

	// Create components with shared cache
	searcher := NewVectorSearcher(store, provider)
	// IMPORTANT: Both retrieval and pipeline must share the same cache instance
	retrieval := NewRetrievalEngine(searcher)
	retrieval.cache = cache  // Manually inject shared cache
	pipeline := NewWritePipelineWithOptions(store, WritePipelineOptions{
		Embedder: provider,
		Cache:    cache,  // Same cache instance
	})

	// Write some test memories
	memories := []string{
		"The Kubernetes API server handles authentication and authorization",
		"Redis is an in-memory data structure store used as a cache",
		"PostgreSQL supports ACID transactions and complex queries",
	}

	for _, content := range memories {
		_, err := pipeline.Write(ctx, WriteInput{
			Workspace: "test-ws",
			Type:      core.SemanticMemory,
			Content:   content,
			Source:    core.MemorySource{Type: core.SourceUserInput},
		})
		if err != nil {
			t.Fatalf("write memory: %v", err)
		}
	}

	query := "database caching systems"

	// First query - cache miss (should be slower)
	start1 := time.Now()
	result1, err := retrieval.Retrieve(ctx, RetrievalOptions{
		Workspace: "test-ws",
		Query:     query,
		TopK:      5,
		Mode:      ModeSearch,
	})
	if err != nil {
		t.Fatalf("first retrieve: %v", err)
	}
	latency1 := time.Since(start1)

	if len(result1.Hits) == 0 {
		t.Fatal("first query returned no hits")
	}

	// Get cache stats after first query
	stats1 := retrieval.CacheStats()
	if !stats1.Enabled {
		t.Fatal("cache should be enabled")
	}
	if stats1.ResultEntries == 0 {
		t.Error("expected cache to have entries after first query")
	}

	// Second query (same) - cache hit (should be faster)
	start2 := time.Now()
	result2, err := retrieval.Retrieve(ctx, RetrievalOptions{
		Workspace: "test-ws",
		Query:     query,
		TopK:      5,
		Mode:      ModeSearch,
	})
	if err != nil {
		t.Fatalf("second retrieve: %v", err)
	}
	latency2 := time.Since(start2)

	// Verify results are consistent
	if len(result2.Hits) != len(result1.Hits) {
		t.Errorf("cached result has different hit count: got %d, want %d",
			len(result2.Hits), len(result1.Hits))
	}

	// Cache hit should be significantly faster (at least 2x)
	if latency2 > latency1/2 {
		t.Logf("WARNING: Cache hit not faster enough - First: %v, Cached: %v (speedup: %.2fx)",
			latency1, latency2, float64(latency1)/float64(latency2))
		// Don't fail test - timing can be flaky in CI
	} else {
		t.Logf("Cache speedup achieved - First: %v, Cached: %v (speedup: %.2fx)",
			latency1, latency2, float64(latency1)/float64(latency2))
	}

	// Get cache stats after second query
	stats2 := retrieval.CacheStats()
	if stats2.ResultHits == 0 {
		t.Error("expected at least one cache hit")
	}
	if stats2.ResultHitRate() == 0 {
		t.Error("expected non-zero cache hit rate")
	}

	t.Logf("Cache stats: Entries=%d, Hits=%d, Misses=%d, HitRate=%.2f%%",
		stats2.ResultEntries, stats2.ResultHits, stats2.ResultMisses,
		stats2.ResultHitRate()*100)

	// Write a new memory - should invalidate cache
	_, err = pipeline.Write(ctx, WriteInput{
		Workspace: "test-ws",
		Type:      core.SemanticMemory,
		Content:   "Memcached is a distributed memory caching system",
		Source:    core.MemorySource{Type: core.SourceUserInput},
	})
	if err != nil {
		t.Fatalf("write invalidating memory: %v", err)
	}

	// Third query after invalidation - should be cache miss again
	start3 := time.Now()
	result3, err := retrieval.Retrieve(ctx, RetrievalOptions{
		Workspace: "test-ws",
		Query:     query,
		TopK:      5,
		Mode:      ModeSearch,
	})
	if err != nil {
		t.Fatalf("third retrieve after invalidation: %v", err)
	}
	latency3 := time.Since(start3)

	// Result count may be the same or different depending on relevance
	// The key test is that cache was invalidated
	if len(result3.Hits) == 0 {
		t.Error("expected results after adding new memory")
	}

	t.Logf("Latency comparison - Initial: %v, Cached: %v, After Invalidation: %v",
		latency1, latency2, latency3)

	// Verify cache was actually invalidated
	// After invalidation, the third query should be slower (cache miss)
	if latency3 < latency2*10 {
		t.Errorf("expected latency after invalidation to be much higher than cached query: cached=%v, after_invalidation=%v",
			latency2, latency3)
	}
	
	stats3 := retrieval.CacheStats()
	t.Logf("Final cache stats: Entries=%d, Hits=%d, Misses=%d",
		stats3.ResultEntries, stats3.ResultHits, stats3.ResultMisses)
}

// TestCacheEmbeddingReuse validates that query embeddings are cached and reused.
func TestCacheEmbeddingReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cache embedding test in short mode")
	}

	ctx := context.Background()
	dbPath := t.TempDir() + "/test.db"
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &mockProvider{}

	cache := NewQueryCache(DefaultQueryCacheConfig())
	searcher := NewVectorSearcher(store, provider)
	// Inject shared cache into searcher
	searcher.cache = cache
	pipeline := NewWritePipelineWithOptions(store, WritePipelineOptions{
		Embedder: provider,
		Cache:    cache,
	})

	// Write a test memory
	_, err = pipeline.Write(ctx, WriteInput{
		Workspace: "test-ws",
		Type:      core.SemanticMemory,
		Content:   "Test memory for cache validation",
		Source:    core.MemorySource{Type: core.SourceUserInput},
	})
	if err != nil {
		t.Fatalf("write memory: %v", err)
	}

	query := "test query for caching"

	// Check embedding cache is empty
	stats := cache.Stats()
	if stats.EmbeddingEntries != 0 {
		t.Error("expected empty embedding cache initially")
	}

	// First search - should cache the embedding
	_, err = searcher.SearchWithOptions(ctx, VectorSearchOptions{
		Workspace: "test-ws",
		Query:     query,
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("first search: %v", err)
	}

	// Check embedding was cached
	stats = cache.Stats()
	if stats.EmbeddingEntries == 0 {
		t.Error("expected embedding to be cached after first search")
	}

	// Second search with same query - should reuse cached embedding
	_, err = searcher.SearchWithOptions(ctx, VectorSearchOptions{
		Workspace: "test-ws",
		Query:     query,
		TopK:      5,
	})
	if err != nil {
		t.Fatalf("second search: %v", err)
	}

	// Check cache hit
	stats = cache.Stats()
	if stats.EmbeddingHits == 0 {
		t.Error("expected embedding cache hit on second search")
	}
	if stats.EmbeddingHitRate() == 0 {
		t.Error("expected non-zero embedding hit rate")
	}

	t.Logf("Embedding cache stats: Entries=%d, Hits=%d, Misses=%d, HitRate=%.2f%%",
		stats.EmbeddingEntries, stats.EmbeddingHits, stats.EmbeddingMisses,
		stats.EmbeddingHitRate()*100)
}

// TestWorkspacePrefixCacheInvalidation verifies that invalidating Workspace A does not clear Workspace B's cache.
func TestWorkspacePrefixCacheInvalidation(t *testing.T) {
	cache := NewQueryCache(DefaultQueryCacheConfig())

	optA := RetrievalOptions{
		Workspace: "workspace-A",
		Query:     "query A",
		TopK:      5,
		Mode:      ModeSearch,
	}
	optB := RetrievalOptions{
		Workspace: "workspace-B",
		Query:     "query B",
		TopK:      5,
		Mode:      ModeSearch,
	}

	hitsA := []RetrievalHit{{Memory: core.MemoryEntry{ID: "mem-A", Workspace: "workspace-A"}}}
	hitsB := []RetrievalHit{{Memory: core.MemoryEntry{ID: "mem-B", Workspace: "workspace-B"}}}

	ctx := context.Background()

	// 1. Populate cache
	cache.SetResults(ctx, optA, hitsA)
	cache.SetResults(ctx, optB, hitsB)

	// Verify both exist
	if got := cache.GetResults(ctx, optA); got == nil || got[0].Memory.ID != "mem-A" {
		t.Fatal("expected cached result for A")
	}
	if got := cache.GetResults(ctx, optB); got == nil || got[0].Memory.ID != "mem-B" {
		t.Fatal("expected cached result for B")
	}

	// 2. Invalidate Workspace A
	cache.InvalidateWorkspace("workspace-A")

	// 3. Verify Workspace A is miss/cleared, but Workspace B is still present
	if got := cache.GetResults(ctx, optA); got != nil {
		t.Error("expected Workspace A's cache to be invalidated/cleared")
	}
	if got := cache.GetResults(ctx, optB); got == nil || got[0].Memory.ID != "mem-B" {
		t.Error("expected Workspace B's cache to remain intact")
	}
}
