---
name: terminal-interface-release-gate
description: Verify Agent Memory's Go terminal interface, local/remote service contracts, keyboard workflows, private history, and absence of frontend dependencies. Use after terminal UI, action catalog, service transport, install, or packaging changes.
---

# Terminal Interface Release Gate

The primary interface uses Bubble Tea v2 and Lip Gloss v2. Read `.kiro/specs/tui-primary-interface/{requirements,design,tasks}.md`, `.kiro/specs/tui-workspace-ux/{requirements,design,tasks}.md` and `docs/terminal-interface.md` before changing it. The old embedded-dashboard gate is superseded.

## Maintain domain contracts

1. Derive actions from the actual Go request/response schema before changing `internal/tui/console_catalog.go`. Prefer labeled fields and selection autofill. Use structured payloads only for domain documents such as skill proposals/seminar evidence packets.
2. Keep all I/O outside Update, tag asynchronous results, and invalidate generations on form/destination/workspace changes. An old workspace load or editor callback must not replace the current model.
3. Local mode invokes `api.NewMux` through the handler transport without opening a listener. Remote mode uses HTTP. Always bind workspace in both query and the actual body schema. Graph mutations use `scope.workspace_id` and no top-level workspace. Account-level client/model/rights handlers have strict bodies and must omit workspace. Client updates omit path ID from the body; deletion revisions are query parameters. Scheduler `force` is a query parameter.
4. Preserve note/client optimistic revisions, graph version intent, rights acceptance, skill authority, private credential masking, and mutation review. The TUI assembles the note body with the editable title as its first Markdown heading, replacing an existing first heading and retaining the remaining body. New paths are generated once per form and are editable in Advanced options. The backend still derives the stored title from its first physical line. Note revision restore uses an integer `revision`.
5. Source imports keep their explicit format/rights basis and reuse the established optional local principal. Successful ingestion projects edition/asset/work/job metadata into a source note; reimport must not duplicate active source notes. Report partial ingestion/registration failure accurately.
6. Chat/search records are private local history, bounded to 50 per workspace and 128 KiB snapshots. Browser localStorage is not migrated. Exports are create-only private files. The editor changes only a private draft before confirmation.
7. Keep daily navigation visible on wide terminals and searchable with Ctrl-K on every supported size. Ctrl-P searches the full action inventory; a searches contextual actions. Resolve selection through the filtered records and only autofill an action from a record in the same domain. Fetch full note bodies/revisions before editing. Reconcile authoritative returned notes after create/edit/trash/restore and remove permanently deleted records from selection.
8. Daily forms show primary fields while preserving selected identities/revisions and safe defaults in hidden advanced fields. Ctrl-O reveals all options. Rune-aware caret editing, multiline paste and the private editor must retain drafts on failure. Enter/Ctrl-S review mutations; y alone dispatches them. Busy mutations cannot be navigated away from or submitted twice.
9. Ask opens its composer directly with Graph Auto. Render actual recall-preview fields: memories_included_full (fallback memories_used), weak_memories, context_block and graph routing/context. Do not invent an LLM answer or source evidence. Latest result and question draft are scoped to the current workspace; private saved history remains authoritative across launches. Evidence-to-result navigation must preserve readable scrolling.
10. The OpenCode-inspired shell places navigation/context on the right, Home actions in a centered card and Ask input in a framed bottom composer. Four-direction focus uses existing record detail and navigation state: right opens a record, then reaches the workspace panel; left returns to list/content. Up/down selects or scrolls; the compact destination dialog accepts right/Enter and left/Esc. Input takes priority: multiline up/down moves between logical lines with a preferred rune column; Tab/Ctrl-Up/Down changes fields. Pending detail cancellation invalidates generations. Resizing cannot strand focus in a hidden panel. At 60x16, compact Ask headers must leave retrieved content visible.
11. Project discovery shows every registered project and its scoped count. Manager.List reads existing SQLite files in read-only/query-only mode with per-query and scan deadlines, never creates a missing DB or migrates one, and returns count_status ready/unavailable. An unavailable count is not zero. The picker loads counts asynchronously without changing selection; Workspaces identifies the current project and Enter switches. Local defaults honor registered DBPath, explicit --db wins, remote routing bypasses the registry, and the embedded console finds the registry independently of the selected DB directory. Keep small-terminal navigation and empty-view project recovery visible.

## Automated gates

### Diagnose an empty workspace

Check the header's selected workspace before changing transport code. Compare `am stats --workspace <selected>` with the registered database path and the exact CLI/TUI `--db` or `--api` settings. Core local reads use `ListRecentMemoriesByWorkspace` and `MemoryService.Search` in `internal/cli/tui_backend.go`; a zero count is distinct from an unavailable/error state. Use `Ctrl-W` / `w`, arrows and Enter to choose the intended registered workspace, or an explicit `am start --workspace <name>`. Never broaden scope or copy records to conceal an empty workspace.

For real-data diagnosis, query only counts/metadata first. Verify the installed executable's list and Search against consistent SQLite snapshots in a temporary home, with existing model/runtime paths supplied explicitly. Keep telemetry, private history and native graph work in those snapshots. Assert `Search memories`, the submitted query and a visible `Relevance` score, then Right/Enter reading; the bounded-list caption can be clipped. After switching, wait for `Overview · Project: <name>` in the header, since a project's preview also contains its name before switching finishes. Drain PTY output while waiting for exit. Report retrieval separately if shutdown cannot be verified.

Run the affected focused tests first, then:

```bash
go test ./internal/tui ./internal/cli ./internal/api ./internal/config ./internal/bootstrap .
go vet ./internal/tui ./internal/cli ./internal/api
go test -p 4 ./...
go build -trimpath -o <temporary-path>/agent-memory ./cmd/agent-memory
git diff --check
```

If real local harness opt-in variables contaminate unrelated test fixtures, clear only those opt-ins for the test process; do not read or print credentials. The verified 2026-10-11 clean test invocation used:

```bash
env -u AGENT_MEMORY_HARNESS_JEV -u AGENT_MEMORY_HARNESS_JEV_EGRESS go test -p 4 ./...
```

Audit active code/build paths for frontend remnants with `rg`, excluding historical reviews and specs. React/Vite, HTML/CSS/JS assets, operator HTML, native dashboard launcher, dashboard command/bootstrap/install flags, frontend CI/build targets and frontend container ports/volumes are removed. JSON compatibility routes containing the word dashboard and legacy placeholder migration handling are headless contracts; do not remove them merely by name. Node used by MCP is independent of the removed frontend.

## Real terminal smoke

On macOS/Linux, use the bundled standard-library Python script:

```bash
python3 .agents/skills/terminal-interface-release-gate/scripts/pty_smoke.py \
  --binary <absolute-tested-binary> \
  --model-dir <absolute-installed-MiniLM-model-directory> \
  --runtime-lib <absolute-ONNX-runtime-library>
```

The script creates an isolated home and two project databases, seeds one semantic memory and opens a real PTY. It verifies picker/project counts, switching to an empty project and back to populated search, the right workspace panel, actual arrow escape sequences through Home/list/read/panel focus, direct grounded Ask, filtered destination/command palettes, essential note fields, review cancellation without a write, confirmed creation, filtered reopening and full-revision editing. SQLite must contain exactly one titled note with the updated body at revision 2, followed by a successful terminal exit. It never uses the user's workspace database. Runtime/model files are existing read-only inputs. The runtime flag is optional when the environment already resolves it correctly. It restores/cleans the terminal process and temporary data on errors.

Bubble Tea redraws only changed terminal cells. Even a changed word can arrive partially: Records → Reading may emit only a cursor move and “ading.” The bundled terminal_screen.py maintains cells across incremental UTF-8, cursor movement, erase, character insertion/deletion and scroll; cursor motion cancels pending last-column wrap. It ignores styling/title/keyboard protocols. Assert on that screen, not raw fresh substrings. Pre-alt-screen restart banners use the retained transcript because entering the TUI clears them. This reader covers the verified workflow, not every terminal protocol.

Run its standard-library unit checks with `python3 -m unittest discover -s .agents/skills/terminal-interface-release-gate/scripts -p test_terminal_screen.py`. Test partial redraws, Unicode/wide cells, private protocols, character insertion/deletion, last-column wrap and backward tabs (CSI Z, eight-column stops). Bound Go layouts at 60x16, 80x24, 100x30 and a large viewport; test forms, palettes and long Unicode detail content as well as Home.

Use `--command start --pick-workspace` to verify the no-flag launcher and registered-workspace selector. Use `--command restart --source-dir <absolute-checkout>` to verify an actual rebuild/publication/launch: the script copies the executable into its private scratch install directory first, then verifies both scratch binaries match. It never replaces the user's installed copies during this smoke test. `tui` retains its explicit workspace contract; start/restart are interactive launch controls and turn selection into an explicit workspace flag. API launch requires the flag.

After an authorized installed-binary update, verify the actual `command -v agent-memory` and `command -v am` targets, both launch commands/help, executable byte identity and a real start smoke. Restart validates the exact module, bounds the Go build, uses the atomic replacement helper, restores canonical bytes if alias publication fails, and opens the rebuilt process with the original db/model/API flags. Preserve user configuration and workspace rules; a binary-only refresh does not need the full install workflow.

## Concurrent checkout work

When another task is editing unrelated backend files, preserve its changes. A temporary Git snapshot of HEAD with only the interface patch can verify the interface independently. Initialize/stage that temporary repository because root repository-hygiene tests use `git ls-files`. Report snapshot verification separately from the shared checkout. Never replace an installed binary from a snapshot that omits concurrent backend work.

After success, update task checkboxes, package any new reusable check, record What/Where/When/How and retrieval feedback in agent-memory, end the solution episode, and run session-end. This gate does not certify externally signed launch or counsel/UI evidence.
