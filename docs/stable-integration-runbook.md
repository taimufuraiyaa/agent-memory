# Stable Integration Runbook

## Install and verify

Build with `make build`, initialize a workspace with `agent-memory init`, then
run `agent-memory doctor`. The supported local path is macOS or Linux with Go
1.24+, Node 20+ for MCP/dashboard builds, and SQLite storage.

Doctor is read-only by default. Preview its bounded data-directory repairs with
`agent-memory doctor --fix --dry-run`, then apply them with `agent-memory doctor
--fix`. PATH, model/runtime downloads, daemon lifecycle, registry/database
reconstruction, and agent configuration remain explicit recommendations rather
than automatic repairs.

Start one multi-workspace service for every registered project with
`agent-memory serve --start`. See `docs/multi-workspace-daemon.md`; individual
workspace services and per-workspace ports are no longer required.

## Agent integration

Use `agent-memory connect codex --dry-run` or `agent-memory connect claude
--dry-run`, inspect the plan, then repeat without `--dry-run`. Run
`agent-memory demo` and `agent-memory doctor`. Roll back with `agent-memory
disconnect <agent>`; only agent-memory-owned entries are removed.

Codex and Claude Code use the v1 normalized hook contract. Capture is enabled
independently from context injection; prompt/session injection stays opt-in.
MCP uses the compact HTTP proxy profile and reports a machine-readable degraded
state when the local service is unavailable. The default profile exposes only
the workflow tools: write, search, recall, feedback, and session finalization.
Set `AGENT_MEMORY_MCP_PROFILE=expanded` on the MCP server when operator-facing
health checks or captured-session browsing must also be available.

For durable per-client selection, open Dashboard → System → Clients, register a
stable client ID, choose Default or Expanded, and add the displayed
`AGENT_MEMORY_CLIENT_ID=<id>` to that client's MCP environment. The saved record
is installation-wide, but the choice is independent for every client. Restart
or reconnect that client after changing its profile. The environment-only
`AGENT_MEMORY_MCP_PROFILE` path remains supported when no client ID is set.

## Trust and replay

Use `agent-memory audit` for filtered append-only mutation records and
`agent-memory import-jsonl <path>` for resumable sanitized transcript imports.
Replay is available through the dashboard and `/api/v1/replay/*`. Audit and
replay metadata never participate in normal memory retrieval.

## Connectors

Filesystem connectors require explicit non-symlink roots. See
`docs/connectors.md`. Network connectors are not supported in this release.

## Compatibility and degraded modes

| Surface | Stable support | Degraded behavior |
| --- | --- | --- |
| Codex | managed MCP plus six lifecycle hooks | doctor reports stale/missing config |
| Claude Code | managed MCP plus stable lifecycle hooks | unsupported config is not mutated |
| MCP | stdio server proxying local HTTP | explicit unavailable-service result |
| Dashboard | built static assets plus local API | empty/error states when API is down |
| Filesystem | polling, checkpointed rescans | per-instance degraded health |

Back up user configuration before first adoption. Connect already creates
restrictive backups; disconnect is idempotent and preserves unrelated entries.
