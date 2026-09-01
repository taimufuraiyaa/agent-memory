# Multi-client memory enforcement tasks

## Phase 1 — Contract and installer foundation

- [x] Define a public contract-version marker and one canonical What/Where/When/How/Feedback policy body.
  - Acceptance: generic, Cursor, Codex, and Claude rule outputs contain the same marker and required workflow commands.
  - Verification: focused `internal/workspace` tests.
- [x] Make `all` and auto-detection cover Kiro without regressing existing IDE targets.
  - Acceptance: explicit Kiro is valid; `all` includes Kiro; `.kiro/` is detected.
  - Verification: target-normalization and install tests.
- [x] Upgrade Kiro prompts to include retrieval feedback and structured solution lifecycle behavior.
  - Acceptance: prompt-submit and stop hooks cover start/step/checkpoint/recall/promotion/finalization responsibilities without requesting chain-of-thought.
  - Verification: hook-content tests and JSON parsing.

## Checkpoint — Foundation

- [x] Workspace tests pass and repeated generation is idempotent.

## Phase 2 — Complete client connections

- [x] Make Claude Code connection install and verify `CLAUDE.md` plus MCP and hooks.
  - Acceptance: connect is complete; disconnect preserves unrelated Claude content and removes only managed content.
  - Verification: Claude adapter tests.
- [x] Add reversible Cursor and Kiro connection adapters.
  - Acceptance: both appear in the default registry; connect/verify/disconnect operate only on owned files.
  - Verification: adapter and CLI connection tests.
- [x] Update CLI target/help text and backup coverage for all supported clients.
  - Acceptance: user-visible choices and backup paths match implemented targets; upgrade can apply `--ide all` across all registered projects.
  - Verification: command tests.

## Checkpoint — Connections

- [x] Focused workspace, integration, and CLI tests pass.

## Phase 3 — Tool and UI awareness

- [x] Expose the complete solution workflow in the default MCP profile while keeping diagnostics expanded-only.
  - Acceptance: ordinary agents can start, update, checkpoint, resume, hand off, recall, and promote solution paths.
  - Verification: MCP list/call tests.
- [x] Add Kiro to client profiles and dashboard controls; correct profile descriptions.
  - Acceptance: Kiro profiles validate through API and UI tests; profile copy matches actual tool sets.
  - Verification: Go profile tests and dashboard tests/typecheck.
- [x] Add contract-aware diagnostics with truthful enforcement reporting.
  - Acceptance: missing/stale rules fail for detected clients and Cursor is not reported as hook-enforced.
  - Verification: doctor tests.

## Checkpoint — Product surface

- [x] MCP, client-profile, dashboard, and doctor focused tests pass.

## Phase 4 — Rollout and evidence

- [x] Document the all-client upgrade, restart requirements, enforcement matrix, and verification commands.
- [x] Run isolated natural verification through public CLI and MCP surfaces for Kiro, Cursor, Codex, and Claude Code.
- [x] Run full regression tests, vet/typecheck, and production builds.
- [x] Store durable implementation/verification knowledge and finalize the Agent Memory session.

## Phase 5 — Cross-project CLI path hardening

- [x] Increment the generated operating contract and add an explicit PATH-installed CLI rule.
  - Acceptance: every rule derived from the canonical contract requires `agent-memory` on PATH, forbids `./bin/agent-memory` outside source-repository development, and directs missing-command failures to install/repair guidance.
  - Verification: focused `internal/workspace` regression proves the old contract fails and the new generated surfaces pass.
- [x] Propagate the updated contract to every registered project.
  - Acceptance: project-owned content is preserved; every applicable generated rule contains the new contract marker and CLI path instruction.
  - Verification: reinstall all registered projects, run doctor/inspection checks, and confirm no installed managed contract retains the prior marker.
- [x] Complete regression and session evidence.
  - Acceptance: focused tests, full workspace tests, vet, diff checks, and durable Agent Memory records pass.

## Phase 6 — Codex permission-preserving reinstall

- [x] Preserve explicit Codex permission selection without aborting reinstall.
  - Acceptance: user-authored `sandbox_mode` or `default_permissions` remains unchanged; the managed Agent Memory profile is refreshed without a competing default selector; Codex rules/hooks and later `--ide all` targets are written.
  - Verification: red/green workspace regressions cover direct config regeneration and the complete multi-client write path.
- [ ] Verify and release the repaired reinstall flow.
  - Acceptance: focused and full Go tests, vet, a rebuilt global CLI, the original `agent-memory reinstall --ide all` command, doctor, and diff checks pass while unrelated dashboard work remains untouched.

## Risks

- Shared rule-file preservation is high impact; cover it before adapter changes.
- Default MCP schema growth can expose stale dashboard assumptions; derive assertions from exact tool names.
- Host hook formats can evolve; verify generated JSON and avoid claims beyond tested host surfaces.
