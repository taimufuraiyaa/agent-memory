---
name: local-graphrag-indexing
description: Configure, debug, or validate Agent Memory's native GraphRAG worker, local Ollama adapter, indexing after memory inserts, and hybrid graph retrieval.
---

# Local GraphRAG indexing and retrieval

Use this skill for graph indexing after memory inserts, local model adapter failures, native daemon jobs, or graph-assisted search. Read [memory insert diagnostics](references/memory-insert-diagnostics.md) for the distinction between memory relations, durable indexing intent, and an active derived graph. Read [native worker operations](references/native-worker-operations.md) for the current execution and retrieval flow.

## Before investigation or changes

- Use `agent-memory` through PATH with `--workspace agent-memory` for project knowledge. Search before research and immediately submit honest retrieval feedback. Continue substantial work in a solution episode.
- Read `.kiro/specs/native-graph-hybrid-retrieval/requirements.md`, `design.md`, and `tasks.md` in that order. Consult `.kiro/specs/install-local-graphrag-setup/` for installer/model configuration. Historical GraphRAG specs may exist on other revisions; do not assume they exist in this checkout.
- Verify the installed binary's source/build identity. A declaration or enabled configuration does not prove a native worker exists in that build.
- Indexing requires both runtime enablement and explicit workspace enablement. Preserve disabled workspaces. Readiness of the adapter, installed models, successful inference and an active graph are separate observations.
- Keep local routes device-local. The native adapter uses Ollama loopback; only a supported dev container may use its fixed host gateway. Do not forward ambient environment variables or caller-provided model-server URLs.

## Diagnose a recorded but unfinished update

1. Read supported `graph-index readiness` and `graph-index status` surfaces for the explicitly authorized workspace. Compare active revision, pending count, current job, last state and successful timestamp.
2. Verify `serve` is running the updated native implementation. `internal/cli/native_graph_daemon.go` invokes `NativeGraphWorker.RunWorkspace`; `internal/application/native_graph_worker.go` owns projection, adapter execution, import and publication. Older installed builds may only have lifecycle scheduling.
3. The worker visits registered projects synchronously with one adapter at a time. A long build can delay projects visited later. A new memory is durable immediately, but subsequent graph batches coalesce at 50 changes or 15 minutes. An initial build or explicit operator job can run immediately.
4. Do not repeat an accepted operation blindly. Monitor its durable state. Failed work uses finite retry/backoff and becomes dead-letter; operator retry creates a fresh revision. Cancel/disable/lease loss prevents activation, and shutdown terminates the adapter process group.
5. Adapter/model failures and corrupt output preserve the previous active graph. Diagnose with safe state, field names, stage and counters. Do not print request JSON, corpus text, raw model responses or credentials.

## Verify adapter contracts

- Keep private per-job directories and reviewed prompts. GraphRAG reads prompt settings as paths; use exclusive private files rather than caller-supplied prompt paths.
- Match vector width to the model: `ollama/qwen3-embedding:0.6b` emits 1,024 dimensions. GraphRAG's default is 3,072.
- Compare the reviewed prompt fingerprint against the installed pinned package. Do not bypass a mismatched fingerprint. The native worker supports a versioned upgrade from the previous shipped reviewed fingerprint and rebuilds under the current one.
- In GraphRAG 3.1.2, text-unit evidence uses singular `document_id`; relationship endpoints use entity titles although entity IDs are UUIDs; reports use numeric community ordinals although community IDs are UUIDs. Resolve explicit unambiguous maps before normalization. `-1` means no community parent.
- Relationship descriptions are prose, not relationship-kind labels. Preserve them in the separate description field. Opaque inferred relationships stay proposed and do not become typed supporting facts by text guessing.
- Incremental indexing needs the complete verified raw base table/vector-store state copied into a new private directory, a bound base manifest, delta documents and the full current correlation map. A command flag or base revision ID alone is insufficient. Missing/incompatible state causes a full rebuild.
- Supplying `input_documents` directly bypasses GraphRAG's document loaders. Include numeric `human_readable_id` and the standard `creation_date`/`raw_data` metadata alongside the canonical `id`, `title` and `text`. Otherwise full indexing can succeed with null ordinals and incremental document concatenation fails when computing the next ordinal. Keep canonical identity in `id`; ordinals are adapter metadata. The offline real-workflow regression is `tools/graphrag-adapter/tests/test_incremental.py`.
- GraphRAG 3.1.2 incremental entity merging maps reused entity titles to previous UUIDs, but its community merge leaves delta UUIDs in membership lists. The adapter-owned incremental community workflow calls the upstream merge, then applies its explicit entity ID mapping and checks every resulting ID against merged entities before cleanup. Preserve this reconciliation rather than guessing aliases or accepting unresolved references. The offline real merge regression covers normalization and original canonical evidence.
- Diagnose adapter workflow failures with the bounded workflow name and exception class. Never emit raw exception text, model output or projected content as an error reason.

## Verification and knowledge capture

Run focused worker, graph query, hybrid, CLI/API and adapter tests, followed by required repository checks. Use the opt-in real packaged local-model integration with isolated synthetic data. Readiness alone is insufficient: prove full activation, incremental activation and original canonical memories returned by hybrid search. Never use a user's existing corpus as acceptance-test data; operational processing of already enabled projects is distinct from testing.

Use [the isolated native daemon smoke](references/native-daemon-smoke.py) to validate automatic insert processing, fresh unindexed retrieval and CLI/HTTP parity through a PATH CLI. The synthetic adapter and real ONNX embedding smoke complements the real GraphRAG/Ollama integration test.

Record durable verified findings with source paths/build identity and terminal status. Update the spec's completed tasks and this skill's runbook when behavior changes. Each skill file must remain below 12,000 characters.
