# Primary Terminal Interface

## Objective and authorization

The user's 2026-10-11 instruction replaces the React/HTML human interface completely with a Go terminal interface and removes frontend packaging. This supersedes the additive, read-only scope of `bubbletea-terminal-ui`. Implementation is authorized by that instruction.

## Scope and assumptions

- Bubble Tea v2 and Lip Gloss v2 already in go.mod remain the rendering stack.
- Local SQLite data, Go application services, REST JSON APIs, MCP, model servers, and GraphRAG remain supported. Node used by the MCP connector is independent of the removed frontend.
- The React/Vite workspace, embedded static assets, HTML operator view, native dashboard launcher, browser launch command, frontend installer, build jobs, and development container frontend dependencies are removed.
- Historical specs remain evidence of their original implementation and receive a supersession notice where relevant. Current docs describe the TUI.
- No database migration, deletion of user databases, or automatic migration of browser localStorage is performed. Existing browser-only chat/search drafts are not accessible to a terminal process.

## Required workflows

1. `agent-memory tui --workspace <registered-name>` requires an explicit workspace, interactive input/output, and clean terminal restoration. Local mode runs without a listening HTTP server; `--api` connects to the existing headless service.
2. Overview, semantic search, recent and pinned memories, detail, write, pin/unpin, delete, and retrieval feedback are keyboard accessible.
3. Ask uses the established grounded recall-preview contract, including graph routing; translation uses configured local LLM services.
4. Notes support list, full detail, create, edit with expected revision, trash, restore, permanent deletion, revision/backlink inspection, and index retry.
5. Sources support project/source inspection, PDF/EPUB/Markdown/text file import with explicit rights basis, import-job inspection, library structure/query/review, study, and source index retry/removal through their existing contracts. Current rights statements must be displayed and explicitly accepted before gated import.
6. Private local chat/search history supports persisted query/result snapshots, reopen, rename, delete, and repeat; previous browser localStorage is not migrated. Solution history, review, sessions, observations, replay, benchmark reports, audit, feedback, scheduler status/history/run, skill lifecycle/orchestration, client profiles, graph readiness/status/explorer/review/operations, model settings, workspace management, and portable data export/import have terminal destinations or named actions.
7. Common workflows use labeled forms and record selection. Advanced domain payloads may use a JSON field where their existing contract requires a structured document; they must validate before dispatch. Blank required fields, invalid JSON/numbers/booleans, and unsupported actions never silently succeed.
8. All workspace operations bind the selected workspace. User-entered payloads cannot override it. Destructive operations require a review/confirm step naming the action and workspace. Revision/generation checks remain enforced by the backend.
9. All I/O is asynchronous, cancellable, bounded, and generation tagged. Stale loads cannot replace the current view or form. Busy mutations cannot be submitted twice. Loading, empty, error, and retry states are visible.
10. Terminal output sanitizes all stored/backend/user text against control-sequence injection, remains within terminal bounds, and handles Unicode, paste, resizing, scrolling, and monochrome use.
11. Building, installing, serving APIs, and opening the terminal UI requires no React, Vite, npm frontend build, HTML assets, frontend container, or browser.
12. `agent-memory start` and `am start` open the terminal workspace. With `--workspace`, they use that explicit workspace. Without it, they require an interactive choice from registered local projects before opening the selected project's runtime; they never infer a workspace from cwd or environment. The picker may read bounded memory counts from existing registered databases without migration, creation or content loading. API mode requires `--workspace`. The existing `tui` command retains its explicit workspace requirement.
13. `restart` rebuilds from the Agent Memory source checkout, updates the canonical and concise executables together, then opens the rebuilt terminal program with the chosen workspace and the original database/model/API flags. `--source-dir` overrides existing source discovery. Missing Go/source, failed compilation, cancellation or publication failure must not launch a stale build or report success. Failure updating the concise executable restores the canonical executable.
14. Both launchers reject nonterminal use before selection/build/installation and support clean cancellation of workspace selection. They open a foreground terminal session; callers close their current TUI before invoking restart. They do not terminate other terminal sessions or change the headless daemon.

## Verification commands

- `go test ./internal/tui ./internal/cli ./internal/api ./internal/bootstrap ./internal/config`
- `go vet ./internal/tui ./internal/cli ./internal/api`
- `go test -p 4 ./...`
- `go build -trimpath -o <temporary-path>/agent-memory ./cmd/agent-memory`
- `git diff --check` and tracked source/build reference audit.
- Real pseudo-terminal smoke against an isolated database: overview, destination navigation, mutation form cancellation, quit and successful process exit.

## Boundaries

Always preserve storage/authentication/rights/revision contracts and unrelated work. The requested frontend removal and Go replacement are authorized. Never erase user data, accept rights on the user's behalf, disclose credentials, or report unverified parity/verification as complete.
