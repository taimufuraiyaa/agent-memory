# Lifecycle Command Portability Design

## 1. Decision

Treat the source package and the client project as independent locations. Resolve a source package to its owning Go module before invoking the compiler, run the build from that module root, and pass a module-relative package path. Keep the client working directory only for project initialization and rule installation.

## 2. Command boundaries

- `init` owns workspace registry/database creation and optional project rules.
- `install` owns dependency/data setup, binary installation, optional environment integration, and initial project registration.
- `upgrade` owns replacement of an existing binary plus optional refresh of already configured integrations.

These commands may call shared atomic file replacement utilities, but source resolution must not infer a specific developer checkout.

## 3. Source resolution

For an existing source directory, resolution starts from its absolute path and walks upward to the nearest `go.mod`. The build package is expressed relative to that module root. This supports absolute and relative source arguments and removes dependence on the caller's current module.

If no module root exists, installation fails before writing the final binary. Temporary build output stays in the target binary directory so the final rename remains atomic on the same filesystem.

## 4. Dry-run semantics

Dry-run is a read-only planning operation. It may validate flags, inspect source availability, determine target and integration paths, and report the selected method. It must not compile, create temporary artifacts, replace binaries, update dashboard files, or write project hooks. Hook previews should be represented as planned actions rather than calls to write functions.

## 5. Failure modes

- Source directory exists outside a Go module: fail with source/module context.
- Source path is inside a module but is not buildable: return compiler diagnostics and remove temporary output.
- Target directory is not writable: fail before replacement and preserve the existing binary.
- Client project already exists: install uses the established reinstall path for rules without recreating data.
- Explicit data or Codex directories contain spaces: all operations use structured process arguments and filepath joins.

## 6. Security and portability

Explicit runtime destinations take priority over defaults. Defaults derive from `os.UserHomeDir`, environment variables, the executable location, the current directory, or Go tooling. No username or checkout path is compiled into source, tests, docs, generated rules, or configuration templates.

## 7. Verification and rollout

Add a regression test that changes into a client directory outside the repository and installs from an absolute source package. Add dry-run mutation tests before altering upgrade control flow. Verify focused CLI/workspace/root suites, real isolated command flows, repository-wide tests, race detection for touched packages, and `go vet`. Changes are internal and additive to accepted CLI flags, so rollback is a code revert without data migration.

## 8. Atomic executable replacement

The replacement contract is continuous destination availability. The new executable is copied to a uniquely named sibling file, closed, assigned executable permissions, and then committed with a single platform replacement primitive. The destination must never be explicitly removed before that commit.

On Unix, renaming a sibling file over the existing destination provides the required atomic namespace switch. On Windows, replacement uses the operating system move primitive with replace-existing and write-through semantics because the generic rename operation does not consistently replace an existing file there. Both paths keep temporary and destination files on the same volume.

Alternatives considered:

- Delete followed by rename is simple and portable at the API surface, but exposes a missing-command interval and can destroy the working executable if the rename fails. It is rejected.
- Copying directly into the installed executable avoids rename portability concerns, but exposes partially written binaries to concurrent invocations and is rejected.
- Keeping versioned binaries behind a stable launcher would support richer rollback, but adds indirection and lifecycle complexity beyond this repair.

Failure handling preserves the old destination whenever the commit primitive fails and removes the sibling temporary file. A process that already has the old executable open continues normally, while new invocations resolve either the complete old image or the complete new image. The copy cost remains linear in binary size; the final namespace switch is constant-time and introduces no additional steady-state storage beyond one temporary binary.

The rollout changes only the shared install/upgrade replacement helper. Focused tests cover replacement of an existing executable and continuous path visibility under repeated replacement, followed by CLI package tests and a cross-platform Windows compile check. Rollback is a source revert and requires no data migration.

## 9. Doctor remediation hardening

Keep a strict boundary between `internal/doctor`, which owns independent read-only checks and sanitized typed results, and `internal/cli`, which owns repair planning, application, rechecking, and rendering. This makes the no-mutation default auditable and prevents repair callbacks from becoming hidden check side effects.

Executable diagnosis inspects the running file and normalizes its parent directory against effective PATH entries. Workspace diagnosis parses a minimal registry projection and confirms membership. Database diagnosis independently resolves the registered path, falls back to the conventional path only when registry resolution is unavailable, and opens the file read/write without issuing SQL. Thus malformed registry state does not suppress database evidence or trigger migrations.

The bounded repair registry initially ensures only the data root and `models`, `logs`, and `onnxruntime` directories. Existing contents and group/world permission bits are preserved while missing owner read/write/execute bits are added. Operations report only paths needing creation or permission changes, retain safely completed directory work after partial failure, and converge idempotently on retry.

Calling the full installer was rejected because it downloads artifacts and rewrites integrations. Embedding repairs inside checks was rejected because it weakens read-only guarantees. Automatic shell/PATH edits were rejected because activation, preservation, and rollback differ by platform. Those conditions remain actionable recommendations.

Doctor adds aggregate total/pass/warning/fail/skipped counts and an overall healthy flag; warnings represent degraded optional surfaces and only failures make health false. Fix mode retains pre-repair and final results. Filesystem work is constant per artifact, registry parsing is linear in local workspace count, network timeouts stay bounded, and the additional fix-mode pass only doubles bounded diagnostic work. Rollout is additive, preserves `--repair`, requires no migration, and rolls back as code while installer-owned directories may safely remain.
