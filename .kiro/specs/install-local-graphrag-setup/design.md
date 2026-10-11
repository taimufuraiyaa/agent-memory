# Local GraphRAG Model Setup Design

## Context

Agent Memory's install TUI already supports an optional Ollama-backed query planner. GraphRAG uses independent completion and embedding routes, so selecting the planner currently does not configure indexing. The GraphRAG adapter builds a LiteLLM configuration and needs an explicit loopback `api_base` for Ollama. The CLI installer already has bounded Ollama installation/model-inventory helpers and writes user configuration through `agent-memory.env`.

## Chosen Design

Add a separate unchecked `Local GraphRAG models` installer component. The component opens a model-bundle phase with two completion choices: Qwen3 8B and Qwen3 14B. Each bundle includes Qwen3 Embedding 0.6B. The UI shows model IDs and approximate download sizes, and the review page repeats the bundle so a user can see the cost before confirming. The query planner remains a separate opt-in component and defaults off.

All installer side effects stay after review confirmation. Before pulling several gigabytes, the install process checks the configured adapter through its absolute executable's fixed `readiness` command with a bounded timeout and a minimal child environment. If the adapter is unavailable, it skips both model downloads. Once adapter readiness passes, the installer reuses the existing Ollama runtime management path and pulls the exact completion and embedding model IDs. It confirms both exact names through the local Ollama model inventory. If either pull or inventory check fails, the route is not activated.

The adapter uses `ollama_chat` for chat completion and `ollama` for embeddings. For those provider IDs only, generated settings set the backend-owned `http://127.0.0.1:11434` API base and omit cloud API-key placeholders. GraphRAG's LiteLLM schema requires a non-empty `api_key` field even for Ollama, so the adapter supplies the fixed non-secret literal `ollama`; the local service does not use it for authentication. Cloud providers keep the existing credential placeholders and receive no local endpoint injection. No caller-supplied model endpoint is accepted.

After adapter readiness and exact model inventory both pass, the installer persists the exact validated `AGENT_MEMORY_GRAPH_ADAPTER` executable path, writes the local provider/model values, and enables GraphRAG in the user's env file. If readiness fails, it does not write partial local routes or enable GraphRAG; it prints a concise reason and leaves existing GraphRAG settings intact. `--write-env=false` still installs only the selected runtime/models and does not mutate configuration.

The installer never calls GraphRAG full-index, incremental-update, or Reindex. Project indexing remains a separate explicit Settings operation after installation.

## Data Contracts

| Role | Provider | Model | API base | Credential |
|---|---|---|---|---|
| Completion/extraction | `ollama_chat` | `qwen3:8b` or `qwen3:14b` | `http://127.0.0.1:11434` | fixed non-secret `ollama` schema value; no authentication |
| Embedding | `ollama` | `qwen3-embedding:0.6b` | `http://127.0.0.1:11434` | fixed non-secret `ollama` schema value; no authentication |

The installer also persists `AGENT_MEMORY_GRAPH_ADAPTER` using the exact absolute, non-symlink executable path that passed readiness.

The existing Graph config persists provider/model identity and the enabled flag. The adapter settings fingerprint includes the derived API base so a route change produces a new settings fingerprint. The model bundle selection itself is transient TUI state until confirmation.

## Alternatives and Trade-offs

- Reusing the query planner selection was rejected because planner and index models have different roles, and silently conflating them makes privacy and quality choices unclear.
- Reusing cloud-compatible credential placeholders for Ollama was rejected because the local provider requires no credentials; the fixed schema value is not a cloud credential and is never read from the environment.
- Allowing a user-entered API base was rejected for the first local setup because it widens the route to remote hosts and creates an SSRF boundary; loopback is safer and sufficient for on-device inference.
- Automatically running a sample or full index during installation was rejected because that would incur unreviewed CPU/RAM usage and could process every project. Readiness is limited to the local service, exact model inventory, and adapter package; project Reindex remains explicit.
- Making model downloads the default was rejected because the selected model pair requires several gigabytes and devices vary substantially.

## Failure Modes and Edge Cases

- Ollama is not installed: only after confirmation, use the existing platform-specific installer; unsupported/missing package-manager paths fail without enabling GraphRAG.
- Ollama is installed but not reachable: bounded readiness fails; no model routes are written.
- A selected model is already present: exact inventory recognizes it and skips the pull.
- A model pull or verification fails midway: already-downloaded files may remain in Ollama, but GraphRAG env configuration is unchanged and GraphRAG stays disabled.
- The GraphRAG adapter path is absent, non-absolute, a symlink, or reports not-ready: model downloads are skipped and no route activation occurs; the user receives a sanitized setup message.
- Environment writing is disabled or fails: report that models may be installed but configuration was not applied; never claim GraphRAG is ready.
- A selected local model becomes unavailable later: worker failure remains contained, canonical memory and Basic retrieval continue, and no cloud fallback is attempted.
- Tiny terminal size: preserve current compact layout, model IDs, download estimates, and review action.
- User selects GraphRAG only, planner only, both, or neither: route phases follow only the selected components and Back preserves each choice.

## Security and Privacy

- Provider endpoints are hard-coded loopback constants in the adapter for `ollama` and `ollama_chat`; remote endpoint overrides are not accepted.
- The installer verifies the endpoint through existing redirect-disabled loopback HTTP helpers and exact model names.
- Adapter readiness receives no cloud credentials and runs with a bounded context.
- No cloud API key or key environment placeholder is persisted for local Ollama. GraphRAG settings contain only the fixed non-secret schema value, and existing cloud key variables remain untouched.
- Model downloads contact the Ollama distribution service after review; indexed user text stays on the local device during inference.

## Performance and Scaling

The Ollama inventory check is one bounded local HTTP request. Model pulls are sequential to avoid loading two models into device memory together; the completion model is pulled before the smaller embedding model. No graph job or project scan runs in the install path. Model download estimates are display guidance, not a disk-space guarantee, because Ollama storage and quantization can vary.

### Structured indexing inference policy

The supported `ollama_chat` Qwen3 8B/14B completion routes use the backend-owned call argument `reasoning_effort=none`. The pinned LiteLLM transport maps this string to Ollama's top-level `think=false`. Apply this only to these exact completion routes; embedding, cloud and other model routes retain their existing call arguments. The settings fingerprint includes the inference policy, while the reviewed prompt fingerprint and source correlation contract remain intact. Existing adapter processes hold their settings for the duration of a job, so the change applies on a fresh invocation.

This reduces generation spent on reasoning during a high-volume structured extraction job without removing extraction or gleaning passes. Alternatives were keeping implicit model defaults, truncating generation, reducing gleaning or changing models. Implicit defaults were observed to spend thousands of characters per response on reasoning; truncation risks incomplete entity records; reduced gleaning changes coverage; a new model introduces download and quality choices. Explicit non-thinking retains the selected model and complete workflow, but extraction quality can still differ and must be judged from grounded outputs rather than speed alone.

Unsupported transport arguments or malformed outputs must fail through the existing bounded worker/import path, preserving the previous active revision. No cloud fallback or user-controlled endpoint is introduced. Offline tests validate the generated schema and actual pinned request mapping; a synthetic local inference probe checks final content, zero reasoning and observed duration. Operationally, measure corpus size and progress, use an explicit bounded wall limit that fits the device, and activate only after complete artifact validation. Full corpus completion is required before graph/basic comparisons are reported as successful.

## Rollout and Verification

Keep the new component unselected for all existing users. Add tests first for TUI opt-in/model phase/review and exact environment serialization, then implement the CLI orchestration and adapter settings. Run focused Go tests and adapter tests, then the full CLI package and adapter suite. Do not fetch models or perform a real project Reindex as part of automated tests. User validation can opt into a local install and manually trigger Reindex for one representative project in Settings.
