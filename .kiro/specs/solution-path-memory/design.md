# Solution-Path Memory Design

## Context and Diagnosis

Agent Memory already has four durable memory types, outcome metadata, sessions, normalized observations, observation-to-memory provenance, retrieval feedback, consolidation, and skill distillation. These pieces preserve facts, conclusions, some procedures, and a chronological activity stream. They do not represent the task-level causal structure connecting a goal to attempts, decisions, evidence, validation, and a final result.

Adding more prose to outcome memory would not close the gap. It would collapse ordering, failed branches, tool evidence, active state, and promotion lineage into one untyped field. Treating raw transcripts or model reasoning as the missing representation would create privacy, security, portability, quality, and token-cost problems. The chosen architecture adds a structured solution-episode layer between observations and durable memory.

## Chosen Domain Model

The model has four layers with different lifecycle rules:

1. **Working state** is mutable and expiring. It exists to resume the current task.
2. **Observations** are normalized evidence of externally visible events. Existing observation storage remains authoritative for captured client events.
3. **Solution episodes and steps** organize meaningful observations and client summaries into a task-level path.
4. **Durable memories and skills** are promoted, validated reusable knowledge.

```mermaid
flowchart TD
    S["Agent session"] --> W["Expiring working state"]
    S --> O["Normalized observations"]
    S --> E["Solution episode"]
    W --> E
    O --> E
    E --> P["Validated solution path"]
    P --> M["Durable memories"]
    P --> L["Tool lessons"]
    L --> M
    L --> K["Skill or script artifact"]
    M --> R["Future how-oriented recall"]
    K --> R
    W --> C["Authorized session continuation"]
```

This is not a fifth durable memory type. Episodes are provenance-rich work records; existing memory types retain their established retrieval and decay semantics.

## Episode State Machine

An episode starts in `active`. It may move to `paused`, return to `active`, or enter a terminal `completed`, `partial`, `abandoned`, or `cancelled` state. A handoff keeps the episode non-terminal and changes the active session or principal only through an authorized transition. Finalization operates on a terminal snapshot and publishes a versioned solution-path summary. A corrected finalization supersedes the prior summary without mutating it.

```mermaid
stateDiagram-v2
    [*] --> Active: "start"
    Active --> Paused: "checkpoint or disconnect"
    Paused --> Active: "resume"
    Active --> Active: "append safe step"
    Active --> Paused: "authorized handoff"
    Active --> Completed: "verified success"
    Active --> Partial: "best-known partial result"
    Active --> Abandoned: "stop pursuing"
    Active --> Cancelled: "explicit cancellation"
    Completed --> Finalized: "publish summary"
    Partial --> Finalized: "publish partial summary"
    Abandoned --> Finalized: "publish avoid lesson"
    Finalized --> Superseded: "correct and re-finalize"
```

## Data Contracts

### Solution Episode

The episode carries stable identity, workspace and tenant scope, creating principal and client, originating session, goal summary, status, capture policy, version, timestamps, retention class, finalization status, and an optional superseding episode. Goal text is concise and passes admission before storage.

The episode does not contain the entire path as one JSON document. Steps are independently addressable so append operations, evidence links, pagination, feedback, and redaction remain bounded.

### Solution Step

Each step carries an episode ID, server-assigned monotonic ordinal, kind, summary, status, source, timestamps, confidence, sensitivity, and schema version. Optional references identify parent steps, observations, memories, files, tests, tools, skills, and artifacts.

The summary is the externalizable explanation a competent collaborator would give: what was tried or decided and why the available evidence supported it. It is not an internal token trace. Parent references allow a failed branch or decision dependency without requiring an unrestricted reasoning graph.

### Tool Invocation and Tool Lesson

Invocation records attach to action or observation steps and contain logical tool identity, operation, capability intent, safe input summary, result class, latency, and bounded evidence references. Payload hashes can support deduplication without retaining payload bodies.

A tool lesson is a versioned derived record referencing one or more steps. It records the demonstrated capability, preconditions, limitations, known failure modes, safer fallback, confidence, validation state, and promotion targets. A considered or failed tool remains evidence but cannot be advertised as a successful capability.

### Working State

Working state is a single current document per episode generation, stored separately from steps. It contains bounded fields for goal, constraints, plan items, completed items, open questions, next action, and artifact references. It carries a generation counter, expiry, sensitivity, and updater identity.

Updates use compare-and-swap semantics. A stale generation receives a conflict plus current metadata; the server never silently merges prose or lists. Clients can refetch and intentionally reconcile.

### Solution-Path Summary

Finalization creates a compact, versioned summary containing outcome, decisive steps, useful failed approaches, verification evidence, tool lessons, unresolved risks, and next-time guidance. The summary references its source steps and observations. It may be embedded for retrieval, while full step detail remains relational and lazy-loaded.

### Promotion Provenance

Promotion records link an episode or summary to an existing memory or skill path. They store promotion kind, target identity, source step identities, reviewer or automated policy identity, validation state, and time. Existing memory provenance remains intact; the new record extends rather than replaces observation-to-memory provenance.

## Storage Architecture

SQLite gains normalized tables for episodes, steps, step references, working state, tool lessons, solution summaries, promotions, and retrieval feedback. Foreign keys protect episode ownership and summary lineage. Server-assigned ordinals use a transaction that validates the current episode version, increments it, and inserts the step atomically.

Indexes support workspace/status/updated-time episode listing, episode/ordinal step paging, expiry cleanup, validated-summary retrieval, tool identity lookup, and promotion-target lookup. Large payload bodies are excluded from these tables. Existing per-workspace database placement preserves local isolation; hosted storage follows tenant/workspace authorization and equivalent contracts.

Schema migration is additive. Old binaries ignore new tables. New binaries tolerate workspaces with no episode records and preserve every existing memory and observation query.

## Capture and Orchestration

Clients use explicit lifecycle operations rather than relying solely on transcript parsing. Start declares the goal and capture policy. Checkpoint updates working state and may append one or more safe steps. Tool hooks continue to emit normalized observations; clients or a bounded correlation service link relevant observations to a step using session, time, tool identity, and explicit external event identifiers.

Automatic correlation may propose links but cannot invent rationale or causal certainty. Ambiguous events stay unlinked. The client remains able to reference an observation explicitly.

Session-end becomes an integration point, not the authoritative episode parser. When an active episode exists, session-end requests final checkpoint or terminal status and may invoke finalization. When no episode exists, current heuristic memory extraction remains unchanged.

## Safe Rationale and Admission

The write boundary names its accepted field `rationale_summary` in transport contracts and describes it as concise, externalizable justification. It does not offer fields named scratchpad, internal reasoning, hidden state, or chain-of-thought.

Admission applies four stages:

1. Structural validation enforces types, lengths, references, ordering, and idempotency.
2. Content classification detects secrets, personal data, prompt injection, unsafe instructions, and explicit raw-reasoning submissions.
3. Origin-aware policy selects allow, review, quarantine, or reject.
4. Persistence writes the accepted safe record and content-free audit outcome atomically where required.

Redaction replaces unsafe content with a typed redaction marker and reason class; it never leaves an apparently complete but altered rationale. Provider prompts and raw completions are operational telemetry at most and are not domain records.

## Finalization Pipeline

Finalization loads a consistent terminal episode snapshot, excludes expired or disallowed working fields, resolves authorized evidence, and validates referenced observations and artifacts. A deterministic assembler can produce a summary from already structured steps. An optional model can propose compression, selection, or tool lessons against the immutable packet.

Model proposals pass schema, citation, entailment, admission, and duplication checks. Accepted outputs and promotions are committed with an idempotency key. If a durable memory write fails, finalization reports partial publication with exact target states and can retry safely. No successful promotion is rolled back merely because a later optional promotion failed.

Repeated successful tool lessons are candidates for existing consolidation and skill distillation. Repetition alone does not prove safety; each candidate retains its success evidence and review state.

## Retrieval and Recall

How-oriented retrieval uses a two-stage process. Candidate generation searches solution summaries, tool lessons, procedural memories, successful outcomes, and authorized current working state. Ranking then considers task similarity, outcome, evidence quality, validation state, feedback, recency, and workspace scope.

Recall assembly emits separate sections for current work, prior solution paths, reusable procedures or skills, and failed-approach warnings. It first allocates budget to compact summaries. Decisive step detail is fetched only when budget remains or the caller asks for an episode. This avoids flooding context with tool chronology.

Solution-path feedback is recorded independently because a path can be misleading even when one promoted procedural memory is useful. Harmful or rejected paths are suppressed from default recall and enter review without deleting historical audit lineage.

## Authorization and Privacy

Workspace access does not automatically grant access to another principal's active working state. Working-state reads require the owning principal, an explicitly authorized handoff, or an operator policy with audited purpose. Completed safe solution paths follow workspace knowledge permissions after finalization.

Local-owner APIs accept registered workspace identity and resolve database and root server-side. Hosted APIs validate tenant, workspace, principal, and capability for every record. Correlation never crosses a workspace or session boundary. Export omits expired working state and honors redactions; deletion removes episodes and derived records according to retention while preserving only policy-required content-free audit data.

## Failure Modes and Edge Cases

- **Client disconnects before checkpoint:** previously accepted steps remain; working state may be stale and eventually expires.
- **Duplicate client retry:** idempotency returns the original record and ordinal.
- **Concurrent step append:** one transaction wins; the other retries against the new episode version without ordinal collision.
- **Stale working-state update:** return a conflict and current generation; do not merge automatically.
- **Missing observation:** retain the step with reduced evidence quality and an unresolved reference state only when policy permits.
- **Observation later deleted:** preserve a tombstoned reference and reduce confidence rather than fabricating evidence.
- **Tool succeeded but validation failed:** record the invocation outcome separately from task verification; do not promote a success lesson.
- **Episode ends without success:** finalize a partial or avoid-path summary if useful; do not label it successful.
- **Finalizer crash:** resume from the immutable episode snapshot and idempotency key.
- **Expired working state races with recall:** query-time expiry filtering is authoritative even before physical cleanup.
- **Secret found after persistence:** quarantine or redact the affected record, suppress derived summaries, and review promotions through lineage.
- **Cross-workspace reference:** reject the write without revealing whether the target exists.
- **Oversized episode:** enforce per-step, per-episode active-step, and reference limits; require checkpoint compaction before further append.
- **Clock skew:** server time sets ordinals, timestamps used for lifecycle decisions, and expiry.

## Performance and Scaling

Step append is constant-sized and transactional. Episode detail uses ordinal cursor pagination. Finalization loads a bounded terminal snapshot and streams or pages older non-decisive steps. Summary embeddings, not every tool event, drive default semantic retrieval.

Working-state lookup uses one keyed row and query-time expiry. Cleanup scans an expiry index in bounded batches. Correlation consumes only a bounded session window. Tool lessons index normalized logical identity rather than provider-specific display names.

Metrics cover append latency, conflicts, rejected content classes, active episode count, expiry backlog, finalization duration, partial promotion, summary size, how-recall latency, path feedback, and path-to-skill promotion. Metrics and traces exclude customer content.

## Alternatives and Trade-offs

### Store the Full Transcript

Rejected. It is high-volume, difficult to retrieve, likely to contain secrets and private reasoning, weakly structured, and coupled to client formatting.

### Store Raw Chain-of-Thought

Rejected. Hidden model reasoning is not a stable or appropriate product contract. It creates privacy and security risk without guaranteeing faithful explanation. Concise rationale summaries plus evidence provide the useful operational value.

### Add a Fifth Durable Memory Type

Rejected. A solution episode has mutable state, ordered children, evidence links, finalization, and promotion lifecycle that do not fit one memory row. Existing durable types remain the correct promoted knowledge forms.

### Expand Outcome Memory Only

Rejected. Outcome metadata can summarize approach and reason but cannot represent branching attempts, current work, tool evidence, ordering, or independent feedback.

### Infer Everything at Session End

Rejected as the primary mechanism. Transcript heuristics lose explicit intent, causal links, idempotency, and live handoff. Session-end inference remains a compatibility fallback.

### Chosen: Structured Episodes with Promotion

This adds schema and client complexity but produces a provider-independent, inspectable, bounded record. It also preserves the distinction between temporary work, evidence, validated path, durable knowledge, and executable skill.

## Rollout Strategy

1. Add domain and storage contracts behind a disabled capability flag.
2. Ship explicit CLI and MCP lifecycle operations for local workspaces.
3. Add working-state continuation and how-oriented recall without automatic promotion.
4. Add deterministic finalization and promotion through the existing write pipeline.
5. Add tool lessons and skill-distillation provenance.
6. Add Activity inspection, correction, redaction, and feedback.
7. Add hosted parity, tenant isolation gates, and bounded model-assisted proposals.
8. Evaluate retention, recall usefulness, and false promotion before enabling automatic suggestions.

Rollback disables episode capture and how-oriented recall. Additive tables remain inert; no existing memory data requires reversal.

## First-release product decisions

- Working state expires after 24 hours by default and callers may shorten it or extend it to a hard maximum of seven days. Query-time expiry is authoritative.
- Completed steps remain append-only in the first release. Finalization reads a bounded 500-step snapshot and produces a size-bounded summary; destructive step compaction is deferred until retention evidence supports it.
- Deterministic finalization is the default. Model-assisted proposals remain explicitly requested and must pass the same schema, citation, admission, and duplication checks before publication.
- Editing is limited to superseding summary correction, misleading-step feedback, typed redaction, pinning, episode supersession, and deletion. Stored historical steps are not silently rewritten.

## Verification and Release Gates

Release requires domain invariant tests, migration round trips, concurrent append tests, stale working-state conflicts, expiry tests with a controllable clock, policy rejection and redaction fixtures, idempotent finalization, partial promotion recovery, provenance integrity, local registered-project routing, hosted two-tenant isolation, CLI/MCP contracts, dashboard accessibility, import/export compatibility, and full regression tests.

The decisive product evaluation compares a baseline agent with an episode-enabled agent on interrupted-task resume, similar-task reuse, tool selection, failed-approach avoidance, token consumption, and harmful-path suppression. Success requires better completion or fewer repeated steps without increased secret retention or cross-scope exposure.
