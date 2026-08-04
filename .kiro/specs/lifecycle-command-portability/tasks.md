# Lifecycle Command Portability Tasks

## Task P1 — Audit current behavior

- [x] Run focused install, init, upgrade, workspace, and portability tests.
- [x] Execute init in an isolated client project and data directory.
- [x] Reproduce absolute-source install outside the repository.

## Task P2 — Repair install source resolution

- [x] Add a failing absolute-source installation regression test.
- [x] Resolve the source package to its owning Go module.
- [x] Build from the module root with a module-relative package path.
- [x] Verify the focused regression and install suite.

## Task P3 — Verify and repair upgrade safety

- [x] Execute a confirmed source upgrade against a temporary binary.
- [x] Add a failing regression for every observed dry-run mutation.
- [x] Make dry-run read-only while preserving useful plan output.
- [x] Verify focused upgrade tests and isolated execution.

## Task P4 — Release gate

- [x] Re-run init/install/upgrade isolated flows with paths containing spaces.
- [x] Run focused lifecycle and portability tests.
- [x] Run repository-wide Go tests, race detection, and `go vet`.
- [x] Confirm the worktree is clean and record the durable outcome.

## Task P5 — Preserve executable availability during replacement

- [x] Trace the Fish `Unknown command: agent-memory` failure to the delete-before-rename window in the shared replacement helper.
- [x] Add a failing regression that observes continuous destination availability while replacing an existing executable.
- [x] Replace the destination with a single platform-appropriate atomic commit operation and preserve the old executable on commit failure.
- [x] Run focused replacement and CLI tests plus a Windows compile check.
- [x] Verify the installed `am` command resolves and reports dashboard status from a fresh Fish shell.

## Task P6 — Harden doctor diagnosis and remediation

- [x] Diagnose missing executable PATH entries with Fish-specific guidance and no automatic profile edits.
- [x] Resolve workspace membership and registered database paths, then verify read/write access without mutation.
- [x] Add `--fix`, validate dry-run mode, and idempotently repair the safe data-directory layout.
- [x] Report before/after summaries and planned/applied paths in text and JSON.
- [x] Cover PATH, custom database, repair, idempotency, dry-run, and summary behavior with red/green tests.
- [x] Run focused normal/race tests, `go vet`, a command build, and manual text/JSON flows.
