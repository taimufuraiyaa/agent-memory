package engine

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
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

// TestLifecycleOnWorkspaceChangeHookFiresAfterRun verifies that the
// OnWorkspaceChange invalidation hook fires with the maintained workspace when
// Run completes.
func TestLifecycleOnWorkspaceChangeHookFiresAfterRun(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "lifecycle-hook.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pipe := NewWritePipeline(store)
	if _, err := pipe.Write(ctx, WriteInput{
		Workspace: "hook-ws",
		Type:      core.SemanticMemory,
		Content:   "orders service emits order.created event",
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis},
	}); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	lm := NewLifecycleManager(store, pipe)
	var mu sync.Mutex
	var fired []string
	lm.OnWorkspaceChange = func(ws string) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, ws)
	}

	if _, err := lm.Run(ctx, "hook-ws"); err != nil {
		t.Fatalf("lifecycle run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 {
		t.Fatalf("expected hook to fire exactly once after Run, fired %d times: %v", len(fired), fired)
	}
	if fired[0] != "hook-ws" {
		t.Errorf("hook fired with workspace %q, want %q", fired[0], "hook-ws")
	}
}
