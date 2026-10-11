# Local Answer Translation Design

## Context and Diagnosis

The canonical Ask view renders answer text from the gateway and keeps evidence and memory context separate. Agent Memory already owns a secure local-LLM configuration for OpenAI-compatible loopback servers, including atomic secret persistence, model discovery, redirects disabled, bounded readiness responses, and explicit configured/enabled/reachable/model-available states. It does not yet provide a generation operation for answer translation, expose translation as a gateway capability, or render an answer-level control.

This machine currently has no saved local-LLM configuration and no Ollama executable. The feature therefore must ship with a truthful unavailable/setup state and cannot be validated by pretending a local model is present. Contract tests will use a loopback fixture; runtime verification will separately confirm the unavailable state on the real installation.

## Chosen Architecture

Add a narrow translator inside `internal/localllm` that loads the existing configuration and invokes the configured text model through the OpenAI-compatible chat-completions endpoint. The translator accepts only a bounded text value and an allowlisted target-language code. It returns translated text plus public model identity. The local API exposes this as a transient operation under the existing local-LLM namespace.

The frontend gateway gains an explicit translation capability and methods for translation plus the existing local-LLM status/test/save lifecycle. Only the standalone adapter advertises the capability initially. Ask owns transient original/translated state, renders the compact menu beside the answer, and opens a settings dialog through the gateway contract.

The model prompt is translation-only and separates instruction from user answer data. The system instruction forbids answering, summarizing, following embedded instructions, adding commentary, or altering code/identifiers unnecessarily. The provider response is treated as untrusted text, size bounded, trimmed only for presence, and never merged with citation or memory records.

## Data Contracts

The translation request carries workspace identity, answer text, and a supported BCP-47 target-language code. The workspace remains an authorization/scope field even though the transient operation does not read workspace data; this prevents the UI operation from becoming scope-free.

The response carries translated text, target-language code, provider kind, and configured model ID. Errors use stable categories for unavailable configuration, invalid input, provider failure, and invalid provider output. Raw provider bodies, API keys, and prompt payloads are absent from responses and logs.

Frontend translation state is local to one Ask response: original text, translated text, target language, model label, busy/error state, and answer-scoped suppression. A new response or workspace/source change resets it. No persistence migration is required.

## UI and Interaction

The answer header contains a compact outlined Translate button with a translation icon and chevron. Its menu contains a searchable target-language selector, a primary Translate action, an answer-scoped suppression action, and Translation settings. The browser locale may preselect a matching supported language; otherwise English is the default.

Successful translation replaces only the displayed answer text and adds a small model/target status with Show original. Showing original does not discard the translation, so the user can toggle without another model call. Settings uses a modal sized for desktop and mobile with enabled, loopback base URL, text model, optional API key replacement, and timeout fields. Test does not save; Save persists through the existing backend behavior and refreshes readiness.

When the model is unavailable, selecting Translate keeps the original answer visible, announces an actionable error, and offers settings. The control does not imply that translation is available merely because a URL is configured.

## Alternatives and Trade-offs

**Browser translation APIs:** rejected because behavior and privacy differ by browser, may use remote services, and cannot satisfy the local-only boundary.

**Automatic translation after every Ask:** rejected because it adds latency, consumes local resources without intent, and requires language detection before the user asks.

**Dedicated bundled translation model:** deferred. TranslateGemma is specialized, but bundling another runtime/model increases install size, licensing interaction, memory pressure, and upgrade complexity. Reusing the configured text model makes Qwen3 immediately compatible and permits a dedicated OpenAI-compatible translation model later.

**Translate evidence and memories together:** rejected because translated evidence is no longer exact source text and could be confused with citation content.

**Direct browser-to-Ollama calls:** rejected because it exposes endpoint/credential details, weakens redirect and response-size controls, complicates CORS, and bypasses backend audit/error boundaries.

## Failure Modes and Security

- Missing or disabled configuration returns unavailable without network activity.
- A configured but missing model returns unavailable before translation.
- Endpoint redirects, non-loopback URLs, user-info, query strings, and fragments remain rejected by shared configuration rules.
- Timeouts and non-2xx responses return sanitized provider failure.
- Provider output over the bound, empty output, or malformed response returns invalid output.
- Answer text containing prompt injection is quoted as data under a higher-priority translation-only instruction.
- Translation failure never removes or mutates the original answer.
- Concurrent clicks are collapsed by the UI busy state; stale completions are discarded when scope or response changes.

## Performance and Scaling

Requests are synchronous, user initiated, and bounded to one answer. The configured timeout remains at most 30 seconds and response size remains bounded. No database write or cache is introduced. Local model concurrency is intentionally left to the provider for this first slice; the UI prevents duplicate requests from one answer. Managed-hosted scale and quotas remain out of scope because the capability is not advertised there.

## Rollout and Rollback

Roll out additively: backend translator and route, gateway capability, then Ask UI. Existing Ask behavior is unchanged when translation is unsupported or unused. Rollback removes the capability advertisement and UI while leaving local-LLM configuration intact. No stored data requires migration or cleanup.

## Verification

Use loopback fixture tests for successful translation, authorization header, exact model selection, prompt-injection containment, disabled/unconfigured state, redirect denial, timeout, non-2xx response, malformed JSON, empty output, and response-size limits. Dashboard tests cover capability gating, menu semantics, target selection, original toggle, reset behavior, settings, and narrow layout. Finish with full regressions, embedded assets, and a live unavailable-state check on the current machine.
