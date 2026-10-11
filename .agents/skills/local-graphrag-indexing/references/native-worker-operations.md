# Native worker operations and hybrid search

The native implementation is specified in `.kiro/specs/native-graph-hybrid-retrieval/` and executed by `internal/cli/native_graph_daemon.go` and `internal/application/native_graph_worker.go`. Confirm the PATH binary includes this implementation before diagnosing a queue.

## Enable and observe

The installed runtime needs `graph.enabled: true`, an absolute `graph.executable`, and the completion/embedding provider/model settings in the user configuration. Supported environment equivalents include `AGENT_MEMORY_GRAPH_ENABLED`, `AGENT_MEMORY_GRAPH_ADAPTER`, `AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER`, `AGENT_MEMORY_GRAPH_COMPLETION_MODEL`, `AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER`, and `AGENT_MEMORY_GRAPH_EMBEDDING_MODEL`. `AGENT_MEMORY_GRAPH_ADAPTER` maps to `GraphConfig.Executable`. Use the local Ollama routes supplied by the installation.

CLI startup loads `~/.agent-memory/agent-memory.env`, whose values override process environment variables. For an isolated runtime, set `AGENT_MEMORY_ENV_FILE` to an absolute private settings path. Loading and toggle writes use that selected file; a missing selected file does not fall back to the user's file. Detached daemon children inherit the selection. Preserve the default settings and precedence during normal operation.

A registered workspace explicitly opts in through `agent-memory graph-index enable --workspace <registered-name>`. This creates or re-enables its scoped configuration; it does not enable unrelated workspaces. Start the native daemon with `agent-memory serve --start`. Its registry scan processes enabled workspaces, and canonical writes only record intent. Check `graph-index readiness --workspace <registered-name>` for the actual adapter prerequisite and `graph-index status --workspace <registered-name>` for active revision, pending changes and job state. Use `serve --status` separately for daemon health.

The background launcher calls the existing `configureBackgroundProcess` helper so the daemon survives its launching terminal/tool session. Verify `serve --status` in an independent later command; the initial healthy reply alone is insufficient. On POSIX, the detached daemon has its own process group/session. A long index in an earlier enabled workspace delays later ones because there is one adapter process at a time; report the current running workspace and the later workspace's pending state separately.

Subsequent changes coalesce at 50 changes or a maximum 15-minute wait. First builds run immediately. A manual `graph-index update`/`rebuild` needs an idempotency key and the current active revision via `--expected-revision` when one exists. `cancel`/`retry` use `--job-id`. Retry produces a new candidate revision; a cancelled or failed revision is never mutated into a ready one. Disable/re-enable operations also need the expected active revision when one exists.

The worker renews scoped leases, validates projected canonical evidence and output hashes, imports a complete revision and atomically publishes the revision/job/journal cutoff. Changes made after the captured projection stay pending. Configuration versions are stored on revisions; old configuration versions are stale even when the new journal is empty. A previously shipped reviewed prompt is upgraded with a configuration version bump. Unknown prompt overrides are unsupported.

## Prioritize one enabled workspace

The native worker runs one registered workspace at a time. Within explicit operator authorization, record the target, daemon identity, active revisions and every interrupted job in a private recovery file. Cancel other running builds through scoped graph-index operations as the worker reaches them. Preserve enabled configurations and model routes. Once the target is actually running, submit one fresh retry for the latest interrupted job per other workspace; these remain queued behind the active target. Cancellation and retry do not establish graph completion. Monitor the target's active revision and successful publication separately.

Use the public status/operations API when coordinating multiple registered workspaces. GET `/api/v1/graph-index/status?workspace=<exact-name>&configuration_id=default`; POST `/api/v1/graph-index/operations` with `scope.workspace_id`, `configuration_id`, `action`, `job_id`, current `expected_revision`, and a fresh `idempotency_key`. `cancel` targets the current running or queued job; `retry` targets an eligible terminal job and creates a new revision. Re-read state after an uncertain result rather than repeating a mutation with a new key. Workspace registry entries are under the `projects` key; use the exact registered name and database path, preserving underscores/hyphens.

Verify daemon health independently after cancellation. A single untraced process exit does not prove cancellation killed the service. The Unix real-runner regression `TestNativeGraphRealRunnerCancellationPreservesServiceAndNextWorkspace` checks separate process groups, child reap, parent HTTP/context survival, retained journal changes and next-workspace publication without user data or model calls. If operational tracing is needed, run the same PATH `serve` in a detached session with a private diagnostic log, real ONNX model directory and existing PID/address controls. Restore interrupted work even after a daemon restart.

## Large local first builds

Read projection/document counts and private parquet footer row counts, cache metrics and completed workflow names. Do not print cache/model/request contents. Supported local `ollama_chat` Qwen3 8B/14B structured indexing sets `reasoning_effort=none`; pinned LiteLLM maps this to top-level Ollama `think=false`. This retains reviewed prompts and gleaning. Cloud, other models and embedding calls keep their own settings. A synthetic inference probe must confirm final content without reasoning before relying on this policy; model availability alone is insufficient.

Native wall time defaults to two hours. For an authorized large local first build, a user configuration can explicitly set `graph.timeout_seconds` up to 86,400 seconds. Preserve unrelated YAML fields and record the change; an already running adapter keeps its original deadline, so a fresh daemon/job is required. Settings changes apply on new adapter invocations. Never activate partial output to work around a deadline. Query comparison must follow an activated, validated revision; graph-required mode proves graph use, and any deliberate stale allowance must be reported with pending writes and revision identity.

## Incremental state and retention

Active and previous raw states are retained privately under `graph.job_root`; other retained states expire after 24 hours. Metadata binds scope, revision, build/models and file hashes. Base copying streams files into a new directory and verifies hashes; Python cannot mutate the active base. Creates may use incremental updates. Deletes, supersession/configuration changes and absent/changed raw state trigger full rebuilds. Native SQLite graph write transactions reserve the writer before reading to avoid heartbeat-induced `BUSY_SNAPSHOT`; busy_timeout alone cannot repair that stale snapshot.

The adapter supplies numeric document ordinals and standard loader metadata when passing DataFrames directly to GraphRAG. A full run alone does not prove this update contract: null `human_readable_id` values break `update_final_documents` while computing the next ordinal. `tests/test_incremental.py` runs the installed document/chunk/update workflows without model calls and checks the merged IDs and original content. Workflow failure reasons retain a safe workflow identifier and exception class without raw exception text.

## Retrieval

Ordinary CLI and HTTP search share `MemoryService.SearchHybrid`. `--graph-mode auto` routes relational/dependency questions to local discovery and corpus-wide questions to community reports; direct lookups remain basic. `--graph-mode local_graph` also enables graph discovery for a keyword query. `--graph-mode basic` selects vector/term retrieval. `--graph-required` needs explicit local/global mode and fails if usable graph evidence is unavailable. Stale graphs fall back by default; `--graph-allow-stale` explicitly allows graph discovery while current vector/term candidates remain independent.

Graph discovery and vector/exact-term discovery produce a union of canonical memory IDs. Fetch those IDs directly from SQLite, then score actual similarity plus path/evidence/trust and lifecycle signals. Do not search vectors to locate an already-known ID, and do not replace direct discovery with graph-only IDs. Low cosine can still be useful connected evidence. Explicit locator keywords supply exact terms; arbitrary prose is not automatically the exact-term index.

Responses identify selected route, active revision, freshness, paths/communities and fallback reason. Empty/weak graph discovery yields `graph_no_evidence`. Every reference is rechecked for current workspace, fingerprint, supersession, cold tier and suppression. A global report is withheld if any supporting evidence is invalid. Legacy `--mode terms` and `--mode graph-expand` retain their separate contracts. Queries never launch the Python GraphRAG indexing adapter.

## Verification commands

`go test ./internal/application ./internal/storage/sqlite ./internal/retrieval ./internal/cli ./internal/api`

`go test -race ./internal/application ./internal/storage/sqlite -run 'TestNativeGraph|TestHybridSearch' -count=1`

`AGENT_MEMORY_REAL_GRAPH_SMOKE=1 go test ./internal/application -run '^TestNativeGraphRealLocalAdapter$' -count=1 -v -timeout 25m`

`tools/graphrag-adapter/.venv/bin/python -m pytest tools/graphrag-adapter/tests -q`

Run full Go tests, vet, build and `git diff --check` for final delivery. Strip only unrelated inherited harness opt-in variables when checking the baseline; do not print or forward secrets. Verify installed CLI transport flags and an isolated native-daemon write → index → search flow before operational rollout. Record actual real workspace job status rather than claiming an index completed merely because the daemon started.

Run `python3 .agents/skills/local-graphrag-indexing/references/native-daemon-smoke.py` to verify the PATH CLI with isolated registered synthetic data, a synthetic offline adapter, and the real installed ONNX embedding provider. Pass `--binary /absolute/candidate/path` to test a candidate through an isolated PATH installation. The script checks automatic indexing after a write, retrieval of a fresh unindexed memory, operator update, and required-graph CLI/HTTP parity; it stops its own daemon and removes its temporary data. Its default model directory is `~/.agent-memory/models/all-MiniLM-L6-v2`. This smoke does not replace the separate real GraphRAG/Ollama full-and-incremental test above.
