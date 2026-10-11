---
name: local-graphrag-indexing
description: Configure, debug, or validate Agent Memory's local GraphRAG development indexing bridge and per-project reindex flow.
---

# Local GraphRAG indexing

Use this workflow for Agent Memory's local GraphRAG adapter, local Ollama model routes, or workspace/project Reindex failures.

## Before changing code or configuration

- Read `.kiro/specs/graphrag-derived-index-integration/requirements.md`, `design.md`, and `tasks.md` in that order. For installer model setup, also read `.kiro/specs/install-local-graphrag-setup/`.
- Search project memory using `agent-memory` on `PATH` with `--workspace agent-memory`; submit feedback immediately after search or recall.
- Keep Graph indexing opt-in. Adapter readiness, model availability, and successful inference are separate checks. Basic retrieval must remain available when GraphRAG is disabled or unavailable.
- Keep local routes device-local: standalone uses Ollama loopback; only the dev Compose API may opt into its fixed host gateway. Never pass ambient environment variables or arbitrary user model-server URLs into the adapter.

## Debug a queue or worker failure

1. Check the UI's Reindex readiness and the latest durable `graph_jobs` and `graph_revisions` rows before retrying. After a `202`/accepted result, do not click Reindex again; monitor that job.
2. For registered local projects, open **Settings → System → Graph index → Project queue** to see queued/running jobs across the local registry. Queued age is measured from job creation; running age is measured from its claim/update timestamp. The view is read-only and reports projects whose status could not be checked, so an incomplete scan is not mistaken for an empty queue. Use the selected project's existing controls for supported operations.
3. The local development worker currently visits registered projects in sorted name order and processes each claimed job synchronously. A long running job can therefore delay jobs in projects visited later; compare the queue state and elapsed time before deciding whether it is stuck.
4. If the queue request gets an unstructured HTTP 404, the API process may predate `/v1/local-projects/graph-index/queue`; check the response code and deployed API version instead of retrying the job. Confirm durable job/revision state and worker progress before rebuilding or restarting an API that may be processing a real index.
5. For container preflight, verify the adapter's image-pinned dependency lock path is present in the adapter's minimal environment. Do not forward the host's complete environment to fix a missing path.
6. Preserve the adapter's private per-job directory boundary. GraphRAG 3.1.2 reads prompt settings as file paths, so write reviewed prompts with exclusive creation and restrictive permissions inside that directory; do not accept prompt paths from a request or corpus record.
7. Match the configured vector-store width to the embedding model. `ollama/qwen3-embedding:0.6b` emits 1,024 dimensions; GraphRAG's default width is 3,072.
8. Validate the pinned artifact schema before normalization. In GraphRAG 3.1.2, text-unit evidence is in singular `document_id`; community reports reference numeric community ordinals while community `id` is a UUID, and `-1` represents no parent. Resolve these through explicit maps and fail closed on missing or ambiguous evidence.
9. Keep user-facing errors sanitized. Diagnose with safe state, stage, and progress metadata; never dump request JSON, prompts, corpus text, or raw model responses into logs or chat.

## Verify without indexing a user's corpus

- Run focused adapter settings, normalization, Go runner, configuration parser, and Graph Settings tests.
- Build and check the locked adapter in its development container; verify that absent configuration or unavailable models disables Reindex.
- Run one complete packaged-adapter smoke with synthetic text only, the configured local completion and embedding models, and no cloud credentials. Readiness alone is insufficient: verify all workflows finish, entities are extracted, and evidence-bound normalized artifacts are emitted.
- Validate the Compose files and `git diff --check`.
- Never use an existing workspace's memories as acceptance-test input. A separate real project reindex is a user operation and should run only after the user explicitly requests it.
- For a real reindex, check the persistent job state and adapter progress metadata. Local inference over a large project can take hours. A transient status-endpoint error does not prove the worker failed; confirm both job/revision state and worker process before retrying or restarting the API.
