# Agent Memory System - Requirements
## 1. Problem Statement
AI coding agents repeatedly re-discover the same project context (architecture, conventions, prior outcomes). This wastes tokens, increases latency/cost, and causes inconsistent decisions. A persistent memory system is required to:
- Provide relevant, budget-bounded context at session start and on demand.
- Capture durable knowledge, outcomes, and conventions safely and deterministically.
- Decay/evict low-value memories without catastrophic “silent amnesia”, enabling graceful re-investigation.

## 2. Goals
- **Local-first, zero-friction install**: runs on a developer machine with a single static binary; no required daemon.
- **CLI-first agent integration (V1)**: any agent can invoke the tool via shell tool calls; stable JSON-over-stdout contract.
- **Write policy, not blind append**: all writes go through security filtering, extraction, dedup/conflict handling, compression, and tier routing.
- **Token budget is enforced**: retrieval/recall never exceed a configurable token ceiling; always-on content is bounded.
- **Hybrid storage with automatic routing**: store memories in the tier that optimizes retrieval cost and inspection value; move between tiers over time.
- **Forgetting is healthy**: implement decay, consolidation, promotion/demotion, eviction; preserve tombstones for graceful forgetting.
- **Human-inspectable**: export/inspect memories as markdown/JSON; predictable structure; no opaque-only store.

## 3. Non-Goals (V1)
- MCP server as the primary integration surface.
- Always-on daemon requirement for correctness.
- External-source ingestion (Confluence/Jira/Notion/web) beyond local files and directories.
- Multi-tenant team shared memory with auth/TLS as the default mode (optional later).
- “Autonomous planning” features that change agent behavior beyond retrieving/writing memory.

## 4. Users and Primary Use Cases
### 4.1 Personas
- **AI Agent**: invokes memory operations via shell tool calls; requires deterministic parsing and bounded outputs.
- **Engineer**: inspects and searches memories, validates what the agent would see, exports or edits always-on rules.
- **Team Admin (optional)**: configures shared backend, retention, security policy, and operational limits.

### 4.2 Core Use Cases (V1)
- **Session start recall**: agent requests a compact context block for a workspace and optional task description.
- **Mid-session search**: agent asks a focused question and receives ranked results with citations/IDs.
- **Write learned facts/outcomes/conventions**: agent persists a structured memory safely.
- **Session end extraction**: agent provides a transcript/summary input; system extracts and persists consolidated memories.
- **Lifecycle maintenance**: system decays, consolidates, resolves conflicts, evicts, promotes/demotes memories.
- **Graceful forgetting**: when a query indicates missing knowledge, system detects gaps and reconstructs using tombstones/sources.
- **Bootstrap / study existing project**: ingest local docs/code to seed memory on day one.
- **Export and stats**: engineers export memory and inspect store health and token savings.

## 5. Functional Requirements
### 5.1 Workspace and Project Lifecycle
- **FR-WS-1**: Support per-workspace memory stores (default: one SQLite DB per workspace).
- **FR-WS-2**: Provide CLI commands to initialize, reinstall (re-write IDE hooks/rules without changing DB/project name), rename, list, and delete workspaces/projects; maintain a local registry.
- **FR-WS-3**: Workspace resolution must be deterministic (flag/env/cwd detection); all operations are scoped to one workspace.

### 5.2 Memory Write Pipeline
- **FR-W-1**: All writes MUST pass through: security filter → extraction/classification → dedup/conflict detection → compression → routing → persistence.
- **FR-W-2**: Writes MUST be idempotent under identical content (content-hash or idempotency key).
- **FR-W-3**: Support memory types: episodic, semantic, procedural, outcome.
- **FR-W-4**: For contradictions, system MUST preserve provenance (superseded links and/or contradiction relations).
- **FR-W-5**: Support both inline content and stdin input modes; avoid shell quoting hazards.

### 5.3 Retrieval and Recall
- **FR-R-1**: Provide `search` for ad-hoc queries and `recall` for session-start context assembly.
- **FR-R-2**: Retrieval MUST combine multiple signals (semantic similarity, recency/decay, graph proximity, outcome weighting, tier bias).
- **FR-R-3**: Retrieval MUST enforce a hard token budget and return deterministic truncation metadata.
- **FR-R-4**: Recall MUST allocate budget across memory types (procedural/semantic/outcome/episodic) and adapt when a task description is provided.

### 5.4 Hybrid Storage and Routing
- **FR-S-1**: Support multiple storage tiers: markdown (always-on), vector, vector+graph, document, cold archive.
- **FR-S-2**: Router MUST decide tier(s) per write based on type, user pin, importance score, size, relationships, and hot-promotion criteria.
- **FR-S-3**: System MUST support promotion and demotion between tiers as part of lifecycle maintenance.
- **FR-S-4**: Markdown tier MUST be round-trip safe and bounded by a markdown token budget.

### 5.5 Lifecycle (REM Cycle)
- **FR-L-1**: Implement decay scoring and access tracking for all memories.
- **FR-L-2**: Implement clustering and consolidation of episodic memories into semantic facts.
- **FR-L-3**: Implement conflict detection/resolution and record contradictions/supersession.
- **FR-L-4**: Implement eviction rules for low-value memories and retention limits.
- **FR-L-5**: Implement promotion rules (e.g., repeated outcomes → procedural rules; hot small facts → markdown).
- **FR-L-6**: V1 MUST be able to run lifecycle opportunistically (e.g., session-end or explicit command) without a daemon.

### 5.6 Graceful Forgetting (Tombstones + Reconstruction)
- **FR-GF-1**: When a memory is evicted/expired/consumed by consolidation, system MUST preserve a tombstone record with entity keys and provenance pointers.
- **FR-GF-2**: Retrieval MUST run a gap detector in parallel and trigger reconstruction when forgotten-signal threshold and guards are met.
- **FR-GF-3**: Reconstruction MUST attempt strategies in cost order and store reconstructed memories only above a confidence threshold; reconstructed memories must be clearly marked.
- **FR-GF-4**: Provide diagnostics to inspect tombstones and reconstruction attempts.

### 5.7 API Surfaces
- **FR-API-1 (V1)**: CLI is the primary integration surface; stdout must be JSON envelope (when requested) with strict stdin/stdout/stderr discipline.
- **FR-API-2 (V1 optional)**: HTTP API may exist for human inspection and future surfaces, but must share the same engine path and produce equivalent results.
- **FR-API-3**: Provide export endpoints/commands for markdown and JSON formats.

## 6. Non-Functional Requirements
### 6.1 Security and Privacy
- **NFR-SEC-1**: Reject writes that appear to contain secrets; configurable patterns and severity.
- **NFR-SEC-2**: Optional PII detection and redaction/rejection policy.
- **NFR-SEC-3**: Strict workspace isolation in all queries and writes.
- **NFR-SEC-4**: Avoid logging sensitive content by default; logs must go to stderr and be controllable via verbosity flags.

### 6.2 Reliability and Data Integrity
- **NFR-REL-1**: Atomic writes for markdown tier to prevent corruption.
- **NFR-REL-2**: SQLite should use WAL mode; tolerate concurrent CLI invocations.
- **NFR-REL-3**: Deterministic behavior for identical inputs where practical (ranking, truncation decisions).

### 6.3 Performance and Scalability
- **NFR-PERF-1**: Recall/search should be interactive for typical local stores (order-of-100ms to low seconds, depending on embeddings and reconstruction strategy).
- **NFR-PERF-2**: Embeddings must be cached; identical memory content should not be re-embedded.
- **NFR-PERF-3**: Support growth to meaningful personal-scale datasets (tens of thousands of memories) with reasonable retrieval latency.
- **NFR-PERF-4**: Token-budget clipping must be efficient and safe under large candidate sets.

### 6.4 Portability and Operability
- **NFR-OPS-1**: Single static binary distribution for the core service (with CGO only where required).
- **NFR-OPS-2**: Configuration via file + env + flags; defaults must be safe and sensible.
- **NFR-OPS-3**: Provide diagnostics (`stats`, router explain, store health, token savings estimates).

## 7. Acceptance Criteria (V1)
- A developer can initialize a workspace and use the CLI to write and recall memories with a stable JSON envelope.
- Retrieval respects a hard token budget and returns deterministic metadata (`tokens_used`, `budget`, `truncated`).
- Write pipeline prevents secret leakage and performs dedup/conflict handling.
- Storage uses SQLite + a markdown always-on tier with bounded token budget and atomic writes.
- Lifecycle operations (decay/consolidate/evict/promote) can run without a daemon and produce measurable store-health output.
- Tombstones and gap detection enable reconstruction attempts and clearly mark reconstructed memories and provenance.
- Export produces human-inspectable markdown/JSON without breaking parsing contracts.

## 8. Out of Scope / Deferred
- Full MCP server integration as the primary surface (planned V1.5+).
- Long-lived `serve` mode as required for lifecycle (optional V1.5+).
- Team-shared deployments with auth/TLS as default (optional later).
- External-source ingestion connectors beyond local files/dirs (planned V2).
