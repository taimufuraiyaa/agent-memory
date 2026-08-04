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
- Replacement keeps the existing executable continuously addressable: there is no delete-before-rename window in which shell invocations can observe a missing command.
- If the final replacement operation fails, the previously installed executable remains intact and executable, and temporary replacement artifacts are removed.
- Dry-run reports the plan without replacing targets, leaving build artifacts, or writing hooks/dashboard/configuration.
- Upgrade source discovery contains no developer-specific absolute paths.

### R4 — Cross-device verification

- Tests execute from directories outside the repository and use temporary data/bin/config roots.
- No tracked file contains a developer-specific home path.
- Focused lifecycle suites and the repository-wide Go suite pass.

### R5 — Runtime doctor remediation

- `agent-memory doctor` remains read-only by default and reports independent, sanitized checks with concrete next actions.
- Executable diagnosis verifies effective PATH membership and provides shell-specific guidance, including `fish_add_path` for Fish, without editing shell profiles.
- Workspace and database checks resolve `workspaces.json`, honor registered database paths, and prove read/write file access without changing database content.
- `--fix` and `--repair` select the same bounded repair mode; `--dry-run` requires that mode and writes nothing.
- Safe repair is limited to creating and owner-permissioning the data root plus `models`, `logs`, and `onnxruntime` directories.
- Text and JSON output summarize pass, warning, failure, skipped, and overall health; repair output retains before/after summaries and planned/applied paths.
- Doctor never automatically downloads artifacts, rewrites shell/agent configuration, reconstructs registry/database content, or controls processes.
