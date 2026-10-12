# Terminal workspace interface

Agent Memory's human interface is written in Go with Bubble Tea v2 and Lip Gloss v2. Build it with `make build` or install it with `go install ./cmd/agent-memory ./cmd/am`. Open the installed TUI in an interactive terminal:

```bash
agent-memory start
am start
```

Agent Memory serves all registered projects. Without a workspace flag, these commands show a project picker with memory counts; Enter opens the highlighted project's data and Esc cancels. Names appear immediately while counts load. A missing or unreadable database shows "count unavailable," and listing projects never creates or migrates their databases. To skip the picker, use `agent-memory start --workspace my-project`. The original explicit command remains:

```bash
agent-memory tui --workspace my-project
```

To rebuild from your Agent Memory checkout, update both installed executables, and open the updated TUI:

```bash
agent-memory restart
am restart --workspace my-project
```

Restart finds the source checkout using the existing upgrade search (cwd/ancestors, then common home locations). Use `--source-dir /path/to/agent-memory` to choose it explicitly. It requires Go on PATH, validates the Agent Memory module, limits compilation to two minutes, replaces each executable atomically and restores `agent-memory` if updating `am` fails. A failed build leaves the installed binaries available. Database, model and API flags carry through to the rebuilt process. Close the current TUI with `q` before invoking restart in the terminal; other TUI sessions and the headless daemon remain independent. API launch requires `--workspace` because the local registry cannot select a remote workspace.

Local mode opens the project's registered database and calls the existing Go application/API handlers in the same process. It opens no HTTP listener. `--db` selects an exact database file; `--model-dir` selects the existing embedding runtime. Unregistered explicit workspaces retain their default database path. The screen uses at least 60 columns and 16 rows; 100 columns and 30 rows give more space for forms and policy text.

If Memories shows "No memories in this workspace yet," check the workspace name in the header and compare it with `am stats --workspace my-project`. Memories, Search and Ask use that workspace's records. Open another registered workspace with `Ctrl-W` or `w`, select it with up/down and press Enter; alternatively launch `am start --workspace my-project`. Press `/`, type a query and Enter to search, or `5` to open Ask. An empty result can be correct for an empty workspace; a failed load or query displays an explicit unavailable message. When comparing CLI and TUI results, use the same workspace, `--db` and `--api` settings.

To use the headless multi-workspace daemon:

```bash
agent-memory serve --start
agent-memory tui --workspace my-project --api http://127.0.0.1:3211
agent-memory serve --status
agent-memory serve --stop
```

API mode uses the selected backend's database/models and does not open local embedding dependencies. It uses a bounded HTTP client and rejects redirects. Existing server permissions and admission checks apply in both modes.

## Workspace navigation

The layout follows the prompt and transcript hierarchy of the [OpenCode terminal interface](https://opencode.ai/docs/tui/): neutral panels, accent edges, a compact header, a framed Ask composer beneath the result, and a searchable command dialog. Wide terminals place workspace navigation and context on the right. Home offers a centered daily-action card; up/down selects an action and Enter opens it.

Notes, memories, sources and saved history show a list with a readable preview when space permits. Right or Enter opens the complete record; up/down scrolls while reading. Another Right reaches the workspace panel, while Left returns to the list. Left from the workspace panel returns to the current content; Right/Enter activates the highlighted destination. Smaller terminals expose the panel through the destination switcher and preserve full-width reading. Focus hints identify Records, Reading, Editing or Workspace panel. Leaving a pending note read with Left or Esc cancels its display intent, so a late result cannot reopen it.

| Key | Action |
| --- | --- |
| `1`, `2`, `3`, `4` | Home, semantic Search, recent Memories, Jev credentials |
| `5`, `6`, `7`, `8`, `9`, `0` | Ask, Notes, Sources, Graph, Activity, Settings |
| `Ctrl-K` | Search all workspace destinations |
| `Ctrl-P` | Search every named command |
| `Ctrl-B` | Focus the right workspace panel; compact terminals open the switcher |
| `Ctrl-W`, `w` | Open Workspaces to switch scope |
| `Tab`, `Shift-Tab` | Cycle the daily destinations outside editing |
| `a` | Search actions for the current destination or selected record |
| `n` | New note on Home/Notes, write a memory on Memories, import on Sources |
| `i` | Open the source import form |
| `h` | Open private saved question/search history |
| `e` | Load the selected note's full body and revision for editing |
| `f` | Filter the loaded records in a domain list |
| `t` | Toggle Notes between active records and trash |
| `x`, `u` | Move a note to trash; in trash, delete permanently or restore with `u` |
| `d` | Toggle advanced record details |
| `Enter` | Open a selected record or submit a form |
| `Up`, `Down` or `j`/`k` | Select in lists/panels or scroll while reading; Home arrows choose an action |
| `Right` | Open a selected record, then move from reading to the workspace panel |
| `Left` | Return from reading to the list, or from the workspace panel to content |
| `Page Up`, `Page Down` | Move through a long reading pane or a loaded record list |
| `g`, `G` | First or last record/line |
| `r` | Reload the current destination |
| `Esc` | Return from detail, close a menu, or cancel editing |
| `/` | Focus semantic search |
| `?` | Keyboard help |
| `q`, `Ctrl-C` | Quit; `q` is ordinary text while a form is being edited |

Type in either palette to narrow the choices, then use up/down and Right/Enter. Left/Esc closes it without changing the selected destination. `Ctrl-K` also includes How history, Feedback, Lifecycle, Skills, Clients, Data, Rights, Diagnostics, Benchmarks, Replay and Observations. Workspaces shows all registered projects, memory counts, the current project and its root. Select a project and press Right/Enter to open it; `f` filters the project list and `r` refreshes counts. The header identifies the selected project, and the wide panel keeps its count and all-project shortcut visible. Late results and the previous workspace's Ask context are discarded. Search, Ask and mutations use the selected project's data.

List filters apply to the loaded records, rather than performing a new server search. Notes distinguish active records from trash; `r` refreshes while preserving the chosen filter and trash view. Use Search for semantic retrieval across workspace memories.

## Forms and editing

Daily forms show essential fields first: question, note title/body, memory type/content, or document path/title/format/rights basis. `Ctrl-O` reveals all options, including identities, saved revisions, note paths, properties and retrieval settings. Existing selected identities and revisions remain part of the request when hidden.

Select an action with `a` or `Ctrl-P`, then use Tab/Shift-Tab or Ctrl-Up/Down to choose a field. Required fields have `*`. A visible caret supports Unicode insertion, left/right movement, Home/End, Backspace and Delete. In multiline fields, up/down moves between logical text lines and retains the desired column across short lines; this does not switch panes or fields. In single-line or choice fields, up/down chooses the adjacent field. Left/right cycles known choices such as memory type, rights basis, file format and booleans. `Ctrl-U` clears the current field. Paste accepts multiline bodies; `Ctrl-J` inserts a newline. `Ctrl-S` reviews a mutation or runs a read action; Enter also submits. Plain `q` remains text while editing.

The note title controls the document's first Markdown heading. Saving replaces an existing first heading or adds it above the body, preserving the remaining text. A new note receives a stable path generated from its title; review and retries reuse that path, and Advanced options lets you change it. For a note body or a structured field, `Ctrl-E` opens `$VISUAL`, `$EDITOR`, or `vi` using a private temporary file. Save and exit the editor to return to the draft. The editor updates the draft only; submit the form and confirm the mutation to write it. The editor variable accepts an executable and simple arguments, for example `EDITOR='code --wait'`. No shell is invoked. The assembled note is limited to 64 KiB.

Mutations show a review screen naming the action and workspace. Press `y` to run, or `n`/Esc to return to the form. Secrets remain hidden. A failed operation keeps the form available for correction. Duplicate submissions are blocked while a request runs. Backend revision/generation/policy checks remain authoritative. Graph and orchestration forms generate a stable idempotency key for retries.

## Workspace workflows

| Destination | Workflows |
| --- | --- |
| Overview / Browse / Search / Memories | Counts, ranked search, metadata and provenance, write, pin/unpin, delete, filtered search and export |
| Ask | Grounded recall context, graph routing and evidence, translation through configured local models |
| Notes | Create, inspect full body, edit with optimistic revision, trash, restore, permanent delete, immutable revisions, backlinks and indexing retry |
| Sources | Import PDF/EPUB/Markdown/text, inspect import jobs and book structure, query passages, review proposed memories, study a project, retry note indexing and remove source notes |
| How history | Solution episodes and steps, solution-path recall, pin/correct/redact/supersede/delete review |
| Activity / Replay / Observations | Sessions, recorded events, JSONL import, observation inspection and promotion |
| Graph | Readiness, index status, nodes/edges/communities, enable/update/rebuild/retry/cancel/disable/rollback operations, trust review and feedback |
| Feedback / Lifecycle | Retrieval scoring/outcomes, scheduler history/status and a forced or admission-checked maintenance run |
| Skills | Installed files, logical skill revisions/evaluations/activation, lifecycle operations and orchestration control |
| Clients / Settings | Client tool profiles with revision checks, local model test/save/key removal, local Jev token setup |
| Workspaces / Data | Register, switch, rename, unregister with optional data retention, JSON export/import, reconstruction and encrypted portable export |
| Diagnostics / Benchmarks | Advisor, metrics/usage, audit and benchmark records |
| Chat and search history | Recent query/result snapshots, reopen, rename, delete, repeat Ask or Search |

`5` opens Ask with the question composer focused. Enter sends the question using Graph Auto by default; `Ctrl-O` exposes retrieval options. Results label grounded context, durable memories, weak context and graph routing. Esc leaves editing to read/scroll the result; Enter asks another question, `v` opens the full durable memories, `s` searches the question, and `h` opens saved history. The latest result and an unsent question draft survive navigation within the selected workspace.

Ask displays context from the established recall-preview API. When no grounded memory matches, it offers Search, source import and another question. It does not invent an answer or promote weak context to source evidence. Markdown and Mermaid are readable terminal text; graph and diagnostic data are inspectable records instead of browser canvases. Advanced skill/seminar operations retain a structured payload field because their contracts include domain documents. Use Ctrl-E to edit those documents; invalid JSON is rejected before dispatch.

Optional services and feature flags retain their existing behavior. A disabled library/observation service, missing local model or seminar runner, unavailable graph index, or rejected skill authorization produces an explicit error; opening the TUI does not enable these capabilities automatically.

## Sources and rights

Before a gated import, open Rights and review the current policy, primary confirmation, and statement IDs/text. Choose its acceptance action, enter the current version and the comma-separated IDs you accept, then explicitly confirm. The TUI never accepts these statements automatically. A stale/expired/missing receipt is rejected by the same backend gate as other clients.

Sources → Import uses a regular local file of at most 64 MiB and an explicit format/rights basis. Leave the optional principal blank to use the backend's established local workspace identity, which preserves access to existing library imports. Organization imports also require an organization identity. A successful import creates a source note containing the edition/asset/work identifiers. Reimporting the same edition reuses its active source note. If ingestion succeeds but source-note registration fails, the error identifies the imported edition and a retry completes registration.

PDF extraction retains its existing external extractor/OCR requirements. The terminal client reports those failures without claiming successful ingestion.

## Local history and migration

The terminal saves up to 50 successful Ask/Search records per workspace. Files are mode `0600` under a private `tui-history` directory beside local workspace databases. API connections use separate local history directories identified by backend URL. Snapshots larger than 128 KiB keep the query metadata and an explicit notice to run the query again. Rename/delete affects only these local records. Queries still follow the backend's normal retrieval telemetry contract.

The former browser's localStorage chat/search drafts are not accessible from a terminal process and are not automatically migrated. Existing SQLite memories, notes, library data, graph records and skill state remain in place.

Data → Export writes a new private file and refuses to replace an existing path. JSON imports accept a plain export bundle or the API envelope written by JSON export. Portable export requires a 12–1024-character passphrase and preserves the established AMPB2 format. Source originals are excluded from portable exports.

## Removed frontend paths

React/Vite sources, embedded HTML/CSS/JS assets, the HTML operator page, the native dashboard launcher, dashboard install/upgrade flags and config, frontend build jobs, and frontend development-container ports/volumes are removed. `agent-memory dashboard`/`ui`, `/dashboard/*`, `/w/*`, and `/ops/dashboard` no longer exist. `serve` remains the headless Go API; its JSON business routes, MCP integration, and model/GraphRAG services remain available.

Historical web specs and reviews describe their original state. They do not certify the terminal release or any externally signed launch gate.
