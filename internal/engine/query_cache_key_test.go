package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// TestQueryCacheKeysOmitRawQueryText verifies that neither cache retains raw
// query text in its map keys: keys must be opaque digests, not substrings of the
// query (sensitive queries must not be recoverable from key inspection).
func TestQueryCacheKeysOmitRawQueryText(t *testing.T) {
	cache := NewQueryCache(DefaultQueryCacheConfig())

	sensitive := "secret=hunter2 password=swordfish"
	opt := RetrievalOptions{Workspace: "ws", Query: sensitive, TopK: 5, Mode: ModeSearch}

	cache.SetEmbedding(context.Background(), sensitive, make([]float32, 384))
	cache.SetResults(context.Background(), opt, []RetrievalHit{{Memory: core.MemoryEntry{ID: "m1"}}})

	checkKeys := func(c *lruCache, label string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if len(c.items) == 0 {
			t.Fatalf("%s cache is empty", label)
		}
		for key := range c.items {
			if strings.Contains(key, "hunter2") || strings.Contains(key, "swordfish") || strings.Contains(key, "secret=") {
				t.Errorf("%s cache key %q contains raw query text", label, key)
			}
			if len(key) != 32 {
				t.Errorf("%s cache key %q is not a 32-char hex digest (len %d)", label, key, len(key))
			}
			for _, r := range key {
				if !strings.ContainsRune("0123456789abcdef", r) {
					t.Errorf("%s cache key %q is not hex-encoded", label, key)
					break
				}
			}
		}
	}

	checkKeys(cache.embeddingCache, "embedding")
	checkKeys(cache.resultCache, "result")
}

// TestQueryCacheNormalizedQueryHits verifies that queries differing only in
// whitespace share cache entries, for both the embedding and result caches.
func TestQueryCacheNormalizedQueryHits(t *testing.T) {
	cache := NewQueryCache(DefaultQueryCacheConfig())
	ctx := context.Background()

	// Embedding cache: set with messy spacing, read with clean spacing.
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = float32(i%7) / 7.0
	}
	cache.SetEmbedding(ctx, "  how   do I  deploy \t safely ", vec)
	if got := cache.GetEmbedding(ctx, "how do I deploy safely"); got == nil {
		t.Error("expected embedding cache hit for whitespace-normalized query")
	}

	// Result cache: same normalization applies.
	optMessy := RetrievalOptions{Workspace: "ws", Query: "  cache   invalidation ", TopK: 3, Mode: ModeSearch}
	optClean := RetrievalOptions{Workspace: "ws", Query: "cache invalidation", TopK: 3, Mode: ModeSearch}
	cache.SetResults(ctx, optMessy, []RetrievalHit{{Memory: core.MemoryEntry{ID: "m1"}}})
	if got := cache.GetResults(ctx, optClean); got == nil || got[0].Memory.ID != "m1" {
		t.Error("expected result cache hit for whitespace-normalized query")
	}

	// Unrelated query must miss.
	if got := cache.GetEmbedding(ctx, "unrelated query"); got != nil {
		t.Error("expected cache miss for unrelated query")
	}
	if got := cache.GetResults(ctx, RetrievalOptions{Workspace: "ws", Query: "unrelated", TopK: 3, Mode: ModeSearch}); got != nil {
		t.Error("expected result cache miss for unrelated query")
	}
}

func TestGraphCacheIdentityInvalidatesResultForRevisionReviewDeletionOrConfigurationChange(t *testing.T) {
	cache := NewQueryCache(DefaultQueryCacheConfig())
	ctx := context.Background()
	base := RetrievalOptions{Workspace: "ws", Query: "related memories", TopK: 3, Mode: ModeRecall, GraphCacheIdentity: "graph-epoch-1"}
	cache.SetResults(ctx, base, []RetrievalHit{{Memory: core.MemoryEntry{ID: "m1"}}})
	if got := cache.GetResults(ctx, base); len(got) != 1 {
		t.Fatalf("expected graph cache baseline hit: %#v", got)
	}
	changed := base
	changed.GraphCacheIdentity = "graph-epoch-2"
	if got := cache.GetResults(ctx, changed); got != nil {
		t.Fatalf("stale graph epoch returned cached context: %#v", got)
	}
}
