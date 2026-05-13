Agent Memory System - Requirements
A persistent, multi-tier memory layer for AI coding agents that retains knowledge across sessions, learns from outcomes, and dramatically reduces repeated research and token consumption.

--------------------------------------------------------------------------------
1. Problem Statement
1.1 The Stateless Agent Problem
Current AI agents (Cursor, Claude Code, Copilot, etc.) operate in a fundamentally stateless mode between sessions. Each new conversation starts from scratch - the agent has no recollection of:
What it studied, what conclusions it reached, what mistakes it made
Which approaches succeeded or failed for this codebase
Codebase-specific conventions, architecture decisions, or team preferences
Relationships between components, services, and domains it previously explored
1.2 The Markdown Workaround and Its Limits
The current best practice - writing study notes to markdown files (.cursorrules, CLAUDE.md, AGENTS.md, study books) - is effective but reaches hard limits:
Problem
Impact
Context bloat
When markdown files exceed ~200 lines / 25 KB, they consume significant context window budget on every session start
Linear growth
Knowledge accumulates linearly; no consolidation, no forgetting, no prioritization
Flat retrieval
Agent must read entire files or guess which file contains the answer - no semantic search
No outcome tracking
Agent cannot distinguish "this approach worked" from "this approach failed"
No temporal awareness
Agent cannot answer "what did I learn last week?" vs "what did I learn today?"
Manual curation
Humans must periodically prune, reorganize, and deduplicate memory files
Token waste
Every session re-reads everything, even information irrelevant to the current task
1.3 The Vector DB Partial Solution
Vector databases (Pinecone, ChromaDB, Qdrant) solve the retrieval problem - finding relevant memories among millions - but introduce new gaps:
No write policy: What gets stored? Everything? Only important things? Who decides?
No update/conflict resolution: Contradictory memories coexist silently
No forgetting/decay: Memory grows unbounded; retrieval quality degrades over time
No relationship modeling: "Service A calls Service B via Kafka topic X" has no natural vector representation
No outcome association: Cannot link "I tried approach X" -> "it failed because Y"
1.4 What We Want
A memory system that gives AI agents the right knowledge at the right time - without loading everything into context, without re-researching from scratch, and without humans manually curating memory files.

--------------------------------------------------------------------------------
2. Functional Requirements
2.1 Memory Types (FR-MEM)
The system must support four distinct memory types, each with different storage, retrieval, and lifecycle semantics:
ID
Memory Type
What It Stores
Examples
FR-MEM-01
Episodic
Timestamped records of what the agent did, decided, and observed
"Analyzed OrderProcessor.java - found it uses Spring Kafka @KafkaListener on topic orders.events"
FR-MEM-02
Semantic
Distilled facts, entities, relationships - what the agent knows to be true
"OPS service listens on orders.events and publishes to decisions.events"
FR-MEM-03
Procedural
Learned behavioral patterns - how the agent should operate
"This team prefers feature toggles with @Value annotation, default false"
FR-MEM-04
Outcome
Task results linked to approaches - what worked, what failed, and why
"Approach: direct binary serializer setters. Result: SUCCESS. Reason: avoids reflection overhead vs generic mapping"
2.2 Memory Operations (FR-OPS)
ID
Operation
Description
FR-OPS-01
Write
Store a new memory with type, content, metadata, and source attribution
FR-OPS-02
Search
Retrieve relevant memories by semantic similarity, recency, type, scope, and outcome
FR-OPS-03
Update
Modify an existing memory when new information supersedes old (conflict resolution)
FR-OPS-04
Forget
Remove or decay memories that are stale, contradicted, or low-value
FR-OPS-05
Consolidate
Merge related episodic memories into semantic facts (episodic -> semantic promotion)
FR-OPS-06
Reflect
Generate meta-observations from patterns across memories ("I keep failing at X because Y")
2.3 Scoping and Namespaces (FR-SCOPE)
ID
Requirement
FR-SCOPE-01
Memories must be scoped to a workspace (codebase / project)
FR-SCOPE-02
Memories must be scoped to a session (single agent conversation)
FR-SCOPE-03
Memories may be scoped to a user (human developer identity)
FR-SCOPE-04
Memories may be scoped to an agent (specific agent type/model)
FR-SCOPE-05
Cross-scope search must be possible (e.g., "all semantic memories for this workspace regardless of session")
2.4 Retrieval Quality (FR-RET)
ID
Requirement
FR-RET-01
Retrieval must combine semantic similarity with recency weighting - recent memories rank higher when relevance is equal
FR-RET-02
Retrieval must support type filtering - "give me only procedural memories"
FR-RET-03
Retrieval must support outcome filtering - "give me only approaches that succeeded"
FR-RET-04
Retrieval must support scope filtering - "memories from this workspace only"
FR-RET-05
Retrieval must return a token budget-aware result set - never exceed a configurable token limit
FR-RET-06
Retrieval must support relationship traversal - "What services does OPS connect to?" follows graph edges, not just vector similarity
2.5 Memory Lifecycle (FR-LIFE)
ID
Requirement
FR-LIFE-01
Episodic memories must decay over time - reduced retrieval priority unless reinforced by access or outcomes
FR-LIFE-02
Semantic memories must be consolidated - when N episodic memories about the same entity exist, merge into one semantic fact
FR-LIFE-03
Contradictions must be detected and resolved - new facts that conflict with existing ones trigger an update-or-flag decision
FR-LIFE-04
Procedural memories must be versioned - when a convention changes, the old version is superseded but preserved for audit
FR-LIFE-05
A background consolidation process must run between sessions (analogous to sleep/REM consolidation)
FR-LIFE-06
Memory store size must be bounded - configurable max entries per scope, with eviction of lowest-value memories
2.6 Agent Integration (FR-INT)
V1 integration policy: V1 ships only the CLI as the AI-agent integration surface. Every agent (Cursor, Claude Code, Codex, Cline, custom Python loops) integrates by invoking `agent-memory <subcommand>` as a shell tool call and parsing the deterministic JSON envelope on stdout. The HTTP API and MCP server are deferred to V1.5+ because (a) every modern agent already has a Shell tool, (b) MCP support is uneven across vendors and the SDK is TypeScript-first which would force Node into the install footprint, (c) the CLI envelope contract is the stable foundation an MCP shim can wrap with zero engine changes later. See `design.md` §2.1, §9, §13.
ID
Requirement
V1 status
FR-INT-01
Must expose a CLI (agent-memory) that any agent can invoke as a shell tool call
V1 - primary surface
FR-INT-02
CLI output must follow a deterministic, machine-parseable JSON envelope ({ ok, command, version, data | error, warnings, meta }) - entire stdout is exactly one JSON value when --format json
V1 - see design.md §9.1.3
FR-INT-03
CLI must support stdin input (--content -, --from-stdin) so agents can pipe arbitrary text/JSON without shell-escaping issues
V1 - see design.md §9.1.5
FR-INT-04
CLI must use stable exit codes (0 success, 2 usage, 3 validation, 4 not-found, 5 conflict, 124 timeout) so agents can branch on outcome
V1 - see design.md §9.1.4
FR-INT-05
CLI write commands must be idempotent (same content hash -> same memory ID; optional --idempotency-key) so agents can retry safely
V1 - see design.md §9.1.6
FR-INT-06
Must provide a session-start hook (agent-memory recall --task "...") that retrieves the most relevant memories for the current task and emits them as a markdown context block
V1
FR-INT-07
Must provide a session-end hook (agent-memory session-end --from-stdin) that extracts learnings from the completed session
V1
FR-INT-08
Must work with any LLM - not tied to a specific model provider
V1
FR-INT-09
Must require no daemon, no extra runtime for the V1 AI-agent path - installing the single Go binary is sufficient
V1
FR-INT-10
Must expose an HTTP API with the same envelope schema as the CLI, used by the dashboard and any future remote integration
V1 (built, optional surface) - only listens when agent-memory serve is run
FR-INT-11
Must support the MCP protocol for native integration with Cursor, Claude Code, and other MCP-enabled agents
V1.5+ (deferred) - implemented as a thin TypeScript shim that wraps the V1 CLI; see tasks.md Deferred section
FR-INT-12
A Python SDK for LangChain/CrewAI integration
V2 (deferred) - wraps the V1 CLI and HTTP API
2.7 Hybrid Storage Routing (FR-ROUTE)
Detailed comparison and worked examples are in `design.md` §6.5 (hybrid routing).
ID
Requirement
FR-ROUTE-01
The system must support at least four storage tiers: markdown (always-on), vector (semantic), graph (relationships), document (bulk metadata)
FR-ROUTE-02
A Hybrid Storage Router must decide the tier for each memory at write time, based on type, importance, size, frequency, and entity relationships
FR-ROUTE-03
Procedural memories (rules, conventions) must default to the markdown tier (always-on injection)
FR-ROUTE-04
Episodic memories must default to the vector tier (searchable, decaying)
FR-ROUTE-05
Semantic memories with relationships must default to vector + graph tier
FR-ROUTE-06
The markdown tier file (MEMORY.md) must be human-readable, git-versionable, and auto-injected at session start
FR-ROUTE-07
Memories must be promotable (vector -> markdown) when access frequency exceeds threshold (default >= 10 hits / 30 days for small memories)
FR-ROUTE-08
Memories must be demotable (markdown -> vector) when access frequency falls below threshold (default < 2 hits / 60 days, non-pinned only)
FR-ROUTE-09
Users must be able to explicitly pin a memory to the markdown tier (overrides router)
FR-ROUTE-10
The router must be explainable - every decision must be traceable to specific rules with reasons
FR-ROUTE-11
The markdown tier must be bounded by a configurable token budget (default 4,000 tokens); demotion enforces the limit
FR-ROUTE-12
Cold archival (vector -> document tier) must preserve memories with decay_score < 0.05 that are still referenced from other active memories
FR-ROUTE-13
Tier transitions (promote, demote, archive, evict) must be logged for observability
FR-ROUTE-14
The system must fall back gracefully to vector-only mode if the markdown tier is unavailable (e.g., file write fails)
2.8 Forgotten Memory Re-investigation (FR-RECON)
The "tip of the tongue" capability - see `design.md` §8.
ID
Requirement
FR-RECON-01
When a memory is evicted, demoted, archive-expired, or consolidated, the system must record a tombstone preserving its ID, entities, source attribution, and eviction reason
FR-RECON-02
Tombstones must be bounded in size (~50 bytes per tombstone) and bounded in retention (default 5 years, configurable)
FR-RECON-03
Every retrieval must run a gap detector in parallel, computing a forgotten_signal_score based on tombstone match, dangling graph edges, cluster-coverage gaps, and source-density
FR-RECON-04
When forgotten_signal_score >= gap_detection_threshold (default 0.4), the system must trigger reconstruction strategies in cost order
FR-RECON-05
Strategy 1 (Fragment Interpolation): combine surviving partial memories around the gap - must be cheap (no external calls)
FR-RECON-06
Strategy 2 (Outcome Back-tracing): infer lost intermediate steps from a surviving outcome - medium cost
FR-RECON-07
Strategy 3 (Source Re-investigation): re-read the original source pointer (file path + line range, Confluence page ID, session ID) and re-extract the memory - must check source mtime against tombstone creation date
FR-RECON-08
Strategy 4 (User Confirmation): surface the gap to the user when interactive
FR-RECON-09
Reconstructed memories must be clearly marked: source.type = "reconstruction", tags includes "reconstructed", linked to the original tombstone via a derived_from relation
FR-RECON-10
Reconstructed memories with confidence >= 0.8 are stored automatically; 0.5 - 0.8 returned as suggestions; < 0.5 discarded with gap log
FR-RECON-11
The system must enforce per-session and per-month caps to prevent runaway reconstruction cost (defaults: 5/session, 3 reconstructions/month for the same memory ID)
FR-RECON-12
A re-investigation cooldown (default 24 hours) must prevent the same query triggering reconstruction repeatedly
FR-RECON-13
An API and CLI must allow operators to inspect tombstones, manually trigger reconstruction, and confirm or reject low-confidence reconstructions
FR-RECON-14
When a reconstruction's source has changed since the original memory was created, the system must flag the reconstructed memory as "source modified - review recommended"
2.9 Observability (FR-OBS)
ID
Requirement
FR-OBS-01
Must provide a web dashboard showing memory contents, search results, and lifecycle events
FR-OBS-02
Must log all memory operations with timestamps and source attribution
FR-OBS-03
Must expose token cost metrics - tokens saved vs. baseline (full-context approach)
FR-OBS-04
Must support memory export to markdown for human review
FR-OBS-05
Must show per-tier statistics - counts, token usage, recent promotions/demotions per storage tier

--------------------------------------------------------------------------------
3. Non-Functional Requirements
3.1 Performance (NFR-PERF)
ID
Requirement
Target
NFR-PERF-01
Search latency (p95)
< 200ms for up to 100K memories
NFR-PERF-02
Write latency (p95)
< 100ms
NFR-PERF-03
Session-start context assembly
< 500ms
NFR-PERF-04
Background consolidation
Must not block agent operations
3.2 Storage (NFR-STOR)
ID
Requirement
Target
NFR-STOR-01
Must run local-first - no mandatory cloud dependency
SQLite + local embeddings
NFR-STOR-02
Optional cloud backend for team-shared memory
PostgreSQL + pgvector
NFR-STOR-03
Max storage footprint per workspace (default)
500 MB
NFR-STOR-04
Memory entries must be human-readable when exported
Markdown / JSON
3.3 Token Efficiency (NFR-TOK)
ID
Requirement
Target
NFR-TOK-01
Token consumption for memory retrieval vs. full-context baseline
>= 80% reduction
NFR-TOK-02
Session-start injection must fit within configurable token budget
Default: 4,000 tokens
NFR-TOK-03
Retrieved memories must be compressed - semantic lossless compression, not raw conversation logs
Compression ratio >= 5:1
3.4 Security (NFR-SEC)
ID
Requirement
NFR-SEC-01
Memory contents must never leave the local machine unless explicitly configured for cloud sync
NFR-SEC-02
Must support memory poisoning detection - anomaly scoring on write patterns
NFR-SEC-03
Must support memory redaction - remove specific entries by ID, scope, or content pattern
NFR-SEC-04
Must not store credentials, tokens, or secrets - content filtering on write
3.5 Portability (NFR-PORT)
ID
Requirement
NFR-PORT-01
Must work on macOS, Linux, and Windows (WSL)
NFR-PORT-02
Must not require Docker for basic operation (SQLite mode)
NFR-PORT-03
Must support export/import of memory stores between machines
NFR-PORT-04
Must be language-agnostic at the API level (HTTP + CLI)

--------------------------------------------------------------------------------
4. Constraints
ID
Constraint
Rationale
CON-01
Local-first architecture - SQLite as default backend
Developers must not need cloud accounts or subscriptions for basic use
CON-02
Embedding generation must support local models (e.g., all-MiniLM-L6-v2 via ONNX)
Avoid mandatory API calls for every memory operation
CON-03
LLM calls for consolidation/reflection are optional and configurable
Some teams cannot send codebase data to external APIs
CON-04
Must not modify the agent's source code - operates as an external service/tool
Must integrate via CLI, HTTP, or MCP without forking the agent
CON-05
Must handle workspaces with up to 100K memory entries without degradation
Covers ~2 years of active agent use on a large codebase
CON-06
V1 = single Go binary, zero TypeScript. Engine, CLI (V1 AI-agent surface), HTTP API, REM cycle, embeddings, vector ops, gap detector, and reconstructor all implemented in Go 1.22+ - single static binary, CGO only for sqlite-vec and ONNX Runtime. V1.5+ adds the MCP server shim (TypeScript wrapper over the V1 CLI commands).
V1 install footprint = one binary on PATH; no Node, no daemon required. Heavy CPU work belongs in Go for performance + deterministic memory profile. The TS shims are I/O-bound and benefit from MCP/React ecosystem maturity when added later - but they are explicitly off the V1 critical path.

--------------------------------------------------------------------------------
5. Success Metrics
Metric
Baseline (No Memory)
Target (With Memory)
Tokens per session (for context that could be recalled)
25,000+ (full re-read)
< 5,000 (targeted recall)
Time to productive context
2-5 min (re-exploring codebase)
< 10 sec (memory injection)
Repeated research rate
~60% of sessions re-study known material
< 10%
Failed approach re-attempt rate
~40% (agent tries same failed approach)
< 5% (outcome memory prevents)
Memory precision (retrieved memories relevant to task)
N/A
> 85%

--------------------------------------------------------------------------------
6. Research References
This requirements document is informed by the following 2025-2026 research:
Source
Key Contribution
Zylos Research - AI Agent Memory Architectures (Apr 2026)
Three-tier taxonomy (episodic/semantic/procedural), hybrid vector-graph consensus
MemR ECAI 2025 Paper (arXiv:2504.19413)
Conflict detection, 90% token reduction vs full-context, graph-enhanced memory
Letta/MemGPT (arXiv:2310.08560)
OS-inspired tiered memory, LLM-managed paging, core/archival/recall model
Zep Graphiti (arXiv:2501.13956)
Bitemporal knowledge graphs, 94.8% DMR accuracy, P95 retrieval < 300ms
AgeMem - Unified LTM/STM (arXiv:2601.01885)
Tool-based memory ops, agent-autonomous store/retrieve/update/discard
SimpleMem (arXiv:2601.02553)
Semantic lossless compression, 30x token reduction, 26.4% F1 improvement
FadeMem (arXiv:2601.18642)
Biologically-inspired decay, 45% storage reduction, maintained reasoning
OBLIVION (arXiv:2604.00131)
Decay-driven forgetting, uncertainty-gated retrieval, reinforcement-based write
Lanham - Memory Beyond RAG (Medium, Apr 2026)
Write policy + retrieval policy + update policy = memory; memory poisoning risks
Towards AI - State of Agent Memory (May 2026)
Four dimensions (storage, curation, retrieval, lifecycle), benchmark analysis
