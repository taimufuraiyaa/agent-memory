---
name: local-inference-provider-contract
description: Build or extend backend-owned configuration and readiness checks for OpenAI-compatible local model servers. Use when adding local text, vision, OCR, reranking, or enrichment providers without weakening citation provenance or exposing the API as an SSRF primitive.
---

# Local Inference Provider Contract

Use this workflow when connecting agent-memory to Ollama, LM Studio, vLLM, or another OpenAI-compatible server.

## Invariants

- Deterministic source parsing remains the citation baseline.
- Provider connectivity is not proof that inference processed a source.
- Generated artifacts record provider, model, prompt/version, source inputs, and processing state.
- API keys are write-only and never returned by status endpoints.
- Disabled, configured, enabled, reachable, and model-available are separate states.
- Parser-only operation remains available when optional inference is unavailable.

## Secure configuration workflow

1. Add red tests for absent configuration, invalid URLs, persistence permissions, secret preservation/redaction, model discovery, timeout, and redirects.
2. Restrict the default local-only rollout to `http` or `https` loopback hosts. Reject URL user info, query strings, fragments, and non-loopback IPs.
3. Deny HTTP redirects. A loopback endpoint must not pivot requests to another local service or remote host.
4. Bound request time and response size. Do not include provider response bodies in user-facing errors because they may contain untrusted or sensitive content.
5. Persist configuration in the backend data directory through a temporary owner-only file, sync, close, and atomic rename. Keep mode `0600`.
6. Preserve a stored secret when an update omits the key. Require an explicit clear operation to remove it.
7. Return only public configuration plus readiness facts and an actionable sanitized error.

## OpenAI-compatible readiness check

- Build the model-list URL by appending `/models` to the normalized base URL, which may already end in `/v1`.
- Send the configured bearer token only to the validated endpoint.
- Treat a successful 2xx JSON response as reachable.
- Match the exact configured text and optional vision model IDs against the returned `data[].id` values.
- Report reachable-but-model-missing independently from connection failure.

## UI decision gate

When an optional provider is not operational, offer:

- Set up and test the local endpoint.
- Continue with the deterministic parser.
- Cancel without modifying the source.

A remembered parser-only choice is device convenience, not an authorization decision. Fully scanned sources must remain OCR-pending or fail explicitly until a vision/OCR stage actually runs.

## Verification

- Focused provider and API tests.
- Race tests for provider configuration and API handlers.
- Dashboard tests, typecheck, and production build.
- Embedded-dashboard foreground and detached lifecycle smoke tests.
- Browser verification of setup, parser fallback, successful import, and a clean console.
- Repository-wide tests, vet, production dependency audit, portability scan, and `git diff --check`.

Relevant implementation:

- `internal/localllm/`
- `internal/api/local_llm_handler.go`
- `tools/agent-memory/dashboard/src/ui/LibraryWorkspace.tsx`
- `.kiro/specs/multi-format-book-import/`
