# Primary Terminal Interface Tasks

- [x] Inventory existing terminal and web workflows; derive requirements/design from the existing Bubble Tea spec and the user's replacement instruction.
  - Verify: inspect gateway, API routes, install/build/container references and existing TUI tests.
- [x] Implement and verify bounded console transport, explicit workspace binding, envelope errors, local handler transport and remote mode.
  - Verify: HTTP transport tests and real temporary SQLite/API integration.
- [x] Implement destinations, list/detail scrolling, named action forms, validation, selection/revision autofill, guarded asynchronous operations and stale-message rejection.
  - Verify: model tests including cancel, errors, busy state, resize, Unicode/paste and terminal sanitization.
- [x] Complete memory, Ask, notes, sources/library/rights, history/activity, graph, skills, client/settings, workspace and migration workflows.
  - Verify: action inventory coverage and representative real backend journeys.
- [x] Remove frontend sources/assets, HTML operator view, dashboard command/bootstrap/install paths and native dashboard launcher; preserve shared daemon utilities.
  - Verify: removed routes return 404, CLI no longer advertises dashboard, Go API/CLI tests pass.
- [x] Remove frontend build/CI/container dependencies and update active documentation/configuration; record supersession of old UI specs.
  - Verify: source/reference audit and build without frontend artifacts.
- [x] Package verified repeatable terminal-interface maintenance/verification into a reusable skill; record durable memory and close the episode/session.
  - Verify: skill size limit and accurate What/Where/When/How/Feedback accounting.
- [x] Complete release verification and update all task checkboxes truthfully.
  - Verify: focused tests, full Go suite, vet, Go build, diff check and real PTY smoke.

## Installed launch follow-up — 2026-10-11

- [x] Add start and restart launch entry points with explicit workspace flags or a registered-workspace picker, preserving the tui agent-operation boundary.
  - Verify: failing-then-passing command/selection tests; nonterminal and cancel paths open no domain services or rebuild.
  - Files: root.go, terminal launch command/selector and tests.
- [x] Implement bounded rebuild, source validation, atomic binary replacement/rollback, and forwarding to the rebuilt TUI.
  - Verify: failed build prevents launch; actual file publication/rollback tests; flags reach the new process and temporary artifacts are removed.
  - Files: terminal launch command/tests; reuse upgrade publication/source helpers.
- [x] Document commands, extend reusable PTY/install verification, rebuild and replace both existing PATH executables.
  - Verify: focused/full Go checks, vet/build, installed binary identity/help, real PTY start/picker/restart; task and memory/session accounting completed.
  - Files: README.md, terminal-interface.md, terminal-interface-release-gate skill/script, installed executables.

Launch follow-up verification: the full shared-checkout Go suite (two Jev opt-ins cleared), vet and make build passed. Real PTY start selected a registered workspace and completed the confirmed-note flow. A private scratch restart rebuilt from the checkout, synchronized both executable copies and opened the fresh TUI. Both existing PATH targets at `~/.local/bin/agent-memory` and `~/.local/bin/am` were atomically refreshed to identical tested bytes; installed `am start` passed the picker/note/cancel/confirm/exit smoke. Command help and diff check passed. No daemon restart, model installation, workspace-rule rewrite or user-data migration was performed.

## Verified release evidence — 2026-10-11

- Focused transport/model/history and real SQLite/API tests passed. Interrupted graph/orchestration mutation responses retain their generated idempotency key for retries.
- The full shared-checkout Go suite passed with only `AGENT_MEMORY_HARNESS_JEV` and `AGENT_MEMORY_HARNESS_JEV_EGRESS` cleared for the test process. `go vet ./...`, the Go binary build, and `git diff --check` passed.
- The current binary passed the reusable real PTY smoke: overview, destinations, bracketed multiline note paste, cancel without write, confirm, exactly one persisted note, and clean exit.
- Source/build audit found no remaining React/HTML/CSS frontend sources or frontend container/build paths. MCP's separate npm integration remains.
- Unrelated native GraphRAG changes in the shared checkout were preserved. Existing user SQLite databases were not migrated or removed. Browser-only localStorage drafts are not migrated.
