# Local GraphRAG Model Setup Tasks

- [x] 1. Add installer selection and review for an optional local GraphRAG model bundle.
  - **Acceptance:** Planner and Graph models are separately opt-in; both default off; Graph choices pair Qwen3 8B/14B with Qwen3 Embedding 0.6B and show combined size before confirmation; model phase navigation and Back preserve selections.
  - **Verification:** `go test ./internal/cli -run 'TestInstallTUI.*(Graph|Model)|TestInstallSelection'`
  - **Files:** `internal/cli/install_tui.go`, `internal/cli/install_tui_test.go`

- [x] 2. Install and verify selected local models only after review confirmation.
  - **Acceptance:** Check configured adapter readiness before downloads; if unavailable, skip model pulls. Otherwise reuse/install Ollama at the fixed loopback endpoint, pull exact selected model IDs, check exact inventory, and make no GraphRAG environment change after partial failure.
  - **Verification:** `go test ./internal/cli -run 'Test(Install|ResolveHeadlessGraphModel|LocalGraphEnvironment|LocalGraphAdapterReadiness)'` and `go test ./internal/bootstrap -run TestOllamaRuntimeInstallPlanIsPlatformSpecific`
  - **Files:** `internal/cli/install_command.go`, `internal/cli/install_command_test.go`, `internal/bootstrap/ollama.go`, `internal/bootstrap/ollama_test.go`

- [x] 3. Route adapter completion and embeddings to loopback Ollama.
  - **Acceptance:** Adapter maps local Ollama completion and embedding providers to the fixed loopback API base and omits cloud API-key environment placeholders; it uses GraphRAG's required fixed non-secret Ollama schema value; existing cloud provider configuration remains unchanged; settings fingerprint includes endpoint values.
  - **Verification:** `tools/graphrag-adapter/.venv/bin/python -m pytest tools/graphrag-adapter/tests`
  - **Files:** `tools/graphrag-adapter/src/agent_memory_graphrag/settings.py`, adapter settings tests

- [x] 4. Gate Graph enablement on model and adapter readiness and complete verification.
  - **Acceptance:** Successful local install writes routes and enables Graph; missing/unready adapter leaves Graph disabled and routes unchanged; setup never invokes Reindex; graph remains disabled by default when the component is not selected.
  - **Verification:** Focused installer and bootstrap tests; `go test ./internal/config ./internal/application`; `tools/graphrag-adapter/.venv/bin/python -m pytest tools/graphrag-adapter/tests`; `git diff --check`
- **Files:** installer tests and any focused adapter readiness/config tests

- [x] 5. Disable inference thinking only for supported local Qwen3 structured indexing routes.
  - **Acceptance:** Qwen3 8B/14B completion calls serialize top-level Ollama `think=false`; cloud, embedding and other models retain their call contracts; evidence prompts and gleaning remain intact; real synthetic inference produces final content without reasoning before an operator restarts a full corpus build under sufficient bounded time.
  - **Verification:** Failing settings/transport regression followed by the complete adapter suite and an opt-in local synthetic inference probe; report real full-index activation separately.
  - **Files:** Adapter settings, settings tests, local indexing operations documentation.
  - **Evidence:** 65 adapter tests pass; the pinned transport serializes top-level `think=false`; a synthetic local JSON extraction returned final content with zero reasoning in 0.6 seconds on 2026-10-11. The operational full corpus build remains a separate completion check.

**Environment note:** The full `internal/cli` and `internal/bootstrap` package suites include existing `httptest` cases that bind a loopback listener. This sandbox rejects that socket operation, so those complete package runs require a socket-permitted test environment; the focused installer suite and non-network bootstrap test above pass here.
