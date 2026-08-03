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
