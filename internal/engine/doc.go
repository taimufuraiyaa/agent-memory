// Package engine implements retrieval, write pipeline, and lifecycle orchestration for agent-memory.
//
// # Overview
//
// The engine package is the core runtime of agent-memory. It handles:
//   - Memory retrieval with ranked scoring
//   - Write pipeline for ingesting new memories
//   - Lifecycle management (decay, consolidation, conflict resolution)
//   - Export and reconstruction operations
//
// # Retrieval Pipeline
//
// Retrieval is a multi-stage ranked retrieval process:
//
//  1. Candidate Generation:
//     - Semantic search using embedding similarity
//     - Optional filtering by type, tier, confidence, etc.
//     - Generates initial candidate set from storage
//
//  2. Multi-Signal Scoring:
//     Each candidate is scored using weighted signals:
//
//     Semantic:  Embedding cosine similarity (0.0 - 1.0)
//     Recency:   Time-based score favoring recently updated memories
//     Outcome:   Boost for successful outcomes (mode-dependent)
//     Decay:     Penalty for old/stale memories (0.0 - 1.0)
//     Tier Bias: Preference for certain tiers (markdown = 1.0, vector = 0.35, etc.)
//     Salience:  Boost for frequently accessed memories
//     Suppression: Penalty for rejected/harmful memories
//
//     Final score = weighted sum of all signals
//
//  3. Mode-Specific Weights:
//     Different retrieval modes use different signal weights:
//
//     search:  Semantic-focused (semantic: 0.55, recency: 0.20)
//     recall:  Balanced (semantic: 0.45, recency: 0.25, decay: 0.10)
//     relate:  Graph-focused (traverses relationships)
//     outcomes: Outcome-focused (outcome signal weighted heavily)
//
//  4. Ranking & Cutoffs:
//     - Candidates sorted by final score (descending)
//     - Minimum thresholds applied (min_semantic_score, min_total_score)
//     - Relative cutoff: drop items <X% of best score
//     - Result: strong_hits (confident), weak_hits (uncertain), clipped (budget exceeded)
//
//  5. Budget Management (recall mode):
//     - Token budget enforced for session context
//     - Memories added until budget exhausted
//     - Clipped items reported with reason
//
// Retrieval is explainable: each hit includes a score breakdown showing
// contribution of each signal. Enable with explain=true in retrieval options.
//
// # Scoring Algorithm
//
// The scoring algorithm combines multiple signals with configurable weights:
//
//	func computeScore(signals WeightedSignals, memory Memory) float64 {
//	    semanticScore := cosineSimilarity(query, memory.Embedding)
//	    recencyScore := computeRecency(memory.UpdatedAt)
//	    decayScore := memory.DecayScore  // from decay engine
//	    tierBias := getTierBias(memory.StorageTier)
//
//	    total := (signals.Semantic * semanticScore) +
//	             (signals.Recency * recencyScore) +
//	             (signals.Decay * (1.0 - decayScore)) +  // invert: lower decay = higher score
//	             (signals.TierBias * tierBias) +
//	             (signals.Outcome * outcomeSignal(memory))
//
//	    // Apply suppression penalty
//	    total *= (1.0 - memory.SuppressionScore)
//
//	    return total
//	}
//
// Weights are tunable via adaptive tuning (see core.AdaptivePolicy).
//
// # Write Pipeline
//
// The WritePipeline handles memory ingestion:
//
//  1. Content Validation:
//     - Non-empty content
//     - Valid memory type
//     - Workspace exists
//
//  2. Embedding Generation:
//     - Generate embedding vector via embeddings provider
//     - Normalize to unit length
//
//  3. Confidence Estimation:
//     - Base confidence from source type (user_input: 0.7, code_analysis: 0.8)
//     - Check for contradictions with existing memories
//     - Adjust based on outcome result (success: +0.15, failure: -0.10)
//
//  4. Tier Routing:
//     - Outcome memories → vector+graph (need relationship traversal)
//     - Procedural memories → vector (need semantic search)
//     - Large episodic content → document tier
//     - Pinned/important → markdown (always loaded)
//
//  5. Storage:
//     - Insert into appropriate storage tier(s)
//     - Update graph relationships if applicable
//     - Record creation timestamp and source
//
// The pipeline is transactional: failures roll back the entire write.
//
// # Lifecycle Management (REM Cycle)
//
// The lifecycle manager performs periodic maintenance:
//
//	Decay Engine (decay.go):
//	  - Computes decay scores for all memories
//	  - Type-specific half-lives (episodic: 7d, semantic: 30d, procedural: 90d)
//	  - Boosts for recent access, pins, successful outcomes
//	  - Formula: decay = exp(-ln(2) * age / halfLife) * boosts
//
//	Consolidation Engine (consolidation.go):
//	  - Merges similar episodic memories into semantic facts
//	  - Clusters by content similarity (overlap threshold)
//	  - Generates consolidated content via merge strategies
//	  - Creates derived_from relationships
//
//	Deep Consolidation Engine (deep_consolidation.go):
//	  - Cross-type consolidation for advanced patterns
//	  - Outcome pattern mining (find repeated failures)
//	  - Procedural workflow extraction
//	  - Requires higher similarity thresholds
//
//	Conflict Engine (conflict.go):
//	  - Detects contradictory memories
//	  - Resolves conflicts by confidence, recency, feedback
//	  - Marks loser as superseded
//	  - Creates contradicts relationship for audit trail
//
//	Promotion/Demotion (lifecycle_manager.go):
//	  - Promotes high-value memories to higher tiers
//	  - Demotes low-value memories to lower tiers
//	  - Maintains markdown tier budget (token limit)
//	  - Evicts fully decayed memories to cold storage
//
// Lifecycle runs on schedule or manually via CLI: agent-memory lifecycle run
//
// # Forgetting & Reconstruction
//
// Forgetting is intentional to prevent memory bloat:
//
//	Tombstones:
//	  When a memory is evicted, a small tombstone remains with:
//	  - Original ID and content hash
//	  - Creation and eviction timestamps
//	  - Minimal metadata for gap detection
//
//	Reconstruction Engine (reconstruction.go):
//	  - Detects "tip of the tongue" queries (no strong hits but tombstone matches)
//	  - Proposes reconstruction from tombstone + related memories
//	  - Creates reconstructed semantic memory with lower confidence
//	  - Safeguards against reconstruction loops
//
// Reconstruction is opt-in: enable via retrieval options or CLI flag.
//
// # Export
//
// The export module (export.go) provides multiple formats:
//
//	ExportBundle: JSON structure with all memories, relationships, metadata
//	Markdown: Sectioned document grouped by type with outcome formatting
//	CSV: Tabular format for spreadsheet analysis
//
// Usage: agent-memory export --format json > backup.json
//
// # Key Components
//
//	WritePipeline: Handles memory ingestion with validation, embedding, routing
//	RetrievalEngine: Implements ranked retrieval with explainable scoring
//	DecayEngine: Computes and updates decay scores
//	ConsolidationEngine: Merges similar memories
//	ConflictEngine: Resolves contradictions
//	LifecycleManager: Orchestrates all lifecycle operations
//	ReconstructionEngine: Recovers forgotten memories from tombstones
//
// # Usage Example
//
//	// Initialize write pipeline
//	pipeline := engine.NewWritePipeline(store, embedder)
//
//	// Write a new memory
//	result, err := pipeline.Write(ctx, engine.WriteInput{
//	    Type:      core.SemanticMemory,
//	    Content:   "The API uses JWT tokens for authentication",
//	    Workspace: "my-project",
//	    Source:    core.MemorySource{Type: core.SourceUserInput},
//	})
//	if err != nil {
//	    return fmt.Errorf("write failed: %w", err)
//	}
//
//	// Retrieve memories
//	retrieval := engine.NewRetrievalEngine(store, embedder)
//	hits, err := retrieval.Search(ctx, engine.SearchInput{
//	    Query:     "how does authentication work",
//	    Workspace: "my-project",
//	    TopK:      10,
//	    Explain:   true,  // include score breakdowns
//	})
//
//	// Run lifecycle maintenance
//	lifecycle := engine.NewLifecycleManager(store, pipeline)
//	report, err := lifecycle.RunCycle(ctx, "my-project")
//	fmt.Printf("Consolidated: %d, Conflicts resolved: %d, Evicted: %d\n",
//	    report.Consolidated, report.ConflictsResolved, report.Evicted)
//
// # Performance
//
// The engine is optimized for local-first operation:
//   - SQLite with FTS5 for fast text search
//   - In-memory embedding cache to reduce recomputation
//   - Batch operations for lifecycle management
//   - Concurrent write pipeline (lock-free for different workspaces)
//
// Benchmarks (see *_test.go files):
//   - Retrieval: ~5-10ms for 1000 memories (local embeddings)
//   - Write: ~20-30ms including embedding generation
//   - Decay update: ~100-200ms for 10,000 memories
//   - Consolidation: ~500ms for 1000 episodic memories
//
// # Testing
//
// The engine package has comprehensive tests:
//   - e2e_test.go: End-to-end lifecycle flows
//   - *_test.go: Unit tests for each component
//   - Benchmarks for performance-critical paths
//
// Run tests: go test ./internal/engine/...
// Run benchmarks: go test -bench=. ./internal/engine/...
package engine
