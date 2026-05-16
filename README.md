# agent-memory
A persistent, multi-tier memory layer for AI coding agents (Cursor, Claude Code, Codex, Cline, custom). It retains knowledge across sessions, learns from outcomes, and reduces repeated research/token consumption through hybrid storage and lifecycle management.

## Status
- Core implementation is actively in progress and runnable in this repository.
- CLI + local HTTP dashboard are available.

## Why This Exists
Current agents are mostly stateless between sessions. Markdown-only notes and vector-only stores each solve part of the problem, but not the full memory lifecycle.

### Hybrid Design
| Tier | What It Holds | Why |
|---|---|---|
| Markdown | Pinned conventions, project rules, AGENTS.md-style facts | Always loaded, zero retrieval cost |
| Vector | Semantic recall over discovered facts (`SQLite + sqlite-vec`) | Fast similarity search without mandatory cloud |
| Graph | Service/topic/file relationships | Captures structural links vectors miss |
| Document | Raw episodic transcripts, larger analyses | Cold archive referenced by other tiers |
| Tombstones + Reconstruction | Markers of forgotten memories + recovery strategies | "Tip of the tongue" graceful re-investigation |

All local-first by default: SQLite per project plus local ONNX embeddings.

## Quickstart (Current Reality)
```bash
cd /Users/time/timebooks/agent-memory
```

Build/test baseline:

```bash
go test ./...
go build ./...
```

## Install (Current Reality)

### Prerequisites
- Go `>= 1.26.3` (see [go.mod](file:///Users/time/timebooks/agent-memory/go.mod))

### Install via Homebrew (tap)

This repo includes a Homebrew formula at `Formula/agent-memory.rb` for `--HEAD` installs (build from source).

```bash
brew tap taimufuraiyaa/agent-memory https://github.com/taimufuraiyaa/agent-memory.git
brew install --HEAD taimufuraiyaa/agent-memory/agent-memory
```

Notes:
- Do not wrap the URL in backticks; backticks execute a command in your shell.
- If you fork this repo, replace `taimufuraiyaa` with your GitHub username/org.

### Install the CLI binary

From this repo:

```bash
cd /Users/time/timebooks/agent-memory
go install ./cmd/agent-memory
```

Or use the installer (also downloads the local embedding model):

```bash
cd /Users/time/timebooks/agent-memory
go run install.go
```

This installs `agent-memory` into your Go bin directory (usually `$(go env GOPATH)/bin`). Ensure that directory is on your `PATH`.

Verify:

```bash
agent-memory --help
```

### Enable `agent-memory` for any project

Inside each project’s root directory (the place you run your agent from):

```bash
agent-memory init
```

If you want a one-shot install + initialize-the-current-folder flow, run:

```bash
go run install.go --init-here
```

Common options:

```bash
agent-memory init --project-name my-project
agent-memory init --study
agent-memory init --reuse
agent-memory init --force
```

What `init` does:
- Registers the project under `~/.agent-memory/workspaces.json`
- Creates/uses a per-workspace SQLite DB under `~/.agent-memory/<workspace>.db`
- Writes a Cursor rule file (default: `.cursor/rules/agent-memory.mdc`) unless `--no-rule`

### Day-to-day usage (inside a project)

```bash
agent-memory write --type semantic --content "orders service publishes order.created"
agent-memory search --query "order event" --top-k 5
agent-memory recall --task "debug order event regression" --budget 400 --format raw
agent-memory session-end --transcript "we found the root cause..." --format json
```

### Engineer dashboard (optional)

```bash
agent-memory serve --addr :3210
```

Then open:
- `http://localhost:3210/dashboard/`

Or use the convenience command (starts the server and opens the browser):

```bash
agent-memory dashboard
```

Background start/stop:

```bash
agent-memory dashboard --start
agent-memory dashboard --stop
```

What you can do in the dashboard:
- Pick a workspace from the dropdown (this comes from `agent-memory init` / `agent-memory list`).
- Run natural-language search with optional filters (type, tier, outcome, confidence, decay, entities, date range).
- Toggle explain mode to see the score breakdown fields (`semantic_similarity`, `recency`, `outcome_boost`, `decay_weight`, `tier_bias`) plus `match_reason`.
- Use Recall Preview to see the exact `context_block` the agent would load for a task, plus which memories were clipped by the token budget (`memories_clipped`) and why.
- Use “Open in CLI” to copy a ready-to-paste `agent-memory search ...` / `agent-memory recall ...` command that matches the current UI settings.

Notes:
- The dashboard is local-only and served by the same Go binary; there is no separate Node/React dev server required.
- The dashboard uses the same in-process retrieval engine path as the CLI (parity-tested).

### Managing multiple projects

```bash
agent-memory list --format text
agent-memory rename --to new-project-name
agent-memory delete --project-name old-project-name --keep-data --yes
```

## Planned Command Catalog (V1)
| Command | Purpose | Run From |
|---|---|---|
| `agent-memory init` (`i`) | Wire current project (create DB + Cursor rule) | Inside project |
| `agent-memory rename --to <new>` | Rename project (move DB, update rule) | Anywhere |
| `agent-memory list` | List registered projects | Anywhere |
| `agent-memory delete --project-name <name>` | Remove project (`--keep-data` archives DB) | Anywhere |
| `agent-memory write` | Store semantic/procedural/episodic/outcome memory | Agent/Human |
| `agent-memory search` | Multi-signal retrieval | Agent |
| `agent-memory recall` | Session-start context within token budget | Agent |
| `agent-memory reconstruct` | Forgotten-memory recovery | Agent |
| `agent-memory session-end` | Extract learnings from session transcript | Agent |
| `agent-memory study` | Bootstrap from README/docs/code | One-off + incremental |
| `agent-memory help agent-prompt` | Print recommended agent prompt snippet | One-off |
| `agent-memory serve` | Optional local HTTP API + dashboard | Engineer inspection |

Planned CLI contract:
- Deterministic JSON envelope on stdout with `--format json`.
- Progress/logging on stderr.
- Stable exit codes (`0/1/2/3/4/5/124`).

## Engineer Dashboard (Search + Recall Preview)
Run `agent-memory serve` to expose `http://localhost:3210/dashboard/` for human inspection.

| Engineer Can... | How |
|---|---|
| Search across markdown/vector/vector+graph/document | `POST /api/v1/memories/search` (`filters.tiers`, optional `explain: true`) |
| Inspect why results ranked | Per-signal score breakdown |
| Filter by type/tier/date/outcome/decay | Search panel filters |
| Preview exact agent recall block | `POST /api/v1/memories/recall/preview` |
| Inspect clipped-by-budget memories | Recall preview side panel |
| Copy equivalent CLI command | "Open in CLI" action |

Hard contract: dashboard and CLI search use the same in-process retrieval engine path (parity-tested in CI).

## AI Agent Integration (V1 Plan)
V1 is CLI-first. No daemon, MCP, or Node is required for the agent path.

Example planned usage:

```bash
# session start
agent-memory recall -w <ws> --task "<one-line task>" --budget 4000 --format raw

# learned a fact
echo "<fact>" | agent-memory write -w <ws> --type semantic --content - --format json

# outcome memory
agent-memory write -w <ws> --type outcome \
  --content "<what you tried>" \
  --outcome-result success|failure|partial \
  --outcome-approach "<how>" \
  --outcome-reason "<why>" \
  --format json

# session end
cat <transcript.json> | agent-memory session-end -w <ws> --from-stdin --format json
```

MCP is deferred to V1.5+ as a thin TypeScript wrapper over the same CLI contract.

## Data Layout (Planned)
```text
~/.agent-memory/
├── workspaces.json
├── <project>.db
├── archived/
│   └── <project>.<RFC3339>.db
├── models/all-MiniLM-L6-v2/
└── logs/
```

Per project:
- `.cursor/rules/agent-memory.mdc` stores workspace hint for agents.

## Roadmap
### V1
- Local-first SQLite storage per project with deterministic local embeddings
- CLI-first workflow: write/search/recall/session-end + project lifecycle commands
- Optional local dashboard for search + recall preview
- Study/bootstrap from local files and directories

### V1.5
- Optional MCP integration as a thin wrapper over the CLI contract

### V2+
- External connectors (Confluence/Jira/Notion), shared/team memory, cross-workspace recall

## Privacy / Security
- Local-first by default (`~/.agent-memory/`).
- Write pipeline includes secret/PII filtering controls.
- Data leaves local machine only when explicitly configured in future remote modes.
