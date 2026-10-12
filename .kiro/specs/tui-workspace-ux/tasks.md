# Terminal Workspace UX Tasks

## Multi-project discovery follow-up — 2026-10-11

- [x] Specify the all-project discovery contract and verify read-only, project-scoped count summaries with unavailable states and caller cancellation.
  - Files: requirements/design, workspace manager and summary tests. Verify missing files are not created and shared files count only the named project's rows.
- [x] Add asynchronous launch-picker counts, readable Workspaces list/details, current-project count and empty-view recovery; retain selection, bounds and input guards.
  - Verify model/keyboard tests, missing-count compatibility, selection during loading and narrow/wide terminal rendering.
- [x] Fix default registered database routing and local registry lookup independently of explicit/custom database directories.
  - Verify explicit override, unregistered fallback, malformed registry, remote bypass, real backend list/switch and no creation of a second empty default store.
- [x] Extend the reusable terminal release check with multiple projects and an empty-to-populated query journey; run focused/full/vet/build, atomically refresh installed binaries and verify actual installed start.
- [x] Record verified durable facts/workflow, finish task checkboxes and complete the solution episode/session.

### Multi-project verification

- Failing-then-passing regressions cover scoped counts in shared/special-character database paths, no migration or missing-file creation, cancellation, custom registered path routing, explicit override, unregistered fallback, malformed local registry and remote bypass. Real local console list/switch reads the normal registry even when selected database files live elsewhere.
- TUI/CLI/API/workspace checks, the full Go suite with the two documented Jev opt-ins cleared, scoped vet, make build and canonical retrieval/storage invariants pass. Project/empty presentation fits 60x16 through large viewports; every right-panel destination remains visible at 100x20. Legacy remote count documents remain readable.
- Real built start/picker, isolated restart/picker and installed am start/picker pass two-project counts, deliberate empty-to-populated switching and Search/reading, grounded Ask, arrows/palettes, cancelled and confirmed note writes, one titled note at revision 2, and exit 0. The picker uses a custom registered path without a database override.
- Live read-only listing sees 12 registered projects with available counts. Both installed executables match tested SHA256 `6055672a29cc9e33218ec41a45b59b82d471ca98c4611491ec18dcde89bedf31`.
- Seven parser tests pass after observing renderer CSI P deletion and CSI Z backward-tab movement. The screen reader supports cell insertion/deletion, eight-column backward tabs and pending-wrap cancellation. Assertions wait for a switched Overview header and a visible query/Relevance score, avoiding preview names and clipped list captions. Every reusable skill file remains below 12,000 characters.
- Durable semantic `a266c659-410a-49e1-b733-5d2835cf5863` and procedural `e3e2f8db-a65c-4376-8656-91de8d4ca657` record verified routing and release guidance. Structured session-end completed episode `2cc5225b-a88d-48d3-9537-61a971a3b40c` with verified successful summary `f330ad5d-177f-44a4-a68f-29841104d9a2`.

## Empty-workspace query diagnosis — 2026-10-11

- [x] Compare the reported `times` scope with the installed CLI and registered database; distinguish empty data from unavailable transport without broadening scope.
  - Evidence: installed `am stats --workspace times` returns zero memories; initial `agent-memory` database inspection returns 951. Both registered paths match the local CLI defaults.
- [x] Verify installed `am` browse and query behavior against isolated database copies, and document workspace selection/retrieval troubleshooting in the guide and reusable gate.
  - Evidence: `times` overview/browse matches zero and its query succeeds with an empty result; the populated `agent-memory` copy displays records and returns readable `OpenCode` search results. No application code or installed executable change is needed for this report.

## OpenCode and four-direction follow-up

- [x] Inspect official OpenCode TUI reference and current focus contracts; extend requirements and architectural design before implementation.
- [x] Implement spatial list/read/workspace and Home action movement, compact switcher arrows, page scrolling and stale-read cancellation.
  - Verify: keyboard regression tests, filtered note identity, resize recovery and mutation/input guards. Files: workspace_navigation.go, workspace_focus.go, model.go, console.go, workspace_ux_test.go.
- [x] Implement multiline vertical caret movement with preferred column and explicit field movement.
  - Verify: Unicode/uneven-line editing, bounds, Tab/Ctrl-Up/Down, drafts and no accidental navigation/submission. Files: workspace_forms.go, console.go, workspace_ux_test.go.
- [x] Apply the OpenCode-inspired neutral shell, right context panel, Home action card, bottom Ask composer and centered palettes with clear focus hints.
  - Verify: representative render states and bounds at minimum/normal/large viewports, readable focus without color and real PTY arrows. Files: view.go, workspace_shell.go, workspace_ask.go, workspace_records.go, console_view.go.
- [x] Update guide/reusable gate, run focused/full/vet/build and expanded actual PTY checks; atomically refresh both installed PATH binaries and smoke start.
- [x] Record What/Where/When/How/Feedback and durable verified results; complete episode/session and checklist.

### OpenCode follow-up verification — 2026-10-11

- Full `internal/tui` tests and focused terminal TUI/CLI tests pass. New regressions verify spatial arrow focus, filtered note identity, Left/Esc pending-read cancellation, compact activation and resize recovery, logical-line Unicode caret/preferred-column behavior, page reading, right context placement and retrieved content visible at 60x16.
- Final `env -u AGENT_MEMORY_HARNESS_JEV -u AGENT_MEMORY_HARNESS_JEV_EGRESS go test -p 4 ./...`, scoped vet, `make build` and diff checks pass. Verification notes use portable home paths after the repository hygiene check caught an absolute developer path.
- Expanded real PTY start/picker, final isolated restart rebuild and actual installed alias start/picker pass Home/list/read/workspace arrow sequences, grounded Ask, filtered palettes, cancelled/confirmed writes, filtered note reading/editing, one titled note at revision 2 and clean exit.
- Tested build and both installed PATH targets match SHA256 `722be9f404425f35cacba6183f59b32e39c7be8b7e813943157eb1e7734a0fa4`.
- The reusable release gate now includes a standard-library incremental screen reader and four passing parser tests for partial redraws, split UTF-8/wide cells, title sequences and private keyboard/color protocols. Each skill file remains below 12,000 characters.
- Durable semantic memory `c4d3bdae-3508-4159-87f7-2cfe8165b8f5` records the new focus/layout contract. Procedural memory `31855788-853d-43bd-b3df-a07422ee6cc8` supersedes the earlier fresh-byte assertion guidance.
- Structured session-end completed episode `fd83121a-ece4-4cf5-8b25-da6d4e857532` with verified successful summary `ea212c2e-5b69-489b-8814-14d9098c4110`.

## Original daily-workspace redesign

- [x] Audit current TUI and legacy interaction requirements/source; establish daily workflow gaps and requirements/design.
  - Evidence: model/view/console/action/history code; legacy Notes/Chat source at HEAD; unified-workspace-experience and vscode-workspace-explorer specs.
- [x] Implement the responsive workspace shell, visible grouped navigation, searchable destination/action palettes, focus and daily shortcuts.
  - Verify: model keyboard tests for filters/cancel/input guards and bounds at multiple terminal sizes.
- [x] Implement readable list/preview/detail, local filtering, active/trash notes, contextual create/edit/import and authoritative note-list reconciliation.
  - Verify: filtered selection and late-detail tests; actual note revision/create/edit tests; narrow/wide rendering.
- [x] Improve daily forms with primary/advanced fields, caret editing, multiline preview, stable automatic paths and explicit title/body composition.
  - Verify: Unicode insert/delete, advanced access, exact prepared request/review, note-heading preservation and cancelled/failed mutation safety.
- [x] Implement the direct Ask composer, typed grounded result/evidence/weak/graph states, retained latest context and history/recovery affordances.
  - Verify: actual recall-preview shape, missing/weak evidence, query preservation, backend errors and scoped history.
- [x] Update docs and reusable release checks; run focused/full/vet/build and real PTY daily workflows; refresh installed binaries.
  - Verify: installed start opens the new UX, identical executable copies, isolated persisted note flow and clean exit.
- [x] Record durable knowledge, verified evidence and terminal episode/session status; complete checkboxes truthfully.

## Original redesign verification — 2026-10-11

- `go test ./internal/tui -count=1` and focused TUI/CLI terminal tests pass. Keyboard regressions cover searchable palettes, domain-safe autofill, filtered full-revision editing, Unicode caret insertion/deletion and long-question visibility, evidence return/scroll, retained drafts, workspace reset, and trash/restore/permanent deletion.
- View bounds pass at 60x16, 80x24, 100x30 and 160x45 for shell/palettes and essential/advanced daily forms.
- `env -u AGENT_MEMORY_HARNESS_JEV -u AGENT_MEMORY_HARNESS_JEV_EGRESS go test -p 4 ./...`, scoped `go vet`, `make build`, and `git diff --check` pass.
- Expanded PTY release script passes for built `start` with picker, an actual isolated `restart` rebuild, and installed `~/.local/bin/am start` with picker. Each run verifies visible navigation, grounded Ask, filtered palettes, cancelled write, create/filter/read/edit, exactly one titled note at revision 2 and clean exit.
- `~/.local/bin/agent-memory` and `~/.local/bin/am` resolve on PATH and match the tested build; SHA256 `d0a89e9849bf8e089ca0eef4d8c293bb0b3ed0b840f5d06e41562171888d2fbc`. Help exposes both launch commands.
- Docs and the terminal-interface-release-gate skill/script carry the verified workflow. Both skill files remain below 12,000 characters. Concurrent backend work was preserved.
- Semantic memory `8b8fd680-a8f1-4509-9bf7-7f0d163c50af` and procedural memory `7e39d4f3-c198-4596-be65-fc6bdddcb107` record the durable findings. Structured session-end completed episode `359b5369-f847-4d65-b09f-5a0beff29ca9` with verified successful summary `7a755925-c67b-491d-bccd-0260b1a50d73`.
