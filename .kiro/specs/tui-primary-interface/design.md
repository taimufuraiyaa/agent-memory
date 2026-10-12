# Primary Terminal Interface Design

## Architecture

The existing terminal model retains its tested overview/search/browse/credential paths and adds a workspace console. A destination selector gives access to domain lists; a named action menu opens labeled forms, autofilled from the selected record when appropriate. Record detail and results scroll independently of list navigation. Form submission reviews mutations before dispatch. Rights acceptance is a dedicated action after the current statements are loaded.

The console backend transports structured requests to the existing Go API contracts. Local mode uses an in-process HTTP handler transport with the same fixed workspace and exact DB path, so it opens no network listener and invokes the same application services as the headless API. Remote mode uses an HTTP client against an explicitly configured API URL, retaining the configured backend's authentication and request boundary. The adapter unwraps the established v1 envelope, checks HTTP and domain errors, limits response size, and binds workspace in both query and JSON/multipart bodies.

## Alternatives and trade-offs

Reimplementing each domain directly against SQLite would duplicate admission, revision, rights, graph, and policy logic and create long-term divergence. Shelling out to CLI subprocesses would limit features, repeat model startup, and complicate cancellation. Starting an ephemeral local listener adds lifecycle and exposure without benefit. The handler transport keeps one domain authority and enables real API integration tests without an external server. A narrow typed request contract is justified by the broad pre-existing API; arbitrary URLs/methods remain inaccessible from the TUI.

Retaining React as a fallback conflicts with the user's complete replacement request. Both React and the legacy HTML operator page are removed after terminal flows pass their gates. The headless JSON summary APIs remain useful for API clients; a name containing dashboard does not imply a served frontend.

## Data contracts and selection

Destinations define list requests, array locations, identity/title/status fields, detail requests, and named operations. Forms define field kinds, required/default values, structured payload validation, selection autofill, and mutation classification. Note updates carry expected_revision. Client updates carry expected_revision. Graph operations carry configuration identity, expected revision and job identity when required. Skill changes preserve actor, environment, generation and policy contracts. Export uses a private, create-only local file and never prints a passphrase. Upload reads a regular file with a bounded size and explicitly chosen source format/rights basis.

Opaque result documents are normalized into human-readable key/value and record views, including nested fields. Graphs use node/edge/community records and provenance rather than an interactive browser canvas. Mermaid source is readable terminal text. Ask renders grounded context and evidence returned by the API without inventing a generative answer.

## Failure modes and edge cases

Each command runs outside Update. Generations attach to destination loads; changing destinations invalidates old loads. Operations track a separate generation and busy state. Esc cancels editing without writing; Ctrl-C cancels the program. Shutdown cancels outstanding operations before closing stores. Remote timeout, malformed envelopes, rights requirements, conflicts, missing optional services, empty arrays, and rejected operations become actionable messages. A backend error retains the form for correction and never emits a success notice.

User and backend strings pass through the existing terminal sanitizer. Inputs are bounded; UTF-8 backspace removes one rune; pasted multiline text belongs only in multiline form fields. Secrets remain hidden in forms/results and are excluded from error bodies. Navigation and confirmation keys are interpreted only outside editing. Screen size bounds apply after styling. Lists load bounded pages/limits and details render only visible rows.

## Local chat/search history

A backend wrapper records successful Ask/Search results into private local JSON files, bounded to 50 records per workspace and 128 KiB per result snapshot. Remote backend URLs have separate history namespaces. Rename/delete operates only on terminal history. Atomic private-file replacement preserves the previous file on failure; unreadable files are reported without destructive repair. Workspace switching reuses the appropriate history store and rejects old asynchronous generations. The local editor edits a private temporary draft and does not write domain content before form confirmation.

## Performance and scaling

Core search retains the engine's ranked bounded retrieval and cache. Console lists request bounded limits/cursors where supported; the transport caps response bytes. One active load per view and one active mutation avoid unbounded requests. Local services cache per-workspace assets and close deterministically. Large documents are clipped to the viewport while remaining scrollable. Bulk operations use existing API batching; no new schema/index is required.

## Rollout and removal

1. Record the full feature inventory and test console transport/binding/error behavior.
2. Add terminal destinations, forms, selection/detail, and guarded mutation lifecycle.
3. Wire local and remote adapters and verify core journeys against real handlers.
4. Remove React/static/operator/launcher/build/install/container paths, preserving shared daemon utilities in a service-specific file.
5. Update current docs/config and run focused, full regression, vet/build and PTY checks.

Rollback uses version control to restore the prior UI; it requires no data rollback. Existing web processes and browser drafts are not destroyed or manipulated. Historical frontend specs are retained as superseded evidence. Release notes identify the terminal command and loss of browser-only drafts.

## Risks

The main risk is losing a workflow hidden in a secondary panel. The action inventory is derived from the gateway/API calls and tests require each domain to expose its operations. Reusing handlers reduces domain drift. Native dashboard lifecycle controls disappear with the launcher, while `serve --start/--stop/--status` remain authoritative. Model/runtime setup remains in the existing Go installer TUI. No externally signed launch-readiness evidence is asserted by this interface migration.

## Installed terminal launch and rebuild

The 2026-10-11 follow-up authorizes rebuilding/installing both PATH executables and adds start/restart entry points. Start reuses the existing TUI command/runtime instead of maintaining another application lifecycle. An omitted workspace opens a small Bubble Tea selector with immediate registry names and asynchronous read-only memory counts, without initializing project runtimes or assuming the cwd/environment. Selection sets the workspace flag explicitly before invoking the domain runtime. API launch requires an explicit workspace because the local registry is not authoritative for a remote service. Empty registry and cancelled selection produce clear outcomes. The later multi-project discovery design in tui-workspace-ux specifies count failures, bounded reads and registered path authority.

Restart selects/validates the workspace and verifies terminal input/output before any rebuild. It resolves an explicit source directory or the existing upgrade source discovery, validates the module and entry point, builds into a private temporary directory with context cancellation and a two-minute limit, and stages executable publication using the existing atomic replacement helper. The canonical executable is backed up before replacement; a concise-executable failure restores it. A successful rebuild starts the freshly installed canonical executable with the same terminal streams and explicit workspace/db/model/API arguments. Child launch uses an argument vector, with no shell and no recursive restart invocation.

Separate processes are preferred over running the old in-memory model after rebuilding, which would open stale code. A background daemon/PID manager would add unrelated lifecycle complexity: the TUI is foreground and the existing serve daemon remains independent. Two filenames cannot be replaced as one filesystem transaction, so publication is atomic per file with rollback of the canonical file if the second publication fails. A rollback failure is reported explicitly. Both names resolve to byte-identical builds after successful publication. Concurrent manual rebuilds in the same install directory should be avoided; existing running processes retain their loaded executable.

Startup cost is unchanged for start. Restart adds one bounded Go build; it neither installs models nor rewrites workspace rules/environment. Missing Go, wrong module, read-only install directory, interrupted build, invalid workspace and nonterminal streams are failure paths. Verification covers those boundary outcomes with injected process/build edges, actual filesystem rollback, selector cancellation, and real PTY startup through installed commands. Rollout replaces the existing discovered PATH copies only after package checks and build pass, then checks both names and real terminal behavior.
