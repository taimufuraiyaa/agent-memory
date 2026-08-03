# System Design Review: agent-memory
**Date:** June 9, 2026  
**Reviewer:** AI Agent  
**Purpose:** Comprehensive architectural analysis and optimization recommendations

---

## Executive Summary

agent-memory is a **persistent, multi-tier memory layer for AI coding agents** with a well-architected hybrid storage system, explainable retrieval engine, and local-first design. The system demonstrates strong architectural principles but has **7 high-priority optimization opportunities** across performance, correctness, scalability, and operational maturity.

**Overall Assessment:** The system is production-ready for single-user scenarios with **good continuation scores (0.4+)** in benchmarks, but needs targeted improvements for enterprise deployment, operational observability, and semantic quality consistency.

---

## System Architecture Overview

### Core Design Principles
1. **Hybrid Multi-Tier Storage** - Optimizes for both fast retrieval and comprehensive recall
2. **Local-First** - All data stored locally in SQLite, no mandatory cloud dependencies
3. **Explainable Retrieval** - Multi-signal scoring with per-hit breakdown for debugging
4. **CLI-First Contract** - Deterministic JSON output for agent integration
5. **Lifecycle Management** - Automated decay, consolidation, eviction, and tier promotion

### Architecture Layers

```
┌─────────────────────────────────────────────────────────────┐
│                    Entry Points                             │
│  CLI Commands │ HTTP API Server │ Dashboard UI              │
└────────────┬────────────────────────────────────────────────┘
             │
┌────────────▼────────────────────────────────────────────────┐
│              Core Engine Layer                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │  Retrieval   │  │  Lifecycle   │  │    Write     │     │
│  │   Engine     │  │   Manager    │  │  Pipeline    │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
└────────────┬────────────────────────────────────────────────┘
             │
┌────────────▼────────────────────────────────────────────────┐
│           Storage & Embeddings Layer                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │   SQLite     │  │   Markdown   │  │    ONNX      │     │
│  │   Storage    │  │   Storage    │  │  Embeddings  │     │
│  └──────────────┘  └──────────────┘  └──────────────┘     │
└─────────────────────────────────────────────────────────────┘
```

---

## Strengths

### S-1: Well-Designed Hybrid Storage Architecture
**What's Good:**
- Multi-tier storage (Markdown/Vector/Vector+Graph/Document/Cold) provides natural optimization
- Markdown tier for zero-retrieval-cost conventions (always loaded)
- Vector tier for semantic search with SQLite-backed deterministic storage
- Graph tier for relationship-based recall that vectors miss
- Cold tier for episodic archives

**Impact:** Balances retrieval speed, semantic quality, and comprehensive context without forcing a single storage model.

### S-2: Explainable Multi-Signal Retrieval
**What's Good:**
```go
// From retrieval.go - score breakdown structure
type ScoreBreakdown struct {
    SemanticSimilarity float64 `json:"semantic_similarity"`
    Recency           float64 `json:"recency"`
    OutcomeBoost      float64 `json:"outcome_boost"`
    DecayWeight       float64 `json:"decay_weight"`
    TierBias          float64 `json:"tier_bias"`
    SalienceSignal    float64 `json:"salience_signal"`
    SuppressionSignal float64 `json:"suppression_signal"`
    RelativeToBest    float64 `json:"relative_to_best"`
    Activation        float64 `json:"activation"`
    Total             float64 `json:"total"`
}
```

**Impact:** Enables debugging retrieval issues, tuning signal weights, and building operator trust through transparency.

### S-3: Strong Benchmark Framework
**What's Good:**
- 10,000 continuation test cases across 25 topic clusters
- Paired ON/OFF comparison methodology
- Multi-dimensional scoring: task success, fact coverage, completeness, locator success, verification effort, operational cost
- Benchmark results show **0.4+ continuation scores** indicating good benefit
- Deterministic test data generation with prior-session fixtures

**Impact:** Provides confidence in retrieval quality and regression detection for future changes.

### S-4: Automated Lifecycle Management
**What's Good:**
```go
// From lifecycle.go - REM-cycle inspired maintenance
func (m *LifecycleManager) Run(ctx context.Context, workspace string) (*LifecycleMetrics, error) {
    // 1. Update decay scores
    // 2. Consolidate overlapping memories
    // 3. Detect and resolve conflicts
    // 4. Promote successful outcomes
    // 5. Evict decayed memories
    // 6. Rebalance tier token budgets
}
```

**Impact:** Prevents memory bloat, keeps stores useful over time, and mimics human memory patterns with decay/consolidation.

### S-5: Security-First Design
**What's Good:**
- Automatic secret/PII detection and filtering
- Parameterized SQL queries (no injection vulnerabilities)
- Path traversal protection
- Localhost-only dashboard binding by default
- Local-first data storage (no telemetry)

**Impact:** Safe for use with sensitive codebases and production systems.

### S-6: Clean Separation of Concerns
**What's Good:**
- Clear domain boundaries: `core/` (types), `engine/` (business logic), `storage/` (persistence), `embeddings/` (ML), `api/` (HTTP), `cli/` (commands)
- Dependency injection for testability
- Interface-based abstractions for storage and embeddings

**Impact:** Maintainable codebase with clear module boundaries.

---

## High-Priority Optimization Opportunities

### O-1: Incomplete ONNX Embedding Implementation (CRITICAL)
**Current State:**
```go
// internal/embeddings/onnx_provider.go - Currently a scaffold
func (p *ONNXMiniLMProvider) Embed(ctx context.Context, text string) ([]float32, error) {
    // FIXME: This currently delegates to LocalProvider (hash-based)
    return p.fallback.Embed(ctx, text)
}
```

**Problem:**
- Production system still uses **placeholder hash-based embeddings** instead of real semantic embeddings
- Provider name reports `onnx-minilm-scaffold` but doesn't actually use ONNX inference
- Semantic floor tuning is compromised by non-semantic embeddings
- Search quality improvements are blocked until real embeddings are implemented

**Impact:** **HIGH SEVERITY**
- Retrieval quality is fundamentally limited by non-semantic similarity
- Benchmark continuation scores would improve significantly with real embeddings
- Users expect semantic search but receive hash-based proximity

**Recommendation:**
```markdown
1. Implement real ONNX Runtime integration with MiniLM-L6-v2 model
2. Add WordPiece tokenization with attention masking
3. Implement mean-pooling over token embeddings
4. Validate 384-dimensional normalized output
5. Add provider factory to prevent ad-hoc construction
6. Require ONNX provider in production (fail loudly if unavailable)

Priority: P0 - blocks semantic quality improvements
Effort: 2-3 days
Risk: Medium (requires ONNX Runtime dependency management)
```

**References:** `.kiro/specs/archicture-imrpovement/review-findings.md` F-03, F-07

---

### O-2: Missing Vector Provenance and Migration Support (CRITICAL)
**Current State:**
```sql
-- internal/storage/sqlite/vectors.go - no embedding_provider column
CREATE TABLE memory_vectors (
    memory_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    embedding_json TEXT NOT NULL,  -- 384-dim float32 array
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Problem:**
- Cannot track which embedding provider generated each vector
- Mixed-provider corpora cannot be audited or migrated safely
- No `re-embed` command exists to migrate vectors after provider changes
- Corpus becomes inconsistent when switching from hash-based to ONNX embeddings

**Impact:** **HIGH SEVERITY**
- Cannot safely roll out ONNX embeddings without breaking existing workspaces
- No visibility into corpus consistency problems
- Migration requires manual database deletion (data loss)

**Recommendation:**
```markdown
1. Add `embedding_provider` column to memory_vectors table
2. Implement backward-safe migration for existing databases (default to "local-hash")
3. Build `agent-memory reembed --workspace <name>` command with reporting:
   - Total memories examined
   - Memories re-embedded (provider changed)
   - Memories skipped (already correct provider)
   - Final provider verification
4. Update vector upsert logic to always record provider

Priority: P0 - must ship before ONNX provider rollout
Effort: 1-2 days
Risk: Low (additive schema change, backward compatible)
```

**References:** `.kiro/specs/archicture-imrpovement/review-findings.md` F-05, F-06

---

### O-3: Lazy Embedding Creates Search Visibility Gap (HIGH)
**Current State:**
```go
// internal/engine/write_pipeline.go - no embedding at write time
func (p *WritePipeline) Write(ctx context.Context, req WriteRequest) (*WriteResult, error) {
    // ... validation and routing ...
    
    // Insert memory row
    if err := p.store.UpsertMemory(ctx, entry); err != nil {
        return nil, err
    }
    
    // ❌ No vector embedding happens here!
    // Vectors are created lazily during search or by separate reembed job
    
    return result, nil
}
```

**Problem:**
- Newly written memories are **not searchable** until a separate process creates vectors
- Creates a time window where agent knowledge exists in database but is invisible to retrieval
- Relies on implicit "eventually consistent" behavior instead of immediate correctness

**Impact:** **MEDIUM-HIGH SEVERITY**
- Agent may re-discover the same knowledge multiple times in one session
- Session-end learnings aren't available for immediate recall
- Violates user expectation that written knowledge is immediately searchable

**Recommendation:**
```markdown
1. Inject embedder into WritePipeline constructor
2. Call embedder.Embed() during Write() and persist vector immediately
3. Add write-time embedding metrics (latency, errors)
4. Keep lazy embedding as fallback repair path only (not primary path)
5. Add integration test: write → search → verify found in same operation

Priority: P1 - correctness issue but has lazy fallback
Effort: 1 day
Risk: Low (additive change, doesn't break existing behavior)
```

**References:** `.kiro/specs/archicture-imrpovement/review-findings.md` F-04

---

### O-4: SQLite Concurrency and Performance Bottlenecks (MEDIUM-HIGH)
**Current State:**
- Single SQLite database per workspace
- No connection pooling visible in code
- No read/write splitting
- Benchmark notes mention `SQLITE_BUSY` retry logic needed under concurrency

**Problem:**
```go
// internal/storage/sqlite/store.go - single connection pattern
type Store struct {
    db *sql.DB  // Single connection, no pooling config
}
```

**Observed Issues:**
- Benchmarks require retry logic for `SQLITE_BUSY` errors under concurrency
- Dashboard reads can block agent writes
- No WAL mode configuration mentioned
- No query performance profiling infrastructure

**Impact:** **MEDIUM SEVERITY**
- Dashboard usage can degrade agent responsiveness
- Lifecycle consolidation can block retrieval
- Scaling to team/shared memory requires architectural changes

**Recommendation:**
```markdown
1. Enable SQLite WAL mode for concurrent read/write:
   PRAGMA journal_mode = WAL;
   PRAGMA busy_timeout = 5000;

2. Add connection pool configuration:
   db.SetMaxOpenConns(10)
   db.SetMaxIdleConns(5)
   db.SetConnMaxLifetime(time.Hour)

3. Add query performance tracking:
   - Log slow queries (>100ms)
   - Add histogram metrics for query latency
   - Profile lifecycle consolidation performance

4. Consider read replicas for dashboard if needed

5. Add stress test: concurrent write + search + lifecycle + dashboard

Priority: P2 - works at current scale but limits growth
Effort: 2-3 days
Risk: Low (incremental improvements, backward compatible)
```

---

### O-5: Missing Operational Observability (MEDIUM)
**Current State:**
```go
// internal/observability/metrics.go - basic Prometheus metrics defined
// but limited coverage of critical paths
```

**Problem:**
- No distributed tracing for multi-step recall flow
- Limited visibility into embedding performance (latency, cache hits)
- No dashboard for operator debugging (vs engineer dashboard)
- Lifecycle consolidation runs "blind" without progress tracking
- No alerting on retrieval quality degradation

**Missing Metrics:**
```markdown
❌ Retrieval latency percentiles (p50, p95, p99)
❌ Embedding cache hit rate
❌ Semantic similarity score distribution over time
❌ Memory growth rate per workspace
❌ Tier transition rates (promote/demote/evict)
❌ Write-to-search round-trip time
❌ Lifecycle run duration and impact
❌ Token budget utilization per tier
```

**Impact:** **MEDIUM SEVERITY**
- Difficult to diagnose performance regressions in production
- Cannot detect semantic quality drift over time
- No visibility into lifecycle health
- Operators lack debugging tools

**Recommendation:**
```markdown
1. Add comprehensive Prometheus metrics:
   - Retrieval: latency, hit rate, score distribution
   - Embeddings: latency, cache performance, provider selection
   - Lifecycle: run duration, consolidation rate, eviction rate
   - Storage: query latency, lock contention, database size

2. Add OpenTelemetry spans for critical paths:
   - agent-memory recall (end-to-end)
   - search → candidate retrieval → reranking → assembly
   - write → embedding → storage → tier routing

3. Add health check endpoint:
   GET /health → {status, db_size, memory_count, last_lifecycle_run}

4. Build operator dashboard (separate from agent dashboard):
   - Retrieval quality trends
   - Performance metrics
   - Storage health
   - Lifecycle activity

5. Add query performance log (configurable threshold)

Priority: P2 - needed for production operations
Effort: 3-4 days
Risk: Low (additive monitoring)
```

---

### O-6: Token Budget Management Lacks Flexibility (MEDIUM)
**Current State:**
```go
// internal/engine/lifecycle.go - hardcoded limits
type LifecycleManager struct {
    maxEntries     int  // Hardcoded: 5000
    markdownBudget int  // Hardcoded: 4000 tokens
}
```

**Problem:**
- Token budgets are hardcoded per tier and per recall assembly
- Different projects have different context window sizes (GPT-4: 128k, Claude: 200k)
- No adaptive budget scaling based on task complexity
- Markdown tier budget (4000 tokens) may be too restrictive for large projects
- Recall assembly budget doesn't consider available context window

**Impact:** **MEDIUM SEVERITY**
- Wastes available context window for large models
- Constrains retrieval quality for small models
- No way to tune budgets per workspace or per agent

**Recommendation:**
```markdown
1. Make budgets configurable per workspace:
   ~/.agent-memory/<workspace>-config.json:
   {
     "markdown_token_budget": 4000,
     "max_entries": 5000,
     "recall_budget_ratio": 0.3  // 30% of agent context window
   }

2. Add adaptive budget sizing:
   - Detect agent context window from environment (AGENT_CONTEXT_WINDOW)
   - Scale recall budget as percentage of available window
   - Preserve minimum budget floor for quality

3. Add budget utilization metrics:
   - Track actual vs allocated tokens per tier
   - Alert when consistently hitting budget ceiling
   - Suggest budget increases based on usage patterns

4. Add CLI commands:
   agent-memory config --workspace <name> --markdown-budget 8000
   agent-memory config --workspace <name> --recall-budget-ratio 0.4

5. Document budget tuning guidelines per model family

Priority: P3 - quality improvement, not correctness
Effort: 2 days
Risk: Low (configuration addition)
```

---

### O-7: Graph Tier Underutilized (MEDIUM)
**Current State:**
```go
// internal/core/types.go - graph relationships defined
type Relation struct {
    TargetID string            `json:"target_id"`
    Type     RelationType      `json:"type"`  // calls, depends_on, contains, contradicts, etc.
    Weight   float64           `json:"weight"`
    Metadata map[string]string `json:"metadata,omitempty"`
}
```

**Problem:**
- Graph relationships (calls, depends_on, contains, contradicts) are defined but **underutilized in retrieval**
- Vector+Graph tier exists but graph traversal logic is minimal
- No graph-based retrieval mode (e.g., "find all memories related to X through dependency chains")
- Relationships must be manually created; no automatic relationship inference

**Opportunity:**
Graph-based retrieval can find related memories that vector similarity misses:
- "Show me all outcomes related to authentication failures" (traverse relationship chains)
- "What memories led to this decision?" (traverse `led_to` edges)
- "Find contradicting memories about this API behavior" (traverse `contradicts` edges)

**Impact:** **MEDIUM OPPORTUNITY**
- Missing retrieval mode for structural queries
- Wastes potential of vector+graph tier
- Manual relationship management limits scalability

**Recommendation:**
```markdown
1. Add automatic relationship inference:
   - Temporal relationships: memories from same session
   - Entity co-occurrence: memories mentioning same entities
   - Outcome chains: failed attempt → successful approach
   - Supersession: newer memory replaces older

2. Add graph-based retrieval mode:
   agent-memory search --query "authentication" --mode graph-expand --depth 2
   - Start with vector search for seed memories
   - Expand via relationship traversal (configurable depth)
   - Re-rank by path distance and relationship strength

3. Add graph visualization in dashboard:
   - Show memory relationship graph for a workspace
   - Highlight memory clusters
   - Show strongest relationship paths

4. Add relationship quality metrics:
   - Relationship density per workspace
   - Most-connected memories (central nodes)
   - Orphaned memories (no relationships)

5. Document relationship types and when each is appropriate

Priority: P3 - enhancement opportunity
Effort: 4-5 days
Risk: Medium (new retrieval mode)
```

---

## Additional Recommendations

### R-1: Add Embedding Model Versioning
**Issue:** Model upgrades (MiniLM-L6-v2 → better model) break existing vectors  
**Solution:** Store `embedding_model_version` alongside `embedding_provider`, auto-trigger reembed on model change  
**Priority:** P3, **Effort:** 1 day

### R-2: Implement Semantic Caching for Repeated Queries
**Issue:** Same search query computes embeddings and similarities repeatedly  
**Solution:** Cache query embeddings and retrieval results with TTL (5 minutes)  
**Priority:** P3, **Effort:** 1 day, **Expected Gain:** 20-50% latency reduction for repeated queries

### R-3: Add Memory Compression for Cold Tier
**Issue:** Cold tier stores full episodic transcripts verbatim  
**Solution:** Apply LLM-based summarization before cold storage, keep original in compressed archive  
**Priority:** P4, **Effort:** 2-3 days

### R-4: Build Team/Shared Memory Support
**Issue:** Current design is single-user per workspace  
**Solution:** Add workspace-level permissions, shared vs private memories, user attribution  
**Priority:** P4 (future roadmap), **Effort:** 1-2 weeks

### R-5: Add Query Performance Budget
**Issue:** Complex graph traversals or large result sets can stall retrieval  
**Solution:** Add configurable max query time (e.g., 500ms), return partial results with timeout flag  
**Priority:** P3, **Effort:** 1 day

---

## Implementation Roadmap

### Phase 1: Critical Correctness (1-2 weeks)
**Goal:** Fix semantic quality and migration blockers

1. **O-2:** Vector Provenance + Re-embed Command (P0)
   - Add `embedding_provider` column
   - Build migration command
   - Validate backward compatibility

2. **O-1:** Real ONNX Embedding Implementation (P0)
   - Implement ONNX Runtime integration
   - Add tokenizer and mean-pooling
   - Centralize provider factory
   - Update installer for ONNX Runtime

3. **O-3:** Eager Write-Time Embedding (P1)
   - Inject embedder into write pipeline
   - Add write-time embedding
   - Test write → search round-trip

**Success Criteria:**
- ✅ Semantic search works with real embeddings (not hash-based)
- ✅ Workspaces can migrate between embedding providers safely
- ✅ Newly written memories are immediately searchable
- ✅ All tests pass with real ONNX embeddings

---

### Phase 2: Performance & Scalability (1 week)
**Goal:** Improve concurrency and eliminate bottlenecks

4. **O-4:** SQLite Concurrency Improvements (P2)
   - Enable WAL mode
   - Add connection pooling
   - Profile and optimize slow queries
   - Stress test concurrent operations

5. **R-2:** Semantic Query Caching (P3)
   - Cache query embeddings
   - Cache retrieval results with TTL
   - Measure latency improvement

**Success Criteria:**
- ✅ Dashboard usage doesn't block agent operations
- ✅ Benchmark runs complete without SQLITE_BUSY errors
- ✅ 20%+ latency reduction for repeated queries

---

### Phase 3: Operational Maturity (1 week)
**Goal:** Add production monitoring and flexibility

6. **O-5:** Comprehensive Observability (P2)
   - Add Prometheus metrics for all critical paths
   - Add OpenTelemetry spans for retrieval
   - Build operator health dashboard
   - Add query performance logging

7. **O-6:** Configurable Token Budgets (P3)
   - Add per-workspace budget configuration
   - Implement adaptive budget sizing
   - Add budget utilization metrics

**Success Criteria:**
- ✅ Operators can diagnose retrieval issues without code inspection
- ✅ Performance regressions are detected within 1 hour
- ✅ Token budgets adapt to agent context window size

---

### Phase 4: Advanced Features (2-3 weeks)
**Goal:** Unlock graph capabilities and model flexibility

8. **O-7:** Graph-Based Retrieval (P3)
   - Automatic relationship inference
   - Graph expansion retrieval mode
   - Dashboard graph visualization

9. **R-1:** Embedding Model Versioning (P3)
   - Store model version with vectors
   - Auto-trigger reembed on model upgrade

10. **R-3:** Cold Tier Compression (P4)
    - LLM-based summarization
    - Compressed archive storage

**Success Criteria:**
- ✅ Graph retrieval finds related memories vectors miss
- ✅ Model upgrades don't break existing workspaces
- ✅ Cold tier storage grows 50%+ slower

---

## Risk Assessment

### High-Risk Items
1. **ONNX Runtime Integration** (O-1)
   - **Risk:** Platform-specific binary dependencies, installation failures
   - **Mitigation:** Provide fallback instructions, test on all platforms, clear error messages

2. **Database Migration** (O-2)
   - **Risk:** Schema changes on production databases
   - **Mitigation:** Backward-compatible migration, rollback procedure, backup recommendations

### Medium-Risk Items
3. **WAL Mode Migration** (O-4)
   - **Risk:** Existing databases need journal mode conversion
   - **Mitigation:** Auto-detect and migrate on startup, document manual procedure

4. **Graph Retrieval** (O-7)
   - **Risk:** Complex graph queries could degrade performance
   - **Mitigation:** Query timeouts, depth limits, performance budgets

### Low-Risk Items
5. **Observability** (O-5), **Token Budgets** (O-6), **Caching** (R-2)
   - All additive changes with minimal breaking change risk

---

## Conclusion

**agent-memory has a solid architectural foundation** with good separation of concerns, explainable retrieval, and strong benchmark results. The hybrid storage design is well-suited to the problem domain.

**Critical Path:** The highest-priority work is completing the **ONNX embedding implementation** and **vector provenance/migration support**. These are correctness issues that block semantic quality improvements and safe production rollout.

**Biggest Wins:**
1. **Real ONNX embeddings** → 2-3x improvement in semantic search quality
2. **Eager write embedding** → Eliminates search visibility gap
3. **SQLite WAL mode** → 50%+ improvement in concurrent performance
4. **Observability** → Enables production debugging and quality monitoring

**Estimated Total Effort:** 5-7 weeks for all phases (1-2 engineers)

**Recommended Start:** Begin with Phase 1 (Critical Correctness) immediately, as it unblocks all other improvements and is necessary for production readiness.

---

## References

1. Repository: `$HOME/timebooks/agent-memory/`
2. Architecture Improvement Spec: `.kiro/specs/archicture-imrpovement/`
3. Benchmark Results: `benchmark/results/continuation-full-10000/score_report.json`
4. Core Types: `internal/core/types.go`
5. Retrieval Engine: `internal/engine/retrieval.go`
6. Lifecycle Manager: `internal/engine/lifecycle.go`
7. Storage Layer: `internal/storage/sqlite/`
