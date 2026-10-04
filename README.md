# agent-memory
A persistent, multi-tier memory layer for AI coding agents (Cursor, Claude Code, Codex, Cline, custom). It retains knowledge across sessions, learns from outcomes, and reduces repeated research/token consumption through hybrid storage and lifecycle management.

<img width="3014" height="1818" alt="CleanShot 2026-05-29 at 01 57 34@2x" src="https://github.com/user-attachments/assets/62a00b34-912a-44bd-a621-fd5f07b79e23" />
<img width="2292" height="1766" alt="CleanShot 2026-05-29 at 01 28 36@2x" src="https://github.com/user-attachments/assets/bf0c19b6-c75d-4ee0-8dd8-c72033e2dedf" />
<img width="2978" height="1794" alt="CleanShot 2026-05-29 at 01 47 06@2x" src="https://github.com/user-attachments/assets/61f29202-54bc-496d-b249-d592f0809d13" />

## Status
- **Core Implementation**: Complete, optimized, and ready to run.
- **Tools**: CLI, local HTTP dashboard, and test automation scripts are available.
- **Integration**: Supports Cursor, Trae, Claude Code, ZCode, and custom agent integrations out-of-the-box.

---

## Architecture & Hybrid Design
Current agents are mostly stateless between sessions. Markdown-only notes and vector-only stores each solve part of the problem, but not the full memory lifecycle. `agent-memory` solves this with a **local-first, hybrid storage system** (databases stored locally under `~/.agent-memory/`):

| Tier | What It Holds | Why |
|---|---|---|
| **Markdown** | Pinned conventions, project rules, `AGENTS.md`-style facts | Zero retrieval cost, always loaded |
| **Vector** | Semantic recall over discovered facts (SQLite-backed) | Fast similarity search without mandatory cloud dependencies |
| **Graph** | Service/topic/file relationships | Captures structural links vectors miss |
| **Document** | Raw episodic transcripts, larger logs, and reports | Cold archive referenced by other tiers |
| **Tombstones** | Markers of forgotten memories | Graceful recovery of previously evicted context |

---

## Core Features

### 1. Multi-Signal Explainable Recall
Retrieval starts with a semantic candidate set, then re-ranks using:
- **Semantic Similarity**: Vector-based relevance.
- **Recency**: Weighting newer updates higher.
- **Outcome Signal**: Boosts successful approaches and flags failures.
- **Decay Penalty**: Graceful fading of stale/unaccessed items.
- **Tier Bias**: Prefers specific tiers (e.g. Markdown) for stability.
Explain mode provides a granular score breakdown for inspection.

### 2. Graph-Expand Retrieval Mode
Captures structural relationships between files, systems, and topics using a configurable Breadth-First Search (BFS) traversal. It performs depth-controlled expansion to capture related components that a purely semantic vector search would miss.

### 3. Smart Token-Budgeted Assembly
Ranked memories are balanced by task intent, checked against a strict token budget, and emitted as a stable, sectioned `context_block` that can be fed directly into your agent's system prompt. Over-budget memories are reported as "clipped" with clear reasons.

### 4. Cache Invalidation & Bulk Retrieval
Optimized with workspace-scoped cache invalidation, lightweight inference lists, and high-performance bulk memory retrieval to handle active developer workspaces without latency.

### 5. Exact Keyword and Hashtag Search
Each memory can carry up to three deliberate locators—names, terms, hashtags, or other helpful keywords a person is likely to search later. Exact term mode is project-scoped and supports deterministic `AND` and `OR` matching without starting an embedding provider.

A versioned per-project Bloom filter can reject definite misses before SQLite lookup. A Bloom `maybe` is never treated as a match: possible hits always continue to canonical exact-term search. This is additive to semantic search and does not replace it.

---

## Installation & Setup

### Prerequisites
- Go toolchain matching [go.mod](go.mod) (currently `go 1.26.3`)

### Install Options

#### Option A: Via Homebrew (Source Tap)
```bash
brew tap taimufuraiyaa/agent-memory https://github.com/taimufuraiyaa/agent-memory.git
brew install --HEAD taimufuraiyaa/agent-memory/agent-memory
```

#### Option B: CLI Binary Installation
```bash
go install ./cmd/agent-memory ./cmd/am
am install
```

On an interactive terminal, `agent-memory install` opens a component checklist:
use Up/Down to move, Space to select or unselect, and Enter to review and
install. The full-screen interface provides a highlighted active row, aligned
status and size columns, live selection counts, and a persistent shortcut bar.
The recommended profile includes the local `qwen3:8b` question
planner. Selecting it opens a second terminal step with `None`, `qwen3:4b`
(2.5 GB), recommended `qwen3:8b` (5.2 GB), and `qwen3:14b` (9.3 GB) choices
before making any changes. The required core is locked; ONNX Runtime, MiniLM,
the dashboard, local planner, and current-workspace rules remain independently
selectable.

For scripts and redirected execution, the installer never waits for terminal
input. Existing flags remain supported, with these explicit controls:

```bash
# Preserve the legacy headless install without the optional Qwen planner.
agent-memory install --no-tui

# Headless local planner installation and exact-model verification.
agent-memory install --no-tui --with-local-llm

# Select another supported model, or use "none" for parser-only operation.
agent-memory install --no-tui --local-llm-model qwen3:4b
```

#### Option C: Installer Script (Auto-downloads local embedding model)
```bash
# Unix/Linux/macOS:
go run install.go install_unix.go

# Windows:
go run install.go install_windows.go
```

The installer configures every supported AI agent by default. For Codex it preserves existing settings while adding the agent-memory data directory as a narrow writable sandbox root and installing lifecycle hooks, so users do not edit Codex configuration manually. Codex may still request its native one-time hook trust confirmation.
It also writes the conservative exact-term rollout setting (`AGENT_MEMORY_TERM_BLOOM_MODE=shadow`) when no choice already exists. Existing `gate` or `off` values are preserved.
Ensure your Go bin directory (`$(go env GOPATH)/bin` or `~/go/bin`) is in your system `PATH`. Verify with `agent-memory --help`.

---

## Workspace Integration

Initialize any workspace/project root to register the database and configure IDE rules:

```bash
agent-memory init --project-name my-project
```

### Common Flags & Operations:
- `agent-memory init --study` - Register and bootstrap learning from local docs/code immediately.
- `agent-memory init --ide trae` - Configure Trae-specific rules explicitly.
- `agent-memory init --ide zcode` - Configure ZCode rules (`AGENTS.md`) explicitly.
- `agent-memory reinstall` - Refresh or repair IDE configuration rules in an existing project.

**What `init` does**:
- Registers the project inside `~/.agent-memory/workspaces.json`.
- Creates a per-workspace SQLite database under `~/.agent-memory/<workspace>.db`.
- Applies database migrations, backfills locators, and publishes a ready project Bloom snapshot after optional study ingestion.
- Automatically writes rule files (e.g., Cursor rules at `.cursor/rules/agent-memory.mdc`, Trae rules, etc.) to prompt the agent to use `agent-memory`.

`am` is the concise executable name for `agent-memory`; installers publish both
names and keep them synchronized. `agent-memory reinstall` and `agent-memory
init --reuse` run the same idempotent term-index preparation for existing
projects.

Run `am upgrade` inside a registered project (or any of its subdirectories) to
upgrade that project. Run `am upgrade --all` to upgrade every registered
project. Neither command requires `-y`; the legacy confirmation flag remains
accepted for compatibility. One damaged project does not prevent healthy
projects from upgrading in `--all` mode. `upgrade --dry-run` and
`upgrade --hooks-only` do not touch project databases.

`am upgrade` updates the local installation and workspace hooks. The dry-run
and hooks-only modes remain side-effect-limited and do not rebuild the dashboard.

---

## Command Catalog

| Command | Purpose |
|---|---|
| `agent-memory init` (`i`) | Register current project, set up DB and IDE rules |
| `agent-memory rename --to <name>`| Rename a registered workspace |
| `agent-memory list` | List all registered workspaces and memory counts |
| `agent-memory delete --project-name <name>` | Deregister workspace (`--keep-data` preserves DB) |
| `agent-memory write` | Store a semantic, procedural, episodic, or outcome memory |
| `agent-memory search` | Query memories using multi-signal retrieval |
| `agent-memory reindex-terms` | Backfill exact locators, rebuild the project Bloom filter, or inspect its status |
| `agent-memory recall` | Retrieve stable, token-budgeted prompt context for a task |
| `agent-memory advisor` | Score workspace memory health and show evidence-backed recommendations |
| `agent-memory session-end` | Parse a session transcript to extract clean learnings |
| `agent-memory study` | Bootstrap/learn from project documents and code files |
| `agent-memory dashboard` (`ui`) | Start and open the local web-based dashboard |
| `agent-memory tui --workspace <name>` | Open the local keyboard-driven terminal workspace |

---

## Day-to-Day CLI Examples

```bash
# Write a fact with up to three explicit search locators
agent-memory write --workspace my-project --type semantic \
  --content "Authentication service handles JWT validation" \
  --keyword authentication --keyword jwt

# Store an outcome memory (with approach and reasoning details)
agent-memory write --workspace my-project --type outcome \
  --content "Upgraded Node.js client package" \
  --outcome-result success \
  --outcome-approach "Updated package.json and ran npm install"

# Search memories
agent-memory search --workspace my-project --query "auth validation" --top-k 5

# Exact project term search: AND requires every term; OR requires at least one
agent-memory search --workspace my-project --mode terms --query "authentication jwt" --operator and
agent-memory search --workspace my-project --mode terms --query "authentication oauth" --operator or

# Backfill/rebuild and safely inspect Bloom health (no raw terms or bitmap)
agent-memory reindex-terms --target-fpp 0.01
agent-memory reindex-terms --status

# Recall context for a task (within a 4000-token budget)
agent-memory recall --workspace my-project --task "debug JWT token validation failure" --budget 4000 --format raw

# Review workspace memory quality, context efficiency, hygiene, coverage, and trust
agent-memory advisor
agent-memory advisor --format json

# Extract learnings at the end of a session
cat session_transcript.txt | agent-memory session-end --workspace my-project --format json
```

## Terminal UI

Open one explicitly selected local workspace without starting the HTTP server or browser dashboard:

```bash
agent-memory tui --workspace my-project
```

The terminal UI provides a workspace overview, recent-memory browsing, semantic search, memory details, loading and error states, an in-app help screen, and local Jev access-token setup. Memory browsing remains read-only except for normal retrieval telemetry recorded by semantic search. The existing React dashboard remains the interface for source ingestion, notes, graph exploration, settings, and other local mutations.

For Claude Code, `agent-memory connect claude-code --workspace <name> --root <project>` installs a project `/am` skill alongside the existing MCP server and lifecycle hooks. In Claude Code, run `/am listen` for bounded local Agent Memory recall, or `/am listen all` to opt into both local recall and Jev advice. `/am listen status` reports both; `/am listen off` stops both. The CLI equivalents are `agent-memory listen on [--jev]|status|off --workspace <name>`. Keep the local Agent Memory service running. The listener is off by default and only emits context when the hook's working directory is inside the registered project. Plain listen never initiates Jev consent, and actual model switching requires host integration.

On the Jev screen, press `s` to set or replace the token and `x` then `y` to remove it. Entry is hidden. The token is stored only for the local user in `~/.agent-memory/credentials/jev-access-token` (private directory and file); it is not a workspace setting. “Configured” reports presence, not successful authentication with Jev. The TypeSafe decision adapter validates access only when explicitly enabled; full harness model/tool execution is separate work. For a confident `/am listen all` choice, the synchronous Claude `UserPromptSubmit` hook also shows a `[Jev] Recommended ... Model not switched.` message; without a model catalog it names a reasoning tier, not a model. No status is shown for failed or low-confidence choices.

In a connected Claude Code project, `/am listen all` explicitly opts into sending a bounded, redacted prompt and local project skill names/descriptions to TypeSafe for advisory task-complexity and skill choices. It requires a configured token and verified Jev access; failed verification leaves the listener state unchanged. `/am listen off` stops both local recall and Jev egress; `/am listen status` shows separate `jev_enabled` and live `jev_decisions_ready` states. `/am jev on|off` and CLI `listen jev-on|jev-off` remain compatibility controls for Jev alone. Jev results do not switch Claude's main-session model, load skills automatically, or authorize tool actions. See the [TypeSafe System One API contract](https://api.typesafe.ai/openapi.json).

To let Jev choose a specific model, create `~/.agent-memory/model-catalog.json` as a private (`0600`) user-owned file. Its version-1 `hosts` object may contain `claude` and `chatgpt`, each with two to sixteen `id`/`description` candidates. For example:

```json
{"version":1,"hosts":{"claude":[{"id":"claude-sonnet-5-5","description":"Balanced coding work"},{"id":"claude-opus-5-5","description":"Complex, long-running work"}],"chatgpt":[{"id":"gpt-6-luna","description":"Fast, scoped app tasks"},{"id":"gpt-6.1-sol","description":"Complex app tasks"}],"openai_api":[{"id":"gpt-6-luna","description":"Fast API tasks"},{"id":"gpt-6.1-sol","description":"Complex API tasks"}]}}
```

These are example identifiers from the [Claude model catalog](https://platform.claude.com/docs/en/models/overview) and [official OpenAI documentation](https://developers.openai.com/api/docs/models/all), **not** an entitlement check. Configure only models available to your account and host. With `/am listen all`, the Claude prompt hook asks Jev to choose from the Claude entries instead of a generic complexity tier and presents the exact choice as advice. Separately, when Claude invokes an `Agent` subtask, the managed `PreToolUse` hook asks Jev about that bounded, redacted subtask and replaces only the Agent call's `model` field with a confident Claude-catalog choice. It does not change the main session or bypass Claude Code's tool permissions. Missing catalog, Jev failure, or low confidence leaves the original Agent call unchanged. The hook reports the requested subagent model; Claude Code may substitute another allowed model, and its Agent tool result's `resolvedModel` is the actual host observation. This adds a Jev request to the subagent-start critical path; latency and cost improvements are unproven until measured. A local ChatGPT/OpenAI-side dispatcher can request a one-shot recommendation by piping the task into `agent-memory model-choice --workspace <name> --host chatgpt --allow-jev` from the registered project. The command returns a validated candidate ID and `model_switched:false`; it does not change an existing ChatGPT conversation or call the OpenAI API. Without a catalog, Claude retains the complexity-tier advisory and the one-shot command returns no choice. The task and candidate descriptions leave the machine only after the explicit Jev opt-in or one-shot flag.

The `chatgpt` catalog and project [jev-model-router skill](.agents/skills/jev-model-router/SKILL.md) support the ChatGPT app as a recommendation; apply the choice in its model picker if available. The separate `openai_api` catalog supports a bounded, text-only API execution path: set `OPENAI_API_KEY`, then pipe one task into `agent-memory model-run --workspace <name> --allow-jev --allow-openai-api` from the registered project. The command probes access to Jev's selected model and creates one [OpenAI Responses request](https://developers.openai.com/api/reference/cli/resources/responses/methods/create) with `store:false`, no tools, a 512-token default output cap, and no retry. Both the task and model candidates go to TypeSafe; the redacted task also goes to OpenAI. This may incur API charges. It does not change any existing ChatGPT chat, run a coding-agent tool loop, or infer API access from a ChatGPT subscription.

Interactive `agent-memory install` also offers an optional masked Jev token prompt after setup. Interactive `agent-memory upgrade` offers it after a successful upgrade only when no token is configured. Answer `y` to approve or press Enter to skip. Existing tokens are never replaced by upgrade; non-interactive, JSON, dry-run, and hooks-only runs do not prompt. The TUI remains the place to replace or remove a token later.

| Key | Action |
| --- | --- |
| `Tab` / `Shift-Tab`, `1` / `2` / `3` / `4` | Switch between Home, Search, Browse, and Jev |
| `/` | Focus search input |
| `Enter` | Run a search or open the selected memory |
| Arrow keys or `j` / `k` | Move through results and detail content |
| `g` / `G` | Jump to the start or end |
| `r` | Refresh the current view |
| `Esc` | Leave search input, detail, or help |
| `s` / `x` on Jev | Set/replace or request removal of the token |
| `?` | Toggle help |
| `q` or `Ctrl-C` | Quit |

The command requires an interactive terminal and does not support `--api` mode.

### Exact-term Bloom rollout

`AGENT_MEMORY_TERM_BLOOM_MODE` controls only exact term mode:

- `shadow` (default): probe Bloom but always execute canonical SQLite lookup.
- `gate`: skip canonical lookup only for a healthy, checksum-valid, current definite miss. `AND` can stop when any token is absent; `OR` stops only when every token is absent.
- `off`: immediate kill switch; bypass Bloom and fail open to canonical lookup.

Missing, dirty, rebuilding, corrupt, incompatible, generation-mismatched, saturated, high-FPP, or delete-heavy state always fails open. Run `agent-memory reindex-terms --status` for the stable bypass/rebuild reason, and run `agent-memory reindex-terms` to rebuild. Semantic search behavior is unchanged.

---

## Local Development

Build and test the local binary, embedded dashboard, and MCP server directly from the checkout:

~~~bash
make build
make test
make integration-test
make build-with-dashboard
~~~

The Go binary serves the local API and embedded dashboard. The MCP server uses the same local API and exposes only local tools; set `AGENT_MEMORY_URL` when the API listens on a non-default address.
## Local HTTP Dashboard
The dashboard is served locally by the Go binary (no separate servers required) and matches the core API engine path exactly:

```bash
# Start and open in browser
agent-memory dashboard

# Run headlessly in the background
agent-memory dashboard --start
agent-memory dashboard --stop
```

During UI development, opt into the Vite development server and hot module
replacement while the Go binary serves the real local API:

```bash
agent-memory dashboard --hot-reload

# The development server can also run in the background.
agent-memory dashboard --hot-reload --start
```

Hot-reload mode requires npm and a source checkout containing
`tools/agent-memory/dashboard`. It is opt-in; normal dashboard startup still
uses the embedded UI and has no npm runtime dependency.

For a source checkout, build the embedded dashboard and binary once with
`make build-with-dashboard`, then run `./bin/agent-memory dashboard`. The
dashboard listens on port 3100 by default, and the production binary serves
`/dashboard/` itself; npm is not needed at runtime.

The local command uses one embedded React webapp. The server publishes a small,
no-store runtime manifest for the standalone SQLite workflow.

### Notebook and dashboard capabilities

- **Notebook-first workspace**: Notes is the default home, with a workspace explorer, multi-note tabs, Markdown editing, rendered preview, Mermaid diagrams, and responsive desktop/mobile layouts.
- **Human + agent recall**: Saved note revisions are indexed asynchronously as replaceable memory chunks. Search and Ask label Human note and Agent memory evidence separately and retain note path, revision, heading, and line provenance.
- **Safe document lifecycle**: Autosave uses optimistic revisions, revision history is immutable, normal deletion moves notes to recoverable trash, and failed indexing never blocks reading or editing.
- **Connected notes**: `[[Internal links]]`, backlinks, outline navigation, typed properties, folder paths, and revision restore are available from the context panel.
- **Grounded Ask**: Ask can search the active note, current workspace, or all local workspaces; citations reopen human notes, and answers can become notes only through an explicit confirmed action.
- **System workspace**: Existing Overview, Sessions, Diagnostics, Benchmark, Lifecycle, Wiki, Feedback, Skills, raw stats, Explain Mode, Recall Preview, and Memory Advisor remain reachable under System.
- **Per-client MCP profiles**: System → Clients registers Codex, Claude, Cursor, or custom clients and assigns Default (five workflow tools) or Expanded (adds health and session browsing). Add the displayed `AGENT_MEMORY_CLIENT_ID` value to that client's MCP environment and reconnect it. Profiles apply across all local workspaces, but each client selects its own profile.
- **Local installation settings**: System → Diagnostics exposes local runtime health and resource signals. Saving changes local configuration only.
- **Encrypted local backup**: System → Migration downloads an encrypted AMPB2 copy of the selected workspace's memories and active notes. It excludes source originals and never deletes local data.
- **Keyboard workflow**: Command palette, new note, global search, Ask, save, close tab, and next/previous tab shortcuts are supported with visible focus states.

The notebook is enabled by default. For the one-release rollback window, build
the dashboard with `VITE_NOTEBOOK_ENABLED=false` to restore Overview as the
default and hide Notes without changing stored notes or memories:

```bash
VITE_NOTEBOOK_ENABLED=false make build-with-dashboard
```

Rebuild without that environment variable to re-enable the notebook. The
rollout is additive: existing memory rows require no destructive migration.

---

## Benchmark & Test Automation
The repository includes automated test suites and metric runners to measure recall efficiency, operational token costs, and search quality under the `benchmark/` directory:

```bash
# Execute the benchmark suite
./benchmark/run_benchmark.sh

# Run scoring and analyze token metrics
python3 benchmark/score.py --run-dir benchmark/results/continuation-full-10000 --db benchmark/results/continuation-full-10000/benchmark.db --ingest
```

---

## Privacy & Local-First Security
- **Local Storage Only**: All data is saved inside `~/.agent-memory/` on your system.
- **No Telemetry**: Absolutely no data collection or tracking.
- **Secret & PII Redaction**: Automatic scanning and filtering of API keys (`sk-`, `ghp_`, etc.), private keys, credit cards, and emails on ingest.
- **Transparent Local Host**: The local dashboard binds to `127.0.0.1` by default.
- Refer to [docs/security.md](docs/security.md) and [SECURITY.md](SECURITY.md) for more details.
