# Local Answer Translation Requirements

## Objective

Allow a user to translate a non-empty Ask answer with an explicitly configured local text model while preserving the original grounded response and its evidence. The control must resemble a compact browser translation action: a small Translate button with a dropdown for target language, suppression, and settings.

## Assumptions

1. Translation applies only to the generated/displayed answer. Source evidence, durable-memory context, provenance, identifiers, and feedback targets remain unchanged.
2. The user selects the target language. The browser locale supplies only a default suggestion and is never treated as authoritative identity data.
3. Translation is optional and local-only. There is no cloud fallback and no automatic download or execution of a model without an explicit user action.
4. The existing loopback-only OpenAI-compatible local-LLM configuration is the source of endpoint, model, credential, timeout, and readiness state.
5. Qwen3 is the initial recommended general model because the existing installer supports it and its official model documentation describes multilingual translation. Other compatible text models may be configured without model-specific frontend behavior.

## User Stories

- As a user reading an answer in another language, I can translate it locally without sending the answer to a cloud translation service.
- As a user, I can choose a target language, switch back to the exact original answer, and see which local model produced the translation.
- As a user without a ready local model, I receive an actionable settings state instead of a broken or misleading translation.
- As a user, I can suppress the translation suggestion for the current answer without changing stored knowledge.

## Functional Requirements

### R1 — Local Translation Boundary

- Translation requests must use only the saved, enabled, reachable OpenAI-compatible local text model.
- The backend must reuse the existing loopback URL validation, redirect denial, secret handling, timeout, and model-readiness rules.
- Missing, disabled, unreachable, or model-missing configurations must fail with a bounded actionable error and must not invoke another provider.
- Translation input and output must be size bounded. Provider response bodies must not be reflected in errors.

### R2 — Translation Contract

- A request contains workspace, answer text, and one supported target-language code.
- The server sends a translation-only instruction that treats answer text as data, not instructions.
- The response contains translated text, target language, and public provider/model identity. It never returns credentials or raw provider payloads.
- Empty text, unsupported language codes, oversized text, malformed provider output, redirects, timeouts, and oversized responses fail explicitly.
- Translation is transient and does not modify the original Ask response, memories, notes, sources, or retrieval feedback.

### R3 — Compact Ask Control

- A non-empty Ask answer shows a compact Translate control with a translation icon and dropdown affordance.
- The dropdown provides a target-language selector, Translate action, an answer-scoped “Don’t suggest translation” action, and Translation settings.
- Translation settings expose the existing local endpoint, text model, API-key update, timeout, enabled state, readiness test, and save operations without exposing a stored API key.
- While translation runs, the control reports progress and prevents duplicate requests. Errors are announced in context while preserving the original answer.
- After success, the answer displays the translation and offers Show original. The original text remains byte-for-byte unchanged in component state.
- Workspace changes, new Ask submissions, and source-scope changes clear translated and suppression state.

### R4 — Capability and Deployment Behavior

- Translation is an explicit gateway capability. Standalone/local runtimes may advertise it; unsupported hosted runtimes do not render the control.
- The browser never supplies a filesystem path, database path, arbitrary provider URL, or model credential in a translation request.
- Settings mutations remain installation-local and use the existing secured local-LLM API.

### R5 — Accessibility and Responsive Behavior

- The button, dropdown, language selector, settings dialog, status, and original/translated toggle are keyboard accessible and have stable accessible names.
- The compact control must not force answer text wider than its container or overflow at 320 px.
- Translation status and errors use polite live regions; settings validation errors use an alert role.

## Commands

- Backend focused tests: `go test ./internal/localllm ./internal/api -run 'Test.*Translat'`
- Dashboard focused tests: `node --test tools/agent-memory/dashboard/tests/answer-translation.test.mjs`
- Dashboard suite: `npm --prefix tools/agent-memory/dashboard test`
- Type check: `npm --prefix tools/agent-memory/dashboard run typecheck`
- Production build: `npm --prefix tools/agent-memory/dashboard run build`
- Full regression: `go test ./...`

## Project Structure

- `internal/localllm/`: bounded local translation client and shared configuration.
- `internal/api/`: standalone translation endpoint and tests.
- `tools/agent-memory/dashboard/src/lib/knowledgeGateway.ts`: translation capability and UI-neutral contracts.
- `tools/agent-memory/dashboard/src/lib/adapters/standaloneKnowledgeGateway.ts`: standalone API mapping.
- `tools/agent-memory/dashboard/src/ui/workspace/AskView.tsx`: compact control, translated/original state, and settings dialog.
- `tools/agent-memory/dashboard/tests/`: translation UI contract tests.

## Boundaries

- Always preserve the exact original answer and unchanged evidence/provenance.
- Ask before enabling automatic translation, downloading a model, or adding managed-hosted translation.
- Never send translation content to an unvalidated endpoint, silently use a cloud provider, persist translated text as knowledge, or claim translation preserves evidentiary wording.

## Success Criteria

1. A configured local OpenAI-compatible text model translates a bounded Ask answer into a selected language.
2. The compact dropdown matches the requested interaction shape and remains usable at 320 px.
3. Original answer and evidence remain unchanged and recoverable after translation.
4. No-model and unavailable-model paths lead to actionable local settings without cloud fallback.
5. Focused, dashboard, full Go, embedded-dashboard, and live checks pass.
