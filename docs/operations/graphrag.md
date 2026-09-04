# GraphRAG Derived Index Operations

## Operating boundary

GraphRAG 3.1.2 is an exact, offline Python dependency behind the `graph-adapter/v1` contract. It converts an immutable Agent Memory projection into signed `graph-artifact/v1` output. Agent Memory validates and imports that output into its own normalized SQLite or PostgreSQL graph store. Online Ask never calls GraphRAG, Python, or the adapter container.

The graph is disposable navigation data. Canonical memories, source evidence, Basic vector retrieval, identity, authorization, deletion, review, and audit remain owned by Agent Memory. A graph outage must not block writes or Basic retrieval.

## Installation and readiness

Build or obtain the adapter image only through `tools/graphrag-adapter`; require an immutable `@sha256:` image reference, non-root runtime, offline wheelhouse hashes, SBOM, license inventory, vulnerability result, and signature. The supported package and lock must both say `graphrag==3.1.2`. Run:

```sh
make graphrag-adapter-supply-chain
make graphrag-adapter-container-test
GRAPHRAG_UPGRADE_POLICY_ONLY=1 make graphrag-upgrade-certify
```

Hosted deployments use the `staging-graphrag` or `production-graphrag` Kubernetes overlay and set the adapter image by digest. `AGENT_MEMORY_GRAPHRAG_ENABLED=false` is the process-level default and kill switch. The worker refuses enabled startup without its signing and workload-attestation configuration.

Readiness is authoritative only when the UI/API reports: enabled, compatible adapter and artifact schema, an active revision, a current indexed watermark, and a fresh state. A running worker alone is not readiness. Verify Prometheus loads `deploy/saas/observability/graph-alerts.yaml` and the `agent-memory-graph-index` dashboard before enabling a workspace.

## Budgets and backpressure

Configure workspace limits for pending projection records, estimated input tokens, estimated cost in micro-USD, and artifact bytes. The worker enforces them before any adapter or model call. Treat a limit rejection as a capacity or configuration event; do not bypass it by splitting one logical revision into untracked jobs.

Track queue age, coalescing, projection/entity/relationship/rejection counts, indexing and query latency, tokens, cost, cache outcome, revision age, fallbacks, dead letters, and storage custody classes. Metrics and traces must contain bounded labels and identifiers only—never query text, entity names, source text, or report summaries.

## Workspace lifecycle

1. Keep Basic as the initial and fallback route.
2. Enable the workspace configuration with a configuration version, projection version, prompt fingerprint, model route, adapter version, and artifact schema.
3. Request `rebuild` for the first revision. The pipeline projects canonical data, runs the offline adapter, validates files and evidence, imports a pending normalized revision, then atomically activates it.
4. Request `update` for later canonical changes. Coalescing is allowed; ordering, watermark continuity, and idempotency are mandatory. A deletion or an adapter-state retention boundary forces a full rebuild.
5. Use `cancel` with the job ID. Cancellation must stop before activation; it never exposes a partial revision.
6. Use `retry` only after classifying the failure and preserving the original idempotency boundary.
7. Use `rollback` with the expected active revision. It atomically restores the previous compatible normalized revision.
8. Use `disable` to force Basic immediately. Disabling does not delete canonical data.

All mutations require the correct workspace scope, graph operator authorization, an idempotency key where applicable, optimistic revision intent, and a durable audit event. Use the dashboard controls or the corresponding `/api/v1/graph-index/*` standalone and `/v1/graph-index/*` hosted endpoints; do not modify graph tables directly.

## Freshness, review, and query policy

A stale graph may be inspected but must not be silently used. Auto and explicit routes follow workspace policy; a required graph route fails closed, while an optional graph route falls back to Basic with a visible reason. Local Graph expands bounded, explainable paths from Basic seeds. Global selects diverse community context, but community summaries are navigation material and never source evidence.

Review actions are approve, reject, annotate, supersede, and reconsider with optimistic record versions. Rejected records leave candidate views immediately. Ambiguous entity merges remain visible and must not manufacture authorship or other semantics. Feedback can target the request, route, path, entity, relationship, community report, or canonical memory.

## Routine checks

Before and after a rollout step, record status for all selected workspaces, Basic success rate and p95, graph p95, fallback reasons, freshness, queue and dead-letter state, cost, grounding, and deletion lag. Run `make graphrag-evaluate` for deterministic quality checks. Run all four certification harnesses after changes to queues, workers, storage, deletion, identity, validation, or model routing.

Backups must include canonical stores, graph configuration/review/audit state, normalized active and previous revisions, and immutable object manifests needed by policy. Restore tests must prove atomic active-revision recovery and canonical-only rebuild. Adapter state is an optimization, not the sole recovery source.

## Rollout sequence

Advance one stage at a time: shadow evaluation, explicit Local, Auto Local, explicit Global, then approved Auto Global. Each stage needs a completed observation window meeting the release thresholds and a recorded owner decision. Do not infer approval from a passing local harness. Roll back the route to Basic or disable the workspace on threshold breach. Process-level disable is the last-resort fleet kill switch.
