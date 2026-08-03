# Lifecycle Command Portability Requirements

## Context

`agent-memory init`, `install`, and `upgrade` are client-device lifecycle commands. They must operate from arbitrary project directories and must derive all user, repository, data, binary, and Codex paths at runtime.

## Requirements

### R1 — Isolated initialization

- `init --base-dir` creates the workspace database and registry only under the selected data directory.
- `--no-rule` leaves the client project unchanged.
- Reuse and force behavior remain deterministic and covered by existing tests.

### R2 — Source-based installation from any client directory

- `install --src` accepts a relative or absolute main-package directory inside a Go module.
- The build resolves its module root independently of the client's current working directory.
- The installed binary is created atomically under `--bin-dir`.
- Data, Codex, dashboard, environment, and project-rule writes honor their explicit runtime destinations.
- Missing or invalid source falls back to copying the running executable only when no source directory exists; a present but invalid source reports an actionable build error.

### R3 — Safe upgrade

- Source upgrades build from an explicitly selected or runtime-discovered repository root.
- A confirmed upgrade replaces only the selected target and performs requested hook/dashboard work.
- Dry-run reports the plan without replacing targets, leaving build artifacts, or writing hooks/dashboard/configuration.
- Upgrade source discovery contains no developer-specific absolute paths.

### R4 — Cross-device verification

- Tests execute from directories outside the repository and use temporary data/bin/config roots.
- No tracked file contains a developer-specific home path.
- Focused lifecycle suites and the repository-wide Go suite pass.
