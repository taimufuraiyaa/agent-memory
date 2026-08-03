# Multi-Workspace Daemon

`agent-memory serve` is one local daemon for all registered workspaces. It
listens on `127.0.0.1:3211` by default, reads routing metadata from
`~/.agent-memory/workspaces.json`, and lazily opens each registry entry's
existing `db_path`. Databases remain separate; requests select one by the
`workspace` field or query parameter.

## Upgrade from workspace-specific services

Stop every older `agent-memory serve` process before starting the daemon. An
old process reports `service_mode: fixed_workspace` (or omits the field) from
`/health`; the new daemon reports `service_mode: multi_workspace`.

```bash
agent-memory serve --stop
agent-memory serve --start
agent-memory serve --status
agent-memory doctor
```

If a legacy workspace-specific PID file remains, inspect the listener with
`lsof -nP -iTCP:3211 -sTCP:LISTEN`, stop that legacy process normally, and then
start the daemon. Do not run one port per workspace.

Hooks and MCP should use the stable URL `http://127.0.0.1:3211`. Unknown or
deleted workspace names are rejected and never create database files.
