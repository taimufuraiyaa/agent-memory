# Local GraphRAG Model Setup Requirements

## Objective

Let a user explicitly install and configure device-local models for GraphRAG during the Agent Memory CLI install flow, without requiring a cloud AI account. Keep this choice optional, distinct from the local query planner, and safe for existing workspaces.

## User Stories

- As a privacy-conscious user, I can opt into local GraphRAG models during the interactive install and see their approximate download size before confirming.
- As a user with a smaller device, I can choose a lighter completion model or a higher-quality model with a larger download.
- As a user, I can decline local model setup and continue with the rest of Agent Memory installation.
- As a user, I can install local models without accidentally reindexing a workspace.
- As an operator, I can verify that the configured GraphRAG completion and embedding routes point only to the local Ollama service.

## Acceptance Criteria

1. The install component list presents a separate optional **Local GraphRAG models** choice, distinct from the local question planner, and leaves both AI components unselected by default.
2. Selecting Local GraphRAG models offers Qwen3 8B and 14B completion choices, paired with Qwen3 Embedding 0.6B; review displays both model IDs and approximate combined downloads (about 5.8 GB or 9.9 GB).
3. No Ollama runtime or model installation begins before the user confirms the review screen. Declining the component makes no GraphRAG model or route changes.
4. After confirmation, the installer first checks the configured adapter. If it is unavailable, no Ollama model is downloaded. Otherwise, it reuses or installs Ollama at the fixed loopback endpoint and verifies the exact selected completion and embedding model IDs are available.
5. Only when both models are present does installation write local GraphRAG model routes. The adapter maps these providers to `http://127.0.0.1:11434`; it rejects or ignores caller-supplied remote endpoints for these local providers.
6. GraphRAG is enabled only if the selected models and configured pinned adapter both pass readiness. On success, persist the exact adapter executable path that passed readiness. Otherwise, preserve existing Graph settings and report the sanitized reason; failed installation must not leave a partial active route.
7. Installer setup never invokes a GraphRAG index, update, or Reindex operation. Users continue to trigger per-workspace/project Reindex explicitly in Settings.
8. Existing cloud provider routes are not changed when the local GraphRAG option is not selected. No cloud credentials are required or written for Ollama.
9. Headless installation supports an explicit `--local-graph-model none|qwen3:8b|qwen3:14b` setting with equivalent confirmation semantics supplied by command selection; model downloads happen only when that flag selects a model.
10. Tests cover default opt-out, model bundle selection, delayed side effects, partial model failure, missing adapter, route serialization, local endpoint confinement, and no automatic Reindex.
11. Structured indexing with the supported local `ollama_chat` Qwen3 8B/14B models explicitly disables inference thinking through the pinned transport's supported call arguments. The model IDs, reviewed evidence prompts, gleaning passes, endpoint, canonical evidence contracts and cloud/embedding routes remain intact. Verification must prove the serialized Ollama request uses top-level `think=false`; inference readiness alone does not prove a complete graph.

## Data and Configuration Contract

- Completion provider/model: `ollama_chat` / `qwen3:8b` or `ollama_chat` / `qwen3:14b`.
- Embedding provider/model: `ollama` / `qwen3-embedding:0.6b`.
- Ollama endpoint: backend-owned `http://127.0.0.1:11434` for both roles.
- On success, `AGENT_MEMORY_GRAPH_ADAPTER` records the same absolute, non-symlink executable path that passed readiness.
- Local Ollama needs no cloud credentials; generated GraphRAG settings omit API-key environment references for these providers. GraphRAG's LiteLLM schema requires a non-empty key field, so settings use the fixed non-secret `ollama` value, which the local server does not use for authentication.
- `AGENT_MEMORY_GRAPH_ENABLED` is set true only after both local model checks and adapter readiness succeed.
- Installation does not accept arbitrary model URLs or endpoints.

## Boundaries

- Always: keep graph indexing opt-in; preserve parser/basic retrieval; protect existing cloud settings unless local setup is explicitly chosen; show downloads in review; keep Reindex a user action.
- Ask first: adding a new runtime/dependency, changing the default route, or moving local model downloads ahead of review confirmation.
- Never: silently download several GB, use a cloud fallback when Ollama fails, accept a remote endpoint as “local,” or reindex every workspace from the installer.

## Success Criteria

- A user can choose local models, review the exact model pair and approximate download size, and configure a loopback-only GraphRAG route after readiness passes.
- Declining setup has no effect on cloud routes or existing memory data.
- A failed install cannot enable GraphRAG or trigger indexing.
- Existing installer, Graph adapter, and project Reindex tests pass.

## Verification Commands

- `go test ./internal/cli ./internal/bootstrap`
- `go test ./internal/config ./internal/application`
- `tools/graphrag-adapter/.venv/bin/python -m pytest tools/graphrag-adapter/tests`
- `git diff --check`

## Risks and Rollout

- Local model extraction may be slower or less consistent than hosted models. The installer offers explicit size/quality trade-offs and does not claim that installation proves index quality; pilot a representative project before relying on the graph.
- A user may have a running Ollama server with a different configuration. The feature uses only loopback and checks the exact required model IDs before enabling GraphRAG.
- Model inventory success does not prove that a full corpus index will fit available memory. The first Reindex remains an explicit per-project operation, and existing GraphRAG process limits and failure fallback remain authoritative.
- Roll out behind the install TUI opt-in and explicit headless flag; no existing installation is opted in automatically.
