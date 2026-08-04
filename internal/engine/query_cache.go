package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

// QueryCache implements TTL-based caching for query embeddings and retrieval results.
// Caching dramatically reduces latency for repeated queries within a session.
//
// Keys are deterministic truncated SHA-256 digests, so raw query text is never
// retained in the LRU map keys; the full identity of each entry is stored inside
// the value and verified on read to guard against hash collisions. The TTL acts
// as a backstop for stale entries that are not invalidated explicitly (see
// InvalidateWorkspace and the LifecycleManager.OnWorkspaceChange hook).
type QueryCache struct {
	embeddingCache *lruCache // normalized-query digest → CachedEmbedding
	resultCache    *lruCache // retrieval-options digest → CachedResult
	ttl            time.Duration
	mu             sync.RWMutex
	enabled        bool
}

// CachedEmbedding wraps an embedding with expiration metadata and the normalized
// query it was computed for, verified on read against the requested query.
type CachedEmbedding struct {
	Quantized *embeddings.QuantizedVector
	Key       string // normalized query text the embedding was computed for
	CachedAt  time.Time
	ExpiresAt time.Time
}

// CachedResult wraps retrieval results with expiration metadata and the full
// retrieval-options key, verified on read against the requested options.
type CachedResult struct {
	Hits      []RetrievalHit
	Key       string // full retrieval-options identity string
	CachedAt  time.Time
	ExpiresAt time.Time
}

// QueryCacheConfig configures cache behavior.
type QueryCacheConfig struct {
	Enabled             bool
	TTL                 time.Duration
	MaxEmbeddingEntries int
	MaxResultEntries    int
}

// DefaultQueryCacheConfig returns recommended cache settings.
func DefaultQueryCacheConfig() QueryCacheConfig {
	return QueryCacheConfig{
		Enabled:             true,
		TTL:                 5 * time.Minute,
		MaxEmbeddingEntries: 1000,
		MaxResultEntries:    500,
	}
}

// NewQueryCache creates a new query cache with the given configuration.
func NewQueryCache(cfg QueryCacheConfig) *QueryCache {
	if !cfg.Enabled {
		return &QueryCache{enabled: false}
	}

	return &QueryCache{
		embeddingCache: newLRUCache(cfg.MaxEmbeddingEntries),
		resultCache:    newLRUCache(cfg.MaxResultEntries),
		ttl:            cfg.TTL,
		enabled:        true,
	}
}

// GetEmbedding retrieves a cached embedding for the query text.
// Returns nil if not found or expired.
func (c *QueryCache) GetEmbedding(ctx context.Context, queryText string) []float32 {
	if !c.enabled {
		return nil
	}

	query := normalizeQuery(queryText)
	key := embeddingCacheKey(query)

	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.embeddingCache.get(key)
	if !ok {
		observability.GetRegistry().CacheMisses.WithLabelValues("embedding").Inc()
		return nil
	}

	cached := entry.(CachedEmbedding)
	if cached.Key != query {
		// Truncated-hash collision: treat as a miss rather than returning the
		// embedding for a different query.
		observability.GetRegistry().CacheMisses.WithLabelValues("embedding").Inc()
		return nil
	}
	if time.Now().After(cached.ExpiresAt) {
		// Expired - will be cleaned up on next write
		observability.GetRegistry().CacheMisses.WithLabelValues("embedding").Inc()
		return nil
	}

	observability.GetRegistry().CacheHits.WithLabelValues("embedding").Inc()
	vector, err := embeddings.DequantizeTurbo(cached.Quantized)
	if err != nil {
		return nil
	}
	return vector
}

// SetEmbedding stores an embedding in the cache.
func (c *QueryCache) SetEmbedding(ctx context.Context, queryText string, vector []float32) {
	if !c.enabled {
		return
	}

	query := normalizeQuery(queryText)
	key := embeddingCacheKey(query)

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	qvec, err := embeddings.QuantizeTurbo(vector)
	if err != nil {
		return
	}

	cached := CachedEmbedding{
		Quantized: qvec,
		Key:       query,
		CachedAt:  now,
		ExpiresAt: now.Add(c.ttl),
	}
	c.embeddingCache.set(key, cached)
	observability.GetRegistry().CacheSize.WithLabelValues("embedding").Set(float64(c.embeddingCache.size()))
}

// GetResults retrieves cached retrieval results.
// Returns nil if not found or expired.
func (c *QueryCache) GetResults(ctx context.Context, opt RetrievalOptions) []RetrievalHit {
	if !c.enabled {
		return nil
	}

	fullKey := resultCacheKey(opt)
	key := cacheKeyDigest(fullKey)

	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.resultCache.get(key)
	if !ok {
		observability.GetRegistry().CacheMisses.WithLabelValues("result").Inc()
		return nil
	}

	cached := entry.(CachedResult)
	if cached.Key != fullKey {
		// Truncated-hash collision: treat as a miss rather than returning the
		// results for a different retrieval.
		observability.GetRegistry().CacheMisses.WithLabelValues("result").Inc()
		return nil
	}
	if time.Now().After(cached.ExpiresAt) {
		// Expired - will be cleaned up on next write
		observability.GetRegistry().CacheMisses.WithLabelValues("result").Inc()
		return nil
	}

	observability.GetRegistry().CacheHits.WithLabelValues("result").Inc()

	// Return a copy of the hits slice to prevent future external mutations
	hitsCopy := make([]RetrievalHit, len(cached.Hits))
	copy(hitsCopy, cached.Hits)
	return hitsCopy
}

// SetResults stores retrieval results in the cache.
func (c *QueryCache) SetResults(ctx context.Context, opt RetrievalOptions, hits []RetrievalHit) {
	if !c.enabled {
		return
	}

	fullKey := resultCacheKey(opt)
	key := cacheKeyDigest(fullKey)

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	// Create a copy of the hits slice to prevent future external mutations
	hitsCopy := make([]RetrievalHit, len(hits))
	copy(hitsCopy, hits)

	cached := CachedResult{
		Hits:      hitsCopy,
		Key:       fullKey,
		CachedAt:  now,
		ExpiresAt: now.Add(c.ttl),
	}
	c.resultCache.set(key, cached)
	observability.GetRegistry().CacheSize.WithLabelValues("result").Set(float64(c.resultCache.size()))
}

// InvalidateWorkspace removes all cached results for a workspace.
// Call this after writes to ensure fresh results. Embedding entries are
// workspace-independent and are intentionally left intact.
func (c *QueryCache) InvalidateWorkspace(workspace string) {
	if !c.enabled {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// LRU map keys are digests, so match on the full key stored inside each
	// CachedResult value (always prefixed "ws=<workspace>|").
	prefix := fmt.Sprintf("ws=%s|", workspace)
	c.resultCache.invalidateByValue(func(value interface{}) bool {
		if cached, ok := value.(CachedResult); ok {
			return strings.HasPrefix(cached.Key, prefix)
		}
		return false
	})
}

// Stats returns cache performance metrics.
func (c *QueryCache) Stats() CacheStats {
	if !c.enabled {
		return CacheStats{Enabled: false}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		Enabled:          true,
		EmbeddingEntries: c.embeddingCache.size(),
		ResultEntries:    c.resultCache.size(),
		EmbeddingHits:    c.embeddingCache.hits,
		EmbeddingMisses:  c.embeddingCache.misses,
		ResultHits:       c.resultCache.hits,
		ResultMisses:     c.resultCache.misses,
	}
}

// CacheStats provides cache performance metrics.
type CacheStats struct {
	Enabled          bool
	EmbeddingEntries int
	ResultEntries    int
	EmbeddingHits    int64
	EmbeddingMisses  int64
	ResultHits       int64
	ResultMisses     int64
}

// HitRate returns the cache hit rate (0.0 to 1.0).
func (s CacheStats) EmbeddingHitRate() float64 {
	total := s.EmbeddingHits + s.EmbeddingMisses
	if total == 0 {
		return 0
	}
	return float64(s.EmbeddingHits) / float64(total)
}

// ResultHitRate returns the result cache hit rate (0.0 to 1.0).
func (s CacheStats) ResultHitRate() float64 {
	total := s.ResultHits + s.ResultMisses
	if total == 0 {
		return 0
	}
	return float64(s.ResultHits) / float64(total)
}

// normalizeQuery canonicalizes query text for cache keys: surrounding whitespace
// is trimmed and interior whitespace runs collapse to single spaces, so
// "  foo\t bar " and "foo bar" share one cache entry.
func normalizeQuery(q string) string {
	return strings.Join(strings.Fields(q), " ")
}

// cacheKeyDigest returns a deterministic truncated SHA-256 digest (128 bits,
// hex-encoded) of the given key material. Cache map keys are digests, so raw
// query text is never retained in keys; callers verify the full key stored in
// the value on read to guard against collisions.
func cacheKeyDigest(material string) string {
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:16])
}

// embeddingCacheKey derives the LRU key for an embedding: a digest of the
// normalized query text. Embeddings are workspace-independent (the same text
// yields the same vector regardless of workspace or mode), so the key
// deliberately excludes workspace and mode to maximize reuse.
func embeddingCacheKey(queryText string) string {
	return cacheKeyDigest(normalizeQuery(queryText))
}

// resultCacheKey builds the full identity string for retrieval options: it
// carries workspace, normalized query, mode, and every filter that affects
// results. It is stored inside each CachedResult for equality checks and
// workspace invalidation; the LRU map key is cacheKeyDigest of this string.
func resultCacheKey(opt RetrievalOptions) string {
	var outcome any
	if opt.Filters.OutcomeResult != nil {
		outcome = *opt.Filters.OutcomeResult
	}
	var minConf, minDecay float64
	if opt.Filters.MinConfidence != nil {
		minConf = *opt.Filters.MinConfidence
	}
	if opt.Filters.MinDecayScore != nil {
		minDecay = *opt.Filters.MinDecayScore
	}
	var dateFrom, dateTo string
	if opt.Filters.DateFrom != nil {
		dateFrom = opt.Filters.DateFrom.Format(time.RFC3339)
	}
	if opt.Filters.DateTo != nil {
		dateTo = opt.Filters.DateTo.Format(time.RFC3339)
	}

	var minSem, minTotal, relCutoff, weakSem, weakTotal, weakCutoff float64
	if opt.Policy.MinSemanticScore != nil {
		minSem = *opt.Policy.MinSemanticScore
	}
	if opt.Policy.MinTotalScore != nil {
		minTotal = *opt.Policy.MinTotalScore
	}
	if opt.Policy.RelativeScoreCutoff != nil {
		relCutoff = *opt.Policy.RelativeScoreCutoff
	}
	if opt.Policy.WeakSemanticScore != nil {
		weakSem = *opt.Policy.WeakSemanticScore
	}
	if opt.Policy.WeakTotalScore != nil {
		weakTotal = *opt.Policy.WeakTotalScore
	}
	if opt.Policy.WeakRelativeCutoff != nil {
		weakCutoff = *opt.Policy.WeakRelativeCutoff
	}

	return fmt.Sprintf("ws=%s|q=%s|k=%d|m=%s|d=%d|types=%v|tiers=%v|outcome=%v|conf=%.4f|decay=%.4f|entities=%v|from=%s|to=%s|min_sem=%.4f|min_total=%.4f|rel=%.4f|w_sem=%.4f|w_total=%.4f|w_rel=%.4f",
		opt.Workspace,
		normalizeQuery(opt.Query),
		opt.TopK,
		opt.Mode,
		opt.Depth,
		opt.Filters.Types,
		opt.Filters.Tiers,
		outcome,
		minConf,
		minDecay,
		opt.Filters.Entities,
		dateFrom,
		dateTo,
		minSem,
		minTotal,
		relCutoff,
		weakSem,
		weakTotal,
		weakCutoff,
	)
}

// lruCache is a simple LRU cache implementation.
type lruCache struct {
	items   map[string]*lruNode
	head    *lruNode
	tail    *lruNode
	maxSize int
	hits    int64
	misses  int64
	mu      sync.Mutex
}

type lruNode struct {
	key   string
	value interface{}
	prev  *lruNode
	next  *lruNode
}

func newLRUCache(maxSize int) *lruCache {
	head := &lruNode{}
	tail := &lruNode{}
	head.next = tail
	tail.prev = head

	return &lruCache{
		items:   make(map[string]*lruNode, maxSize),
		head:    head,
		tail:    tail,
		maxSize: maxSize,
	}
}

func (c *lruCache) get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	node, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}

	c.hits++
	c.moveToFront(node)
	return node.value, true
}

func (c *lruCache) set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if node, ok := c.items[key]; ok {
		node.value = value
		c.moveToFront(node)
		return
	}

	node := &lruNode{key: key, value: value}
	c.items[key] = node
	c.addToFront(node)

	if len(c.items) > c.maxSize {
		c.removeLRU()
	}
}

func (c *lruCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *lruCache) moveToFront(node *lruNode) {
	c.removeNode(node)
	c.addToFront(node)
}

func (c *lruCache) addToFront(node *lruNode) {
	node.next = c.head.next
	node.prev = c.head
	c.head.next.prev = node
	c.head.next = node
}

func (c *lruCache) removeNode(node *lruNode) {
	node.prev.next = node.next
	node.next.prev = node.prev
}

func (c *lruCache) removeLRU() {
	lru := c.tail.prev
	if lru == c.head {
		return
	}
	c.removeNode(lru)
	delete(c.items, lru.key)
}

func (c *lruCache) invalidateByValue(filter func(value interface{}) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, node := range c.items {
		if filter(node.value) {
			c.removeNode(node)
			delete(c.items, key)
		}
	}
}
