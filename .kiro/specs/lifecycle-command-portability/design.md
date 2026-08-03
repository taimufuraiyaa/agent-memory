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
