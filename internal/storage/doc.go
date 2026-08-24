// Package storage provides persistence adapters for agent-memory's multi-tier storage system.
//
// # Overview
//
// The storage package implements the physical storage layer for agent-memory.
// It provides adapters for different storage tiers, each optimized for specific
// access patterns and memory types.
//
// # Storage Tier Strategy
//
// agent-memory uses a hybrid storage architecture with five tiers:
//
//	Markdown Tier (markdown/):
//	  - Always-loaded facts in Markdown format
//	  - Zero retrieval cost (loaded at session start)
//	  - Token budget enforced (default: 4000 tokens)
//	  - Managed section within AGENTS.md or similar
//	  - Best for: Pinned conventions, critical rules, project facts
//	  - Implementation: File-based with atomic writes
//
//	Vector Tier (sqlite/ vector tables):
//	  - Semantic search via embedding similarity
//	  - SQLite-backed with FTS5 for text search
//	  - Local-first, deterministic, reproducible
//	  - Best for: Semantic memories, procedural knowledge
//	  - Implementation: SQLite with embeddings stored as BLOB
//
//	Vector+Graph Tier (sqlite/ with relationships):
//	  - Combines vector search with relationship traversal
//	  - Graph edges stored in memory_relations table
//	  - Enables structural queries (call graphs, dependencies)
//	  - Best for: Outcome memories, service architectures
//	  - Implementation: SQLite with separate relations table
//
//	Document Tier (sqlite/ document table):
//	  - Cold storage for large episodic content
//	  - Full text search but not embedded
//	  - Referenced by other tiers via memory IDs
//	  - Best for: Session transcripts, large analyses
//	  - Implementation: SQLite with FTS5 virtual table
//
//	Cold Tier (sqlite/ tombstones + reconstruction):
//	  - Evicted memories marked with tombstones
//	  - Enables "tip of the tongue" gap detection
//	  - Reconstruction engine can recover from fragments
//	  - Best for: Graceful forgetting with recovery option
//	  - Implementation: Tombstone table + lineage tracking
//
// # SQLite Schema Overview
//
// The SQLite store uses several tables:
//
//	memories:
//	  Primary table for all memory entries.
//	  Columns: id, type, content, embedding (BLOB), workspace, source,
//	           confidence, created_at, updated_at, last_accessed, access_count,
//	           decay_score, salience_score, suppression_score, storage_tier,
//	           useful_count, ignored_count, rejected_count, harmful_count,
//	           superseded_by, pinned, promoted_at, demoted_at, importance
//
//	memory_relations:
//	  Graph edges between memories.
//	  Columns: source_id, target_id, relation_type, weight, metadata (JSON)
//	  Indexes: (source_id), (target_id), (relation_type)
//
//	memory_outcomes:
//	  Outcome data for outcome-type memories.
//	  Columns: memory_id, result, approach, reason, linked_memories (JSON)
//
//	memory_documents:
//	  Document tier storage with full-text search.
//	  Uses SQLite FTS5 virtual table for fast text queries.
//	  Columns: id, workspace, content, created_at
//
//	tombstones:
//	  Records of evicted memories for reconstruction.
//	  Columns: original_id, content_hash, evicted_at, reason,
//	           original_type, original_workspace, metadata (JSON)
//
//	reconstruction_lineage:
//	  Tracks which tombstones were used to reconstruct memories.
//	  Prevents reconstruction loops.
//	  Columns: reconstructed_id, tombstone_id, created_at
//
//	observations:
//	  Tool usage and agent action observations.
//	  Columns: id, workspace, session_id, occurred_at, kind,
//	           tool_name, summary, hash, created_at
//	  Deduplication via hash with 60-second window
//
//	sessions:
//	  Session metadata for grouping observations.
//	  Columns: id, workspace, started_at, ended_at, status,
//	           last_heartbeat_at, task_summary
//
//	llm_usage_metrics:
//	  Token consumption tracking for cost analysis.
//	  Columns: id, workspace, timestamp, group_tag, model,
//	           prompt_tokens, completion_tokens, total_tokens
//
//	benchmark_runs:
//	  Benchmark results for performance tracking.
//	  Columns: id, workspace, run_id, mode, created_at,
//	           config (JSON), summary (JSON)
//
// # Migration Approach
//
// The SQLite store uses schema versioning with migrations:
//
//  1. Each schema change gets a migration file (migrations/*.sql)
//  2. Migrations are numbered sequentially (001_initial.sql, 002_add_column.sql)
//  3. Applied automatically on store initialization
//  4. Tracked in schema_migrations table
//
// The store checks the schema version on open and applies pending migrations.
// This ensures safe upgrades across agent-memory versions.
//
// # File Locations
//
// Storage files are organized per workspace:
//
//	User-level:
//	  ~/.agent-memory/                      # User directory
//	  ~/.agent-memory/config.yaml           # User config
//	  ~/.agent-memory/models/               # Embedding models
//
//	Workspace-level:
//	  .agent-memory.yaml                    # Workspace config
//	  ~/.agent-memory/workspaces/<name>/    # Workspace data
//	  └── memories.db                       # SQLite database
//	  ~/.agent-memory/workspaces/<name>/markdown/
//	  └── AGENTS.md                         # Markdown tier
//
// # Markdown Adapter
//
// The markdown adapter (markdown/adapter.go) manages the markdown tier:
//
//	Features:
//	  - Atomic writes via temp file + rename
//	  - Token budget enforcement with eviction strategy
//	  - Preserves non-managed content in AGENTS.md
//	  - Sectioned format: ## agent-memory [id] ... ## /agent-memory
//
//	Usage:
//	  adapter := markdown.NewAdapter("AGENTS.md", 4000)
//	  err := adapter.Upsert("mem-123", "This is a critical fact")
//	  err = adapter.Remove("mem-456")
//
//	Budget Management:
//	  When the markdown file exceeds maxTokens, the adapter:
//	  1. Counts tokens in managed sections
//	  2. If over budget, removes oldest unpinned entries
//	  3. Preserves pinned entries and non-managed content
//	  4. Returns error if pinned entries alone exceed budget
//
// # SQLite Store
//
// The SQLite store (sqlite/store.go) provides the main persistence layer:
//
//	Initialization:
//	  store, err := sqlite.NewStore(dbPath)
//	  // Automatically runs migrations and creates indexes
//
//	Memory Operations:
//	  Insert(ctx, memory) error
//	  Get(ctx, id) (MemoryEntry, error)
//	  Update(ctx, id, patch) error
//	  Delete(ctx, id) error
//	  List(ctx, filters) ([]MemoryEntry, error)
//
//	Relationship Operations:
//	  AddRelation(ctx, sourceID, targetID, relType, weight, metadata) error
//	  ListRelations(ctx, sourceID) ([]Relation, error)
//	  TraverseRelations(ctx, startID, relTypes, maxDepth) ([]MemoryEntry, error)
//
//	Lifecycle Operations:
//	  UpdateDecayScores(ctx, workspace) (int, error)
//	  MarkSuperseded(ctx, sourceIDs, successorID) error
//	  UpdateTier(ctx, id, tier) error
//	  DeleteByIDs(ctx, ids) error
//
//	Observation Tracking:
//	  InsertObservation(ctx, observation) error
//	  ListObservations(ctx, workspace, sessionID, limit) ([]Observation, error)
//	  UpsertSession(ctx, sessionInput) error
//	  ListSessions(ctx, workspace, limit) ([]Session, error)
//
//	Metrics:
//	  AddLLMUsageMetric(ctx, metric) error
//	  AggregateLLMUsageTotals(ctx, workspace) (LLMUsageTotals, error)
//	  AggregateLLMUsageByGroup(ctx, workspace) ([]GroupTotals, error)
//
// # Transactions
//
// The store supports transactions for atomic multi-step operations:
//
//	err := store.InTransaction(ctx, func(tx *sql.Tx) error {
//	    // Multiple operations within transaction
//	    if err := store.InsertWithTx(ctx, tx, memory1); err != nil {
//	        return err
//	    }
//	    if err := store.InsertWithTx(ctx, tx, memory2); err != nil {
//	        return err
//	    }
//	    return nil  // Commits on success, rolls back on error
//	})
//
// # Concurrency
//
// The SQLite store is safe for concurrent access:
//   - Write-ahead logging (WAL) mode enabled
//   - Connection pool with prepared statements
//   - Row-level locking for updates
//   - Optimistic locking for tier changes
//
// Best practices:
//   - Use separate stores per workspace for parallelism
//   - Keep transactions short
//   - Use batched operations for bulk writes
//
// # Performance
//
// The storage layer is optimized for local-first performance:
//
//	Indexes:
//	  - Primary key index on id
//	  - Index on (workspace, type) for filtered queries
//	  - Index on (workspace, storage_tier) for tier queries
//	  - Index on (source_id) and (target_id) for relationship traversal
//	  - FTS5 index on document content
//
//	Query Optimization:
//	  - Prepared statements for common queries
//	  - Batch inserts for lifecycle operations
//	  - Lazy loading of embeddings (only when needed)
//	  - Cursor-based pagination for large result sets
//
//	Benchmarks (1000 memories):
//	  - Insert: ~1ms per memory (including indexes)
//	  - Get by ID: ~0.1ms
//	  - List with filters: ~5-10ms
//	  - Full-text search: ~10-20ms (depends on query)
//	  - Relationship traversal (depth 2): ~15-30ms
//
// # Testing
//
// The storage package has extensive tests:
//   - Unit tests for each adapter (markdown/adapter_test.go)
//   - Integration tests for SQLite operations (sqlite/*_test.go)
//   - Migration tests (sqlite/migrations_test.go)
//   - Performance benchmarks (sqlite/benchmark_test.go)
//
// Run tests: go test ./internal/storage/...
// Run with race detector: go test -race ./internal/storage/...
//
// # Usage Example
//
//	// Initialize SQLite store
//	store, err := sqlite.NewStore("~/.agent-memory/workspaces/my-project/memories.db")
//	if err != nil {
//	    return fmt.Errorf("failed to open store: %w", err)
//	}
//	defer store.Close()
//
//	// Insert a memory
//	mem := core.MemoryEntry{
//	    ID:          uuid.NewString(),
//	    Type:        core.SemanticMemory,
//	    Content:     "Database connection string uses env var DB_URL",
//	    Workspace:   "my-project",
//	    StorageTier: core.TierVector,
//	    Confidence:  0.85,
//	    CreatedAt:   time.Now(),
//	    UpdatedAt:   time.Now(),
//	}
//	if err := store.Insert(ctx, mem); err != nil {
//	    return fmt.Errorf("insert failed: %w", err)
//	}
//
//	// Query memories
//	memories, err := store.List(ctx, core.SearchFilters{
//	    Workspace: "my-project",
//	    Types:     []core.MemoryType{core.SemanticMemory},
//	})
//
//	// Initialize markdown adapter
//	mdAdapter := markdown.NewAdapter(".agent-memory/AGENTS.md", 4000)
//	err = mdAdapter.Upsert("critical-rule", "Always use JWT for API auth")
package storage
