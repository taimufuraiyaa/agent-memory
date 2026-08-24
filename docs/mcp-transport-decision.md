# MCP Transport Decision

The preview MCP adapter uses the warm local HTTP service exclusively. It does
not silently fall back to per-call CLI subprocesses.

Reasons:

- HTTP reuses open SQLite connections, query caches, and embedding providers.
- CLI fallback would add process and provider startup cost to every tool call.
- One transport avoids workspace-resolution and persistence-location drift.
- Service unavailability is easier to diagnose when it remains explicit.

Every successful tool result includes `_transport.mode=http` and
`degraded=false`. A failed service call returns an MCP tool error prefixed with
`transport=http degraded=true`. A future CLI fallback may be added only after
parity fixtures demonstrate identical storage, request IDs, clipping, and
errors, and benchmarks justify the added installation complexity.

The stdio handshake tests complete in tens of milliseconds on the development
machine; localhost proxy tests complete well below the initial 250 ms warm-call
target. Formal p50/p95 gates are owned by roadmap Task 6.4.
