# Debug Session: dashboard-scheduler-sync

Status: OPEN

## Symptom
- Dashboard Overview still shows `Background lifecycle disabled`.
- User reports nothing changed after the previous fix.

## Expected
- When Memory Service is running from the menubar, the dashboard should show scheduler/lifecycle as enabled.

## Hypotheses
1. The browser is using an old dashboard process or stale frontend bundle.
2. The dashboard stats endpoint still returns `scheduler.enabled = false`.
3. The PID fallback path is incorrect at runtime for the dashboard process.
4. The overview UI is not rendering the latest scheduler payload correctly.
5. The packaged app is using a different backend binary than expected.

## Evidence Plan
- Verify which dashboard process is serving the UI.
- Inspect live `/api/v1/stats` response from the running dashboard.
- Inspect live `/api/v1/scheduler/status` response from the running service.
- Compare dashboard process path, bundled binary path, and PID files.

## Notes
- No business logic changes before runtime evidence.

## Evidence Collected
- Live `serve` PID file exists at `/Users/time/.agent-memory/serve.pid`.
- Live `dashboard` PID file exists at `/Users/time/.agent-memory/dashboard.pid`.
- No `serve.agent-memory.pid` file exists in the live runtime directory.
- Live `GET http://127.0.0.1:52158/api/v1/stats?workspace=agent-memory` returns `"scheduler": null`.
- Live `GET http://127.0.0.1:3211/api/v1/scheduler/status` returns `"enabled": true` with workspace state for `agent-memory`.
- `internal/api/server.go` fallback currently reads `filepath.Join(svc.BaseDir, ".agent-memory", "serve.agent-memory.pid")`.
- `internal/cli/serve_command.go` actually writes `serve.pid` or `serve.<workspace>.pid` directly under `filepath.Dir(cfg.dbPath)`.

## Hypothesis Status
- H1 stale dashboard bundle/process: not supported by current evidence.
- H2 stats endpoint returns disabled/null: confirmed.
- H3 PID fallback path is incorrect at runtime: confirmed.
- H4 overview UI misrenders scheduler payload: not supported by current evidence.
- H5 packaged app uses different backend binary: not supported by current evidence.

## Root Cause Candidate
- The dashboard stats fallback looks for the wrong PID path and wrong filename pattern, so the dashboard process never detects the externally running `serve` scheduler and returns `scheduler: null`.

## Follow-up Evidence
- After rebuilding and restarting the packaged app, the menubar launched `serve` with `--workspace agent-memory` and `--db /Users/time/.agent-memory/.dashboard-placeholder.db`.
- Direct bundled `serve` startup printed: `serve lifecycle: workspace=agent-memory run error: open store: ping sqlite: unable to open database file (14)`.
- `internal/cli/serve_command.go` `listManagedWorkspaces()` returns `DBPath: s.cfg.dbPath` whenever `cfg.workspace` is set, even if `cfg.dbPath` is the dashboard placeholder path.
- This means the scheduler tries to open the placeholder DB instead of `/Users/time/.agent-memory/agent-memory.db` for workspace-scoped runs.

## Refined Root Cause
- There are two runtime issues:
  1. Dashboard stats fallback used the wrong serve PID path.
  2. Workspace-scoped `serve` scheduler runs can bind the scheduler to the placeholder dashboard DB path instead of the real workspace DB, causing SQLite open failures during lifecycle execution.

## Additional Evidence
- Terminal output from `am dashboard --stop` showed: `open /Users/time/.agent-memory/dashboard.agent-memory.pid: no such file or directory`.
- `internal/cli/commands.go` stop/status logic previously used only a single PID path derived from the inferred workspace.
- This fails when the running dashboard was launched under `dashboard.pid` while the current shell infers `workspace=agent-memory`.

## Applied Fix
- Dashboard CLI now falls back across both `dashboard.<workspace>.pid` and `dashboard.pid` for `--status`, `--stop`, and pre-start existing-process detection.
- Added focused CLI tests for PID fallback ordering and explicit PID-file precedence.
