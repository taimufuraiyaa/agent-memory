# agent-memory
A persistent, multi-tier memory layer for AI coding agents (Cursor, Claude Code, Codex, Cline, custom). It retains knowledge across sessions, learns from outcomes, and reduces repeated research/token consumption through hybrid storage and lifecycle management.

<img width="2544" height="1290" alt="CleanShot 2026-08-24 at 12 25 48@2x" src="https://github.com/user-attachments/assets/d02949b6-ed89-4763-82aa-33d8ad9f6cd3" />

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

The initial terminal UI provides a workspace overview, recent-memory browsing, semantic search, memory details, loading and error states, and an in-app help screen. It is intentionally read-only except for normal retrieval telemetry recorded by semantic search. The existing React dashboard remains the interface for source ingestion, notes, graph exploration, settings, and other local mutations.

| Key | Action |
| --- | --- |
| `Tab` / `Shift-Tab`, `1` / `2` / `3` | Switch between Home, Search, and Browse |
| `/` | Focus search input |
| `Enter` | Run a search or open the selected memory |
| Arrow keys or `j` / `k` | Move through results and detail content |
| `g` / `G` | Jump to the start or end |
| `r` | Refresh the current view |
| `Esc` | Leave search input, detail, or help |
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
