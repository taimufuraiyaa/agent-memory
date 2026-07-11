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
go install ./cmd/agent-memory
```

#### Option C: Installer Script (Auto-downloads local embedding model)
```bash
# Unix/Linux/macOS:
go run install.go install_unix.go

# Windows:
go run install.go install_windows.go
```

The installer configures every supported AI agent by default. For Codex it preserves existing settings while adding the agent-memory data directory as a narrow writable sandbox root and installing lifecycle hooks, so users do not edit Codex configuration manually. Codex may still request its native one-time hook trust confirmation.
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
- Automatically writes rule files (e.g., Cursor rules at `.cursor/rules/agent-memory.mdc`, Trae rules, etc.) to prompt the agent to use `agent-memory`.

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
| `agent-memory recall` | Retrieve stable, token-budgeted prompt context for a task |
| `agent-memory session-end` | Parse a session transcript to extract clean learnings |
| `agent-memory study` | Bootstrap/learn from project documents and code files |
| `agent-memory dashboard` (`ui`) | Start and open the local web-based dashboard |

---

## Day-to-Day CLI Examples

```bash
# Write a fact
agent-memory write --type semantic --content "Authentication service handles JWT validation"

# Store an outcome memory (with approach and reasoning details)
agent-memory write --type outcome \
  --content "Upgraded Node.js client package" \
  --outcome-result success \
  --outcome-approach "Updated package.json and ran npm install"

# Search memories
agent-memory search --query "auth validation" --top-k 5

# Recall context for a task (within a 4000-token budget)
agent-memory recall --task "debug JWT token validation failure" --budget 4000 --format raw

# Extract learnings at the end of a session
cat session_transcript.txt | agent-memory session-end --format json
```

---

## Local HTTP Dashboard
The dashboard is served locally by the Go binary (no separate servers required) and matches the core API engine path exactly:

```bash
# Start and open in browser
agent-memory dashboard

# Run headlessly in the background
agent-memory dashboard --start --addr :3210
agent-memory dashboard --stop
```

### Dashboard Capabilities:
- **Workspace Navigation**: Swap databases/projects instantly via the UI.
- **Interactive Search**: Run queries with precise type, tier, outcome, and date filters.
- **Explain Mode**: Click to inspect calculated scores (`semantic_similarity`, `recency`, `outcome_boost`, `decay_weight`, `tier_bias`).
- **Recall Preview**: Preview the formatted prompt block that will be injected into the agent, highlighting clipped memories and tokens used.

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
