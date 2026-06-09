// Package core defines the domain model for agent-memory's persistent memory system.
//
// # Overview
//
// The core package contains all domain types, business invariants, and error definitions
// used throughout agent-memory. It defines the memory lifecycle, storage tiers, and
// relationships between memories.
//
// # Memory Types
//
// agent-memory supports four types of memories, each with different lifecycle characteristics:
//
//	Episodic:   Raw observations and conversation turns (MemoryType = "episodic")
//	            Half-life: 168 hours (7 days)
//	            Use for: Session transcripts, tool observations, agent actions
//
//	Semantic:   Extracted facts and knowledge (MemoryType = "semantic")
//	            Half-life: 720 hours (30 days)
//	            Use for: Configuration details, architectural decisions, discovered patterns
//
//	Procedural: Checklists, workflows, and how-to knowledge (MemoryType = "procedural")
//	            Half-life: 2160 hours (90 days)
//	            Use for: Build commands, test procedures, deployment workflows
//
//	Outcome:    Records of what worked or failed (MemoryType = "outcome")
//	            Half-life: 1440 hours (60 days)
//	            Use for: Debugging attempts, approach evaluations, lessons learned
//
// # Storage Tiers
//
// Memories move through different storage tiers based on importance and access patterns:
//
//	markdown (TierMarkdown):
//	    Always-loaded facts; zero retrieval cost.
//	    Budget-limited to keep context manageable.
//	    Highest tier for pinned conventions and critical rules.
//
//	vector (TierVector):
//	    Semantic retrieval via embeddings.
//	    Fast similarity search for most queries.
//	    Backed by SQLite with local-first embeddings.
//
//	vector+graph (TierVectorGraph):
//	    Combines vector search with relationship traversal.
//	    Captures structural connections between memories.
//	    Use for service dependencies, call graphs, topic clusters.
//
//	document (TierDocument):
//	    Cold storage for large episodic content.
//	    Referenced by other tiers but not directly retrieved.
//	    Keeps full transcripts without bloating active memory.
//
//	cold (TierCold):
//	    Evicted memories marked with tombstones.
//	    Enables reconstruction when gaps are detected.
//	    "Tip of the tongue" recovery mechanism.
//
// # Lifecycle States
//
// Each MemoryEntry tracks its lifecycle through metadata fields:
//
//	Creation:
//	    CreatedAt: Initial timestamp
//	    Source: Where the memory originated (user_input, agent_observation, etc.)
//	    Confidence: Initial reliability score (0.0 - 1.0)
//
//	Access Tracking:
//	    LastAccessedAt: Most recent retrieval
//	    AccessCount: Total retrievals
//	    UsefulCount/IgnoredCount/RejectedCount: Feedback signals
//
//	Decay & Suppression:
//	    DecayScore: Time-based decay in [0, 1] (higher = more decayed)
//	    SuppressionScore: Penalty for negative feedback
//	    SuppressionUntil: Temporary exclusion from retrieval
//
//	Tier Movement:
//	    StorageTier: Current tier assignment
//	    PromotedAt/DemotedAt: Timestamps of tier changes
//	    Pinned: Prevents automatic demotion
//
//	Supersession:
//	    SupersededBy: ID of memory that replaced this one
//	    Marks outdated information while preserving history
//
// # Relationships
//
// Memories can link to other memories via typed relationships (Relation):
//
//	calls:        Function A calls function B
//	depends_on:   Feature X depends on service Y
//	contains:     Project contains modules
//	contradicts:  Conflicting information detected
//	supersedes:   Newer version replaces older
//	led_to:       Action caused outcome
//	derived_from: Consolidated from source memories
//
// Relationships have weights (0.0 - 1.0) indicating strength and optional metadata.
//
// # Key Types
//
// The primary types and their relationships:
//
//	MemoryEntry: The canonical memory record containing content, embeddings,
//	             metadata, lifecycle state, and optional relationships.
//
//	MemoryType: Enum classifying the kind of memory (episodic, semantic,
//	            procedural, outcome).
//
//	StorageTier: Enum indicating where a memory is stored (markdown, vector,
//	             vector+graph, document, cold).
//
//	Outcome: Structured outcome data attached to outcome-type memories,
//	         recording result (success/failure/partial), approach, and reason.
//
//	Relation: Graph edge linking one memory to another with a typed relationship.
//
//	SearchFilters: Constraints for retrieval queries (types, tiers, confidence, etc.).
//
//	RecallOptions: Parameters for session-start recall (task description, token budget).
//
// # Error Handling
//
// The core package defines structured errors for the entire system:
//
//	ErrNotFound, ErrAlreadyExists, ErrInvalidInput (sentinel errors)
//	WorkspaceError, StorageError, EmbeddingError, RetrievalError, ValidationError (typed errors)
//
// All typed errors support wrapping with errors.Is() and errors.As().
// See errors.go and docs/error-handling-guide.md for detailed guidance.
//
// # Adaptive Tuning
//
// The adaptive_tuning.go file defines runtime configuration for retrieval weights
// and feedback policies. This allows per-workspace or per-mode tuning of the
// retrieval scoring algorithm without code changes.
//
// # Usage Example
//
//	// Create a semantic memory
//	mem := core.MemoryEntry{
//	    ID:          uuid.NewString(),
//	    Type:        core.SemanticMemory,
//	    Content:     "The API server runs on port 8080 by default",
//	    Workspace:   "my-project",
//	    Source:      core.MemorySource{Type: core.SourceUserInput},
//	    Confidence:  0.9,
//	    CreatedAt:   time.Now(),
//	    UpdatedAt:   time.Now(),
//	    StorageTier: core.TierVector,
//	}
//
//	// Validate before storage
//	if err := mem.Validate(); err != nil {
//	    return fmt.Errorf("invalid memory: %w", err)
//	}
//
//	// Add a relationship
//	mem.Relations = []core.Relation{{
//	    TargetID: "config-memory-id",
//	    Type:     core.RelDerivedFrom,
//	    Weight:   0.8,
//	}}
package core
