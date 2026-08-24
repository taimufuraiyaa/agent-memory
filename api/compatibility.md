# Hosted API and Event Compatibility

Status: initial v1 contract, provider neutral.

This document defines conventions shared by every hosted Agent Memory service.
The OpenAPI document and event schemas are executable companions to this text.

## Authentication and tenant selection

Hosted requests use `Authorization: Bearer <credential>`. The verified identity
resolves the subject, memberships, capabilities, session or credential, and
allowed tenants. `X-Agent-Memory-Tenant` selects one of those tenants; it never
grants membership. Protected requests fail with the same not-found response for
unauthorized and absent customer resources.

Every accepted request receives a server-generated `X-Request-ID`. A valid
client-provided request ID may be retained for correlation but is never trusted
as identity. `traceparent` carries distributed trace context.

## Idempotency

Create and command endpoints accept `Idempotency-Key`. Keys are scoped to the
authenticated tenant, operation, and canonical request hash for 24 hours. A
repeat with the same hash returns the first status and resource. Reusing a key
with a different hash returns `409 idempotency_conflict`.

Updates use a strong `ETag`; callers send `If-Match`. A stale version returns
`412 version_conflict`. Delete commands are idempotent and return the existing
operation when deletion is already in progress or complete.

## Pagination

Collections use opaque forward cursors: `?limit=50&cursor=...`. Responses carry
`items` and an optional `next_cursor`. Cursors are bound to tenant,
authorization version, filters, sort order, and a bounded expiry. Invalid or
cross-tenant cursors return `400 invalid_cursor` without revealing counts.

## Error envelope

All errors use a stable content-free shape:

```json
{
  "ok": false,
  "version": "v1",
  "request_id": "req_opaque",
  "error": {
    "code": "stable_machine_code",
    "message": "Safe user-facing explanation",
    "retryable": false,
    "recovery": {"action": "renew_attestation"}
  }
}
```

Validation details identify fields but never echo source text, prompts, object
keys, credential material, or provider diagnostics.

## Long-running operations

Upload finalization, ingestion, export, deletion, migration, and bulk rebuilds
return `202` with an operation resource. Operations have `queued`, `running`,
`succeeded`, `failed`, or `cancelled` states and monotonic versions. Safe error
codes and retry guidance are visible; internal stack traces are not.

## Event compatibility

Event `spec_version` follows `major.minor`. Producers may add optional fields in
a minor version. Consumers must ignore unknown optional envelope fields but
must reject unknown major versions. Event `data` is defined by the named event
schema and rejects undeclared properties, preventing accidental content export.
At-least-once consumers deduplicate by `event_id`; business effects additionally
use a deterministic operation key.

## Local compatibility map

| Local surface | Hosted v1 surface | Compatibility rule |
|---|---|---|
| Workspace argument | Authenticated tenant plus workspace ID | Client value selects only an authorized scope |
| `write` | `POST /v1/memories` | Preserve memory type, provenance, keywords, and duplicate behavior |
| `search` | `POST /v1/search` | Preserve exact/vector score semantics behind tenant filtering |
| `recall` | `POST /v1/recall` | Preserve token budget and evidence metadata |
| `feedback` | `POST /v1/memories/{id}/feedback` | Preserve outcomes and reconsolidation lineage |
| `session-end` | `POST /v1/sessions/{id}/complete` | Preserve observation-to-memory semantics |
| Local library import | Source upload and operation resources | Never silently upload local source bytes |
| SQLite audit | Hosted audit activity and security ledger | Preserve safe attribution; omit customer content |

## Explicit client mode matrix

No client infers hosted mode from the presence of a local database and no
client scans or uploads SQLite files. Portable source bytes move only through a
user-selected AMPB2 bundle.

| Client | Local mode | Hosted mode | Credential behavior |
|---|---|---|---|
| CLI | Existing commands, or local HTTP with `--api` | `agent-memory hosted ...` with a named profile | `hosted login --token-stdin` stores the token in macOS Keychain, Linux Secret Service, or Windows Credential Manager through the OS keyring; profile files contain URL and tenant only. `hosted logout` revokes the bearer credential before deleting it locally unless `--local-only` is explicit. |
| MCP | `AGENT_MEMORY_MODE=local` (default for backward compatibility) | `agent-memory hosted mcp --profile NAME` launches hosted mode with URL, tenant, and token injected into the child process | The launcher reads the token from the OS keyring; the MCP server never writes it to disk and fails closed when hosted credentials are absent. |
| Go SDK | `agentmemory.Config{Mode: ModeLocal}` | `agentmemory.Config{Mode: ModeHosted, TenantID: ..., TokenProvider: ...}` | Hosted mode requires a token provider; `OSKeyringTokenProvider` reads a named CLI-compatible OS-keyring profile, and current-credential revocation is exposed. Construction performs no network or upload action. |
| Dashboard | The local dashboard served by the local product | `/dashboard/`, visibly marked `data-service-mode="hosted"` | The hosted dashboard keeps the bearer token in page memory only and clears it when the page closes; browser storage is forbidden by test. |

Hosted CLI imports require an explicit `--bundle` file and
`--passphrase-stdin`. The command accepts the AMPB2 encrypted format only at
the service boundary; there is no command that accepts a `.db` path for hosted
upload. Local mode remains fully supported and cannot call hosted source import
through the compatibility SDK.
