# agent-memory
A persistent, multi-tier memory layer for AI coding agents (Cursor, Claude Code, Codex, Cline, custom). It retains knowledge across sessions, learns from outcomes, and reduces repeated research/token consumption through hybrid storage and lifecycle management.

<img width="2292" height="1766" alt="CleanShot 2026-05-29 at 01 28 36@2x" src="https://github.com/user-attachments/assets/bf0c19b6-c75d-4ee0-8dd8-c72033e2dedf" />
<img width="2430" height="1806" alt="CleanShot 2026-05-29 at 01 33 15@2x" src="https://github.com/user-attachments/assets/122c9408-110b-447f-b11b-f380a4b26baa" />
<img width="2992" height="1806" alt="CleanShot 2026-05-29 at 01 23 12@2x" src="https://github.com/user-attachments/assets/851abf28-683c-4c8e-869d-641c38cebc1a" />



## Status
- Core implementation is actively in progress and runnable in this repository.
- CLI + local HTTP dashboard are available.

## Why This Exists
Current agents are mostly stateless between sessions. Markdown-only notes and vector-only stores each solve part of the problem, but not the full memory lifecycle.

### Hybrid Design
| Tier | What It Holds | Why |
|---|---|---|
| Markdown | Pinned conventions, project rules, AGENTS.md-style facts | Always loaded, zero retrieval cost |
| Vector | Semantic recall over discovered facts (SQLite-backed; deterministic and local) | Fast similarity search without mandatory cloud |
| Graph | Service/topic/file relationships | Captures structural links vectors miss |
| Document | Raw episodic transcripts, larger analyses | Cold archive referenced by other tiers |
| Tombstones + Reconstruction | Markers of forgotten memories + recovery strategies | "Tip of the tongue" graceful re-investigation |

Local-first by default: per-workspace SQLite databases under `~/.agent-memory/`, plus an embeddings layer (local model support is evolving).

## How Recall Works (System Design)
At a high level, `agent-memory` treats recall as a ranked retrieval problem under a strict token budget.

### 1) Search vs Recall
- `search` is for interactive inspection: find relevant memories for a query.
- `recall` is for session start: assemble a compact context block that fits the budget.

### 2) Retrieval signals (explainable)
Retrieval starts with a semantic candidate set, then re-ranks with multiple signals:
- Semantic similarity (vector-based)
- Recency (recently updated items matter more)
- Outcome signal (successful/failure outcomes can be boosted depending on mode)
- Decay penalty (old/unhelpful items fade)
- Tier bias (some tiers are preferred for recall stability)

These signals are combined with mode-specific weights (e.g., recall weights differ from search weights) and returned with a per-item breakdown when `explain` is enabled.

### 3) Budgeted recall assembly
For `recall`, ranked hits are then:
- Rebalanced by task intent (prioritize procedural vs outcome vs semantic depending on the task wording)
- Clipped to a hard token budget (deterministic baseline counter today)
- Emitted as a stable, sectioned `context_block` so the agent can paste it directly into the system prompt

If something doesn’t fit, it is reported as “clipped” with a reason (budget exceeded vs item too large).

## Forgetting, Decay, and Reconstruction
Forgetting is an explicit part of the system so the memory store stays useful over time.

### Decay scoring
Each memory gets a decay score in `[0, 1]` (higher = more decayed) based on:
- Time since update (type-specific half-lives)
- Access frequency (frequently used memories decay slower)
- Pins and successful outcomes (can slow decay)

Decay is used both as a ranking signal and as an input to lifecycle decisions.

### Lifecycle (REM cycle)
The lifecycle manager periodically performs maintenance steps like:
- Consolidation (merge overlapping items into cleaner “facts”)
- Conflict detection/resolution
- Tier movement (promote/demote between Markdown/Vector, and keep Markdown within a token budget)
- Eviction when the store exceeds limits

### Tombstones and reconstruction
When an item is evicted, the system leaves a small tombstone (a breadcrumb). If a later query matches a “gap” signal (you’re asking about something the store used to contain), the reconstruction engine can propose or create a reconstructed semantic memory derived from those historical fragments (with safeguards to avoid reconstruction loops).

## Quickstart
```bash
cd agent-memory
go test ./...
go build ./...
```

## Install

### Prerequisites
- Go toolchain matching [go.mod](go.mod) (currently `go 1.26.3`)

### Install via Homebrew (tap)

This repo includes a Homebrew formula at `Formula/agent-memory.rb` for `--HEAD` installs (build from source).

```bash
brew tap taimufuraiyaa/agent-memory https://github.com/taimufuraiyaa/agent-memory.git
brew install --HEAD taimufuraiyaa/agent-memory/agent-memory
```

Notes:
- Do not wrap the URL in backticks; backticks execute a command in your shell.

### Install the CLI binary

From this repo:

```bash
cd agent-memory
go install ./cmd/agent-memory
```

Or use the installer (also downloads the local embedding model):

```bash
cd agent-memory
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

Recommended retrieval policy:
- Start with `agent-memory search` for focused discovery.
- Run `agent-memory recall` only when you are continuing previous work, or when search returns no useful / weak / insufficient results.
- For prompts like `continue`, `resume`, or `what were we doing`, escalate directly to `recall`.

### Dashboard (optional)

```bash
agent-memory dashboard --addr :3210
```

To print the URL without opening a browser:

```bash
agent-memory dashboard --addr :3210 --no-open
```

Background start/stop:

```bash
agent-memory dashboard --start
agent-memory dashboard --stop
```

What you can do in the dashboard:
- Switch workspaces/projects from the dropdown (this comes from `agent-memory init` / `agent-memory list`).
- Run natural-language search with optional filters (type, tier, outcome, confidence, decay, entities, date range).
- Toggle explain mode to see the score breakdown fields (`semantic_similarity`, `recency`, `outcome_boost`, `decay_weight`, `tier_bias`) plus `match_reason`.
- Use Recall Preview to see the exact `context_block` the agent would load for a task, plus which memories were clipped by the token budget (`memories_clipped`) and why.

Notes:
- The dashboard is local-only and served by the same Go binary; there is no separate Node/React dev server required.
- The dashboard uses the same in-process retrieval engine path as the CLI (parity-tested).
- You do not need `--workspace` just to open the dashboard; start it normally and switch projects from the dropdown when needed.

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
| `agent-memory dashboard` (`ui`) | Open the local dashboard (starts the HTTP server) | Engineer inspection |

Planned CLI contract:
- Deterministic JSON envelope on stdout with `--format json`.
- Progress/logging on stderr.
- Stable exit codes (`0/1/2/3/4/5/124`).

## Engineer Dashboard (Search + Recall Preview)
Run `agent-memory dashboard` to start the local HTTP server and open `http://localhost:3210/dashboard/` for human inspection.

| Engineer Can... | How |
|---|---|
| Search across markdown/vector/vector+graph/document | `POST /api/v1/memories/search` (`filters.tiers`, optional `explain: true`) |
| Inspect why results ranked | Per-signal score breakdown |
| Filter by type/tier/date/outcome/decay | Search panel filters |
| Preview exact agent recall block | `POST /api/v1/memories/recall/preview` |
| Inspect clipped-by-budget memories | Recall preview side panel |

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
