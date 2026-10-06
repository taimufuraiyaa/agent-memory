# Local Answer Translation Tasks

- [x] **Task 1: Add a bounded local translation application boundary**
  - Acceptance: The existing saved local-LLM configuration drives a translation-only OpenAI-compatible request; input, timeout, redirects, response size, output, and public model identity are enforced; missing/disabled/unavailable configurations make no provider request.
  - Verify: `go test ./internal/localllm -run 'Test.*Translat'` including success and failure fixtures.
  - Files: `internal/localllm/translation.go`, focused tests.
  - Dependencies: None.
  - Estimated scope: Medium.

- [x] **Task 2: Expose the transient standalone translation API**
  - Acceptance: A bounded workspace-scoped request returns translated text and public model identity; invalid input and provider states map to stable sanitized HTTP errors; no credential or raw provider body is returned.
  - Verify: `go test ./internal/api -run 'Test.*Translat'`.
  - Files: API handler, server route, focused tests.
  - Dependencies: Task 1.
  - Estimated scope: Small.

- [x] **Task 3: Extend the frontend gateway with an explicit translation capability**
  - Acceptance: Standalone maps translate/status/test/save operations; unsupported hosted adapters do not advertise translation; browser requests never contain database paths or provider credentials except an explicit write-only settings update.
  - Verify: focused gateway contract tests and dashboard typecheck.
  - Files: gateway types, standalone adapter, API client, gateway tests.
  - Dependencies: Task 2.
  - Estimated scope: Small.

- [x] **Task 4: Add the compact answer translation dropdown and settings**
  - Acceptance: Non-empty answers in capable runtimes expose an accessible Translate menu, supported target selection, translation progress/error, answer-scoped suppression, local settings dialog, model attribution, and Show original; workspace/source/new-response transitions reset transient translation state; 320 px layout does not overflow.
  - Verify: red-to-green translation UI contract test, full dashboard tests, typecheck, production build, and live narrow/desktop inspection.
  - Files: Ask view, workspace styles, translation test.
  - Dependencies: Task 3.
  - Estimated scope: Medium.

- [x] **Task 5: Complete production and local-unavailable verification**
  - Acceptance: Embedded production assets contain the translation control; a fixture local provider completes the public request; the real installation truthfully reports setup-required without cloud fallback; full Go, vet, MCP, dashboard, and diff gates pass.
  - Verify: `make build-with-dashboard`, `go test ./...`, `go vet ./...`, dashboard suite/typecheck, embedded smoke, and `git diff --check`.
  - Dependencies: Task 4.
  - Estimated scope: Medium.

## Release Checkpoint

- [x] Original answer and evidence remain unchanged through success and failure.
- [x] Translation is local-only, capability-gated, bounded, and credential-safe.
- [x] Qwen3 can be selected through the existing OpenAI-compatible configuration; no model is silently installed.
- [x] Compact desktop and 320 px interactions are verified.
