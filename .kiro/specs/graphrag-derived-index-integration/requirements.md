# GraphRAG Derived-Index Integration Requirements

## Objective

Integrate Microsoft GraphRAG as an optional, asynchronous derived-index subsystem for Agent Memory. GraphRAG enriches stored memories and source passages with canonical entity candidates, relationship candidates, communities, and community reports. It does not replace Agent Memory's canonical memory store, source/citation provenance, existing vector and term retrieval, solution-path records, feedback, lifecycle, authorization, or final recall assembly.

The feature is successful when knowledge learned on different days can become connected through an incrementally updated graph without delaying memory writes, weakening provenance, or making ordinary recall depend on GraphRAG availability.

## Product Principles

1. **Agent Memory remains authoritative.** GraphRAG output is a rebuildable derived index.
2. **Writes remain immediately useful.** A memory is searchable before graph enrichment completes.
3. **Relationships remain explainable.** Every imported entity, edge, community finding, and report resolves to authorized source passages or memories.
4. **Inference is not fact.** GraphRAG-produced relationships begin as proposed derived knowledge and cannot silently become approved truth.
5. **Retrieval remains hybrid.** Existing vector, term, metadata, observation, and solution-path retrieval continue to operate; graph context is added only when query intent and policy permit it.
6. **Failure degrades safely.** Disabled, stale, delayed, or failed GraphRAG indexing never prevents ordinary write, search, recall, export, or deletion.

## Terminology

- **Canonical record:** An Agent Memory memory, source, edition, passage, citation, observation, solution episode, or review record governed by existing lifecycle and authorization rules.
- **Graph projection:** A bounded, policy-filtered representation of canonical records supplied to GraphRAG for indexing.
- **Graph revision:** One immutable description of the GraphRAG input set, configuration, model identity, output artifacts, and completion status for one workspace.
- **Derived entity:** An entity extracted or consolidated by GraphRAG and imported with provenance to its source text units.
- **Derived edge:** A relationship extracted or consolidated by GraphRAG and imported with evidence bindings, confidence, origin, and review state.
- **Community:** A hierarchical GraphRAG cluster whose membership is fully bound to authorized derived entities, edges, and source text units.
- **Community report:** A versioned GraphRAG summary of one community. It is derived context, not canonical evidence.
- **Graph freshness:** Whether the active graph revision covers the current eligible canonical-record watermark and configuration version.
- **Query route:** The Agent Memory retrieval strategy selected for a request: existing/basic retrieval, local graph enrichment, or global community retrieval.

## Assumptions for Review

1. Microsoft GraphRAG is consumed from its published PyPI package through a version-pinned adapter rather than cloned, vendored, or copied into Agent Memory's Go domain packages.
2. Production release requires standalone, self-managed, and hosted indexing parity, including worker lifecycle, tenant isolation, operations, backup/restore, deletion, observability, and upgrade verification. Implementation may be sequenced, but no partial or MVP topology satisfies this specification.
3. Graph indexing is opt-in and disabled by default until a compatible model provider, cost policy, and storage location pass readiness checks.
4. Existing GraphRAG standard and incremental-update behavior may be used, but Agent Memory owns input projection, output validation, provenance import, activation, and rollback.
5. GraphRAG is an indexing dependency only. Agent Memory owns every online query path, including graph expansion, community-report retrieval, routing, ranking, clipping, citation resolution, and answer assembly.

## User Stories

- As a user, I can write a memory now and retrieve it immediately while graph enrichment completes later.
- As a user, I can discover that a Day-10 memory elaborates on or applies to knowledge imported from Book A on Day 1, with evidence showing why the relationship was proposed.
- As a user, I can ask a direct factual question without paying GraphRAG latency or indexing cost when graph context is unnecessary.
- As a user, I can ask a relational question and receive memories expanded through authorized, evidence-backed graph paths.
- As a user, I can ask a corpus-wide question and receive community-level synthesis that can be traced back to original memories and passages.
- As a user, I can inspect, approve, reject, or supersede inferred relationships without rewriting the underlying source memories.
- As an operator, I can enable, disable, rebuild, update, observe, budget, and roll back GraphRAG independently per workspace.
- As an operator, I can upgrade or remove the GraphRAG adapter without losing canonical Agent Memory data.

## Functional Requirements

### R1 — Optional Derived-Index Boundary

- Agent Memory must expose a provider-neutral graph-index adapter contract covering readiness, full index, incremental update, cancellation, and artifact inspection.
- Microsoft GraphRAG must be one version-pinned implementation of that contract.
- No canonical write, search, recall, browse, export, deletion, or solution-path operation may require the adapter to be installed or ready.
- GraphRAG packages, Parquet schemas, prompts, and filesystem layout must not become Agent Memory domain contracts.
- The adapter's license, pinned version, configuration schema version, and upgrade procedure must be recorded in release artifacts.
- The production adapter must declare an exact `graphrag` PyPI version in its own Python project, commit a fully resolved hash-locked dependency graph, and install from that lock without resolving newer packages during build or runtime.
- Production runtime must not require a Git checkout of Microsoft GraphRAG and must not fetch package or source dependencies at runtime.

### R2 — Graph Projection Contract

- Eligible memories, source passages, and approved derived knowledge must be projected with stable Agent Memory identities, workspace scope, source kind, content fingerprint, event time, and bounded provenance locators.
- Projection must exclude secrets, quarantined content, expired working state, deleted content, unauthorized content, raw chain-of-thought, unrestricted tool payloads, and records suppressed by existing admission policy.
- Book/source membership must be encoded explicitly through stable source, edition, asset, passage, and memory bindings; it must never be inferred solely from semantic similarity.
- The projection must distinguish original source text, user-authored memory, agent-derived memory, solution summary, and graph-derived summary.
- Projection output must be deterministic for the same canonical records, policy version, and configuration.

### R3 — Asynchronous Index Scheduling

- A successful canonical write or source publication must enqueue graph work only after its transaction commits.
- Repeated events must be idempotent by workspace, subject fingerprint, projection version, and graph configuration version.
- Standalone mode must provide a bounded local work runner; hosted mode must use the existing transactional outbox and worker isolation pattern.
- The scheduler must coalesce bursts, enforce minimum batch sizes or maximum wait windows, and avoid one GraphRAG run per small memory.
- A failed or unavailable graph run must leave canonical writes committed and mark graph freshness explicitly degraded.

### R4 — Full Index and Incremental Update

- Operators must be able to request a full rebuild and an incremental update for one authorized workspace.
- Incremental updates must cover new, changed, superseded, restored, and deleted eligible records since a stable watermark.
- Each run must use an immutable input manifest and produce an immutable output manifest containing model identities, prompt/configuration fingerprints, adapter version, counts, timings, and cost measurements when available.
- A new revision must not become active until all required artifacts validate and every imported evidence reference resolves.
- Activation must be atomic; failed activation must retain the previous active revision.

### R5 — Derived Entity Import

- Imported entities must have stable Agent Memory identities independent of GraphRAG's per-run human-readable IDs.
- Entity reconciliation must preserve aliases, type, description proposals, source text-unit bindings, occurrence counts, revision membership, and merge/split history.
- Cross-day entity consolidation must not erase distinct entities that share a name but have incompatible source evidence or scope.
- Reconciliation conflicts must remain explicit and reviewable rather than silently merging canonical identities.
- Rebuilding the same projection with the same configuration must not duplicate derived entities.

### R6 — Derived Relationship Import

- Every imported relationship must identify source and target entities, normalized relationship kind, GraphRAG description, weight or confidence, source text-unit bindings, graph revision, inference origin, and review state.
- GraphRAG relationships must enter as `proposed` unless an existing explicit Agent Memory relationship and evidence justify a stronger deterministic state.
- Unsupported GraphRAG relationship descriptions must remain typed as bounded external relationship proposals; they must not be coerced into a misleading existing edge kind.
- Contradictory, challenging, superseding, temporal, causal, membership, and similarity relationships must remain distinguishable during retrieval.
- An imported edge without resolvable authorized evidence must be rejected or quarantined before revision activation.

### R7 — Memory and Source Association

- Agent Memory must maintain explicit bindings from each derived entity and relationship to the memories, passages, citations, and source assets that produced it.
- The system must be able to express that `memory_10` discusses an entity found in Book A without falsely claiming that Book A authored `memory_10`.
- Memory-to-source membership, memory-to-episode promotion, and memory supersession must continue using Agent Memory provenance rather than GraphRAG inference.
- A graph path shown to a user or supplied to recall must include bounded reasons for every hop and the resolution state of its supporting evidence.
- Deleted or tombstoned evidence must remain visibly unresolved until lifecycle policy removes the derived record; it must not silently redirect to unrelated evidence.

### R8 — Communities and Reports

- Imported communities must preserve hierarchy, membership, GraphRAG level, source coverage, graph revision, and deterministic Agent Memory identity.
- Community reports must be stored as versioned derived artifacts with the exact entity, relationship, and text-unit membership used to generate them.
- Community reports must never be treated as direct quotations or canonical source evidence.
- A report becomes stale when its membership, underlying evidence, model identity, prompt fingerprint, or relevant review state changes.
- Global retrieval must expose source coverage and unresolved-evidence counts alongside report relevance.

### R9 — Agent Memory Query Intent and Routing

- Agent Memory must classify or accept explicit intent for direct/basic, relational/local, and global/aggregate questions.
- Basic retrieval must remain the default and preserve current factual ranking behavior.
- Local graph enrichment may add entities, relationships, source passages, and neighboring memories around several diverse basic-retrieval seeds.
- Global retrieval may use community reports and map-reduce style synthesis only when the request is corpus-wide or explicitly routed to global mode.
- Callers must be able to force basic-only retrieval and to request graph enrichment explicitly.
- Online retrieval must read only Agent Memory-managed derived-index contracts; it must not invoke GraphRAG's query engine, import GraphRAG answer text, or depend on the GraphRAG runtime being online.

### R10 — Hybrid Candidate Assembly

- Imported GraphRAG entities, relationships, communities, and reports must enter Agent Memory as candidate index records, not as pre-authorized answer text.
- Final ranking must combine direct semantic relevance, term/entity match, graph path quality, edge confidence/review state, evidence quality, recency, decay, feedback, suppression, supersession, source diversity, and graph freshness.
- Contradictions and challenges must be presented as conflicts, never counted as supporting paths.
- Candidate deduplication must prevent the same source content from consuming budget through a memory, text unit, entity description, and community report simultaneously.
- Token clipping must prefer original evidence and concise path explanations over verbose derived reports when both support the same claim.

### R11 — Recall and Answer Grounding

- Existing recall output must remain valid when no graph context is available.
- Graph-enriched recall must separate canonical memories, source evidence, graph paths, community context, conflicts, and freshness warnings.
- A final answer claim based on graph-derived context must cite resolvable original Agent Memory evidence; a community report alone is insufficient evidence.
- If graph-index reads fail after basic retrieval succeeds, the system must return basic results with an explicit degraded-graph indicator rather than fail the whole request, unless the caller explicitly required graph-only behavior.
- Retrieval feedback must target the request, path, entity, edge, community report, and canonical memory independently where applicable.

### R12 — Review, Correction, and Trust

- Users with existing knowledge-review capability must be able to approve, reject, supersede, or annotate proposed entities and edges.
- Rejected or harmful graph records must be excluded from future candidate assembly immediately and feed the next reconciliation or rebuild policy.
- Human-approved Agent Memory relationships must not be downgraded merely because a later GraphRAG run omits them.
- Graph rebuilds must carry forward compatible review state by stable evidence-backed identity and surface ambiguous carry-forward cases for review.
- The UI must distinguish inferred, reviewed, approved, rejected, stale, and superseded graph records.

### R13 — Deletion, Retention, and Rebuild

- Canonical deletion and retention policy remain authoritative over all graph artifacts.
- Deleting a memory, source, edition, passage, tenant, or workspace must remove or tombstone its graph projection and schedule bounded revision repair without resurrecting content from old GraphRAG artifacts.
- Export must include derived graph metadata and provenance only when requested and authorized; canonical export must not depend on GraphRAG readiness.
- Raw GraphRAG inputs, caches, prompts containing workspace content, and output artifacts must follow explicit retention and secure-deletion policies.
- A full graph rebuild must be possible from eligible canonical records alone.

### R14 — Standalone and Hosted Isolation

- Every graph job, artifact, entity, edge, community, report, query, and review must be scoped to exactly one workspace and, in hosted mode, one tenant.
- Hosted adapters must resolve workspace storage server-side and must never accept an arbitrary database path, artifact directory, or GraphRAG root from a remote caller.
- Worker credentials must use least privilege and separate source-read, graph-artifact-write, graph-query, and review capabilities where supported.
- Cache keys, artifact paths, temporary directories, model requests, telemetry, and errors must not leak identifiers or content across workspaces.
- Two-tenant tests must prove isolation through indexing, incremental update, query, failure, deletion, export, and rebuild.

### R15 — Configuration and Readiness

- GraphRAG configuration must include enabled state, adapter/version, indexing method, model routes, prompt/configuration fingerprints, batch policy, timeout, concurrency, cost limits, storage location, retention, and permitted query modes.
- Secrets must be referenced through existing secret-management boundaries and never persisted in GraphRAG project files generated from user input.
- Readiness must validate executable/package availability, supported adapter version, writable bounded artifact storage, model-provider compatibility, prompt/configuration presence, and workspace policy.
- Unsupported or changed GraphRAG schemas must fail closed before artifact import.
- Configuration changes that alter graph meaning must mark the active revision stale and require a new validated revision before activation.

### R16 — Processing and Operator Controls

- The existing Processing experience must expose graph jobs with queued, running, completed, failed, cancelled, and stale states using safe bounded failure guidance.
- Authorized operators must be able to request update, rebuild, retry, cancel, disable, and roll back to the prior valid revision.
- Users must be able to inspect active revision freshness, indexed watermark, last successful update, pending eligible records, and degraded status.
- Operations must be idempotent and must not create overlapping active revisions for the same workspace and configuration.
- Standalone CLI and hosted API operations must have equivalent lifecycle semantics even when their execution adapters differ.

### R17 — Observability, Cost, and Backpressure

- Metrics must cover queue age, coalesced subjects, projection counts, extraction counts, rejected artifacts, indexing latency, query latency by route, input/output tokens, estimated cost, active revision age, freshness lag, and fallback rate.
- Logs and traces must contain content-free identifiers and bounded error categories, not source text or generated reports.
- Per-workspace concurrency, input size, token, cost, retry, and storage limits must be enforced before work begins.
- Backpressure must delay graph enrichment without delaying canonical memory writes.
- Repeated poison jobs must dead-letter with an operator-visible safe reason and must not spin indefinitely.

### R18 — Compatibility and Rollout

- Existing databases and hosted deployments must continue operating without GraphRAG tables, artifacts, packages, or configuration.
- New persistence must be additive and migration-safe; rollback must leave canonical memories and existing retrieval usable.
- The first release must run graph retrieval in shadow evaluation before it can affect default recall ranking.
- Enabling graph candidates in production must be separately controllable for local and global routes and reversible per workspace.
- Upstream GraphRAG upgrades must pass schema-contract, golden-projection, deterministic-import, isolation, cost, and retrieval-quality gates before rollout.

## Non-Functional Requirements

### Reliability

- Canonical write success must be independent of graph scheduling success.
- Graph jobs, imports, activation, deletion repair, and review carry-forward must be restartable and idempotent.
- An interrupted run must never expose a partially active revision.

### Performance

- Basic-only retrieval must not invoke GraphRAG or read graph artifacts.
- Graph enrichment must obey explicit latency and context budgets and return partial bounded context when policy allows.
- Index scheduling must be batch-oriented and prevent unbounded fan-out from high-frequency memory writes.

### Security and Privacy

- Existing admission, authorization, tenant isolation, source custody, retention, export, and deletion controls apply before projection and after import.
- No prompt, artifact, telemetry record, or failure payload may contain credentials, raw chain-of-thought, or unauthorized workspace content.
- Derived summaries inherit the most restrictive applicable classification of their source membership.

### Portability

- Agent Memory must remain usable as a Go application without a mandatory Python runtime.
- GraphRAG-specific execution and artifacts must remain behind replaceable adapters and documented compatibility contracts.
- Local development, self-managed deployment, and hosted deployment may use different execution transports while preserving the same domain semantics.

## Evaluation Requirements

The release evaluation set must include:

1. Day-1 Book A plus Day-10 related-memory association with explicit source and entity provenance.
2. Semantically related memories with no explicit shared entity, verifying that any inferred association is proposed and evidenced rather than fabricated as membership.
3. Same-name/different-entity collision across sources.
4. Contradictory and superseding memories across graph revisions.
5. Direct factual questions where graph enrichment must not reduce precision or materially increase latency.
6. Relational local questions requiring multi-hop evidence.
7. Corpus-wide recurring-pattern questions requiring community coverage.
8. Stale, failed, disabled, and rolled-back graph revisions with successful basic fallback.
9. Cross-workspace and cross-tenant isolation attacks.
10. Deletion, export, retention expiry, and full rebuild without content resurrection.

Quality gates must compare basic retrieval with shadow graph-enriched retrieval for answer fact coverage, citation correctness, contradiction handling, source diversity, retrieval usefulness feedback, latency, tokens, and cost. Graph enrichment must not become a default route until it demonstrates measurable benefit without regression in isolation or grounding. Passing shadow evaluation is a production rollout gate, not an MVP acceptance boundary.

## Commands

Commands are provisional until the design gate selects the adapter transport and package layout.

- Core and storage tests: `go test ./internal/core ./internal/storage/sqlite ./internal/application ./internal/engine`
- Library and grounding tests: `go test ./internal/library ./internal/readingroom ./internal/retrieval`
- Standalone CLI and API tests: `go test ./internal/cli ./internal/api`
- Hosted source, outbox, worker, and isolation tests: `go test ./internal/saas/source ./internal/saas/outbox ./internal/saas/...`
- Dashboard tests: `npm --prefix tools/agent-memory/dashboard test`
- Dashboard type check: `npm --prefix tools/agent-memory/dashboard run typecheck`
- Dashboard production build: `npm --prefix tools/agent-memory/dashboard run build`
- Full Go verification: `go test ./...`
- Static verification: `go vet ./...`
- GraphRAG adapter contract and pinned-version verification: to be fixed in `design.md` after transport selection.

## Expected Project Structure

- `internal/core/`: provider-neutral graph revision, derived entity/edge/community, freshness, and review contracts.
- `internal/application/`: projection, scheduling, validated import, activation, query routing, hybrid assembly, and lifecycle orchestration.
- `internal/storage/sqlite/`: additive standalone graph metadata and provenance repositories.
- `internal/saas/`: hosted graph job, artifact, outbox, worker, tenant isolation, and operator adapters.
- `internal/engine/`: intent classification, graph candidate ranking, deduplication, clipping, feedback, and evaluation logic.
- `internal/api/` and `internal/cli/`: standalone readiness, update, rebuild, status, query, review, and rollback operations.
- `tools/agent-memory/dashboard/`: graph status, processing, relationship review, provenance paths, freshness, and operator controls.
- `tools/` or `deploy/`: version-pinned Microsoft GraphRAG adapter runtime selected by `design.md`.
- `.kiro/specs/graphrag-derived-index-integration/`: requirements, architecture design, and dependency-ordered tasks.

## Testing Strategy

- **Contract tests:** Freeze projection manifests, adapter requests/responses, GraphRAG output validation, and version compatibility.
- **Unit tests:** Cover normalization, stable identity, relationship-kind handling, trust state, query routing, ranking, deduplication, clipping, and freshness.
- **Storage tests:** Cover migrations, idempotent imports, immutable revisions, atomic activation, rollback, review carry-forward, and deletion repair.
- **Integration tests:** Run a pinned GraphRAG adapter against deterministic small corpora and validate imported entities, edges, communities, reports, and original evidence resolution.
- **Failure tests:** Exercise missing runtime, timeouts, malformed Parquet/output, model failure, partial artifacts, poison jobs, cancellation, stale revisions, and rollback.
- **Security tests:** Exercise secret exclusion, prompt injection, arbitrary-path rejection, artifact traversal, cross-workspace cache leakage, tenant isolation, export, and deletion.
- **Retrieval evaluations:** Compare basic-only, local graph, and global routes with fixed gold questions and cost/latency budgets.
- **End-to-end tests:** Prove write-now/search-now, asynchronous enrichment, cross-day association, graph-aware answer grounding, feedback suppression, deletion, rebuild, and basic fallback.

## Boundaries

### Always

- Preserve canonical Agent Memory identity, provenance, authorization, feedback, lifecycle, and deletion semantics.
- Validate all external GraphRAG artifacts before persistence or activation.
- Keep graph work asynchronous and bounded.
- Expose graph freshness and inference state wherever graph context affects retrieval.
- Cite original authorized evidence for answer claims.

### Ask First

- Enabling GraphRAG by default for existing workspaces.
- Changing the approved Python runtime, GraphRAG package version, lock tool, adapter image base, or hosted graph worker topology.
- Sending workspace content to a new external model provider.
- Changing default retention for raw GraphRAG inputs, caches, or output artifacts.
- Allowing graph-derived relationships to become approved without human review.
- Changing default recall routing or latency/cost budgets.

### Never

- Replace canonical memories with entities, community reports, or GraphRAG summaries.
- Block canonical writes on GraphRAG readiness or success.
- Treat semantic similarity or shared community membership as proof of book authorship, memory membership, causality, or factual support.
- Activate unvalidated, partially imported, cross-workspace, or evidence-free graph artifacts.
- Store credentials, raw chain-of-thought, unrestricted tool output, or unauthorized content in GraphRAG inputs or artifacts.
- Allow remote callers to choose arbitrary GraphRAG roots, artifact paths, database paths, executables, or prompts.

## Success Criteria

1. A fresh workspace operates exactly as before when GraphRAG is absent or disabled.
2. A new memory is immediately searchable and later becomes graph-enriched without being rewritten.
3. The Day-1 Book A and Day-10 discovery scenario produces reviewable entity/relationship associations that preserve distinct authorship and source provenance.
4. Basic, local graph, and global routes are explicit, bounded, observable, and safely degradable without an online GraphRAG dependency.
5. Every graph-derived answer claim resolves to original authorized Agent Memory evidence.
6. Incremental update, full rebuild, atomic activation, rollback, deletion repair, and review carry-forward are idempotent.
7. Standalone, self-managed, and hosted modes preserve equivalent graph lifecycle and retrieval semantics with proven tenant/workspace isolation.
8. Shadow evaluation demonstrates improved relational and corpus-wide answer coverage without regression in direct factual retrieval, grounding, security, or bounded cost.
9. Removing the GraphRAG adapter and its artifacts leaves all canonical memories and existing retrieval behavior intact.

## Open Questions for Product and Architecture Review

1. Which GraphRAG indexing method is the production default: standard, fast, or a policy-selected combination?
2. Which model provider and retention policy may GraphRAG use for local, self-managed, and hosted extraction/report generation?
3. What batch threshold and maximum freshness lag are acceptable for newly written memories?
4. May deterministic matches to existing explicit provenance be auto-approved, or must every GraphRAG inference require human review?
5. Which Agent Memory graph routes may be user-selected in the first production UI, and which remain internal intent-routing decisions?
6. What latency, token, cost, and measurable quality thresholds must local and global routes satisfy before leaving shadow mode?
7. How long should raw GraphRAG inputs, caches, inactive revisions, and community reports be retained?
8. Should GraphRAG-derived entity aliases influence the existing memory write-time relationship inference before human review?
