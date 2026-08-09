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

`agent-memory reinstall` and `agent-memory init --reuse` run the same idempotent term-index preparation for existing projects. A normal `agent-memory upgrade` prepares every registered project and reports per-project successes or failures; one damaged database does not prevent healthy projects from upgrading, and the damaged project remains fail-open. `upgrade --dry-run` and `upgrade --hooks-only` do not touch project databases.

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

---

## Day-to-Day CLI Examples

```bash
# Write a fact with up to three explicit search locators
agent-memory write --type semantic \
  --content "Authentication service handles JWT validation" \
  --keyword authentication --keyword jwt

# Store an outcome memory (with approach and reasoning details)
agent-memory write --type outcome \
  --content "Upgraded Node.js client package" \
  --outcome-result success \
  --outcome-approach "Updated package.json and ran npm install"

# Search memories
agent-memory search --query "auth validation" --top-k 5

# Exact project term search: AND requires every term; OR requires at least one
agent-memory search --mode terms --query "authentication jwt" --operator and
agent-memory search --mode terms --query "authentication oauth" --operator or

# Backfill/rebuild and safely inspect Bloom health (no raw terms or bitmap)
agent-memory reindex-terms --target-fpp 0.01
agent-memory reindex-terms --status

# Recall context for a task (within a 4000-token budget)
agent-memory recall --task "debug JWT token validation failure" --budget 4000 --format raw

# Review workspace memory quality, context efficiency, hygiene, coverage, and trust
agent-memory advisor
agent-memory advisor --format json

# Extract learnings at the end of a session
cat session_transcript.txt | agent-memory session-end --format json
```

### Exact-term Bloom rollout

`AGENT_MEMORY_TERM_BLOOM_MODE` controls only exact term mode:

- `shadow` (default): probe Bloom but always execute canonical SQLite lookup.
- `gate`: skip canonical lookup only for a healthy, checksum-valid, current definite miss. `AND` can stop when any token is absent; `OR` stops only when every token is absent.
- `off`: immediate kill switch; bypass Bloom and fail open to canonical lookup.

Missing, dirty, rebuilding, corrupt, incompatible, generation-mismatched, saturated, high-FPP, or delete-heavy state always fails open. Run `agent-memory reindex-terms --status` for the stable bypass/rebuild reason, and run `agent-memory reindex-terms` to rebuild. Semantic search behavior is unchanged.

---

## Local HTTP Dashboard
The dashboard is served locally by the Go binary (no separate servers required) and matches the core API engine path exactly:

```bash
# Start and open in browser
agent-memory dashboard

# Run headlessly in the background
agent-memory dashboard --start --addr 127.0.0.1:3210
agent-memory dashboard --stop
```

For a source checkout, build the embedded dashboard and binary once with
`make build-with-dashboard`, then run `./bin/agent-memory dashboard --addr
127.0.0.1:3210`. The production binary serves `/dashboard/` itself; npm is not
needed at runtime.

### Notebook and dashboard capabilities

- **Notebook-first workspace**: Notes is the default home, with a workspace explorer, multi-note tabs, Markdown editing, rendered preview, Mermaid diagrams, and responsive desktop/mobile layouts.
- **Human + agent recall**: Saved note revisions are indexed asynchronously as replaceable memory chunks. Search and Ask label Human note and Agent memory evidence separately and retain note path, revision, heading, and line provenance.
- **Safe document lifecycle**: Autosave uses optimistic revisions, revision history is immutable, normal deletion moves notes to recoverable trash, and failed indexing never blocks reading or editing.
- **Connected notes**: `[[Internal links]]`, backlinks, outline navigation, typed properties, folder paths, and revision restore are available from the context panel.
- **Grounded Ask**: Ask can search the active note, current workspace, or all local workspaces; citations reopen human notes, and answers can become notes only through an explicit confirmed action.
- **System workspace**: Existing Overview, Sessions, Diagnostics, Benchmark, Lifecycle, Wiki, Feedback, Skills, raw stats, Explain Mode, Recall Preview, and Memory Advisor remain reachable under System.
- **Per-client MCP profiles**: System → Clients registers Codex, Claude, Cursor, or custom clients and assigns Default (five workflow tools) or Expanded (adds health and session browsing). Add the displayed `AGENT_MEMORY_CLIENT_ID` value to that client's MCP environment and reconnect it. Profiles apply across all local workspaces, but each client selects its own profile.
- **Internal infrastructure settings**: System → Infrastructure lets internal operators configure the installation-wide monthly infrastructure operations budget and its assumption status. New installations default to an assumed USD 1,000/month. The control is not available to tenants or MCP clients, and saving never deploys infrastructure or spends money.
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

## Local SaaS Service Deployment

Run the complete multi-process product locally with persistent PostgreSQL,
object storage, NATS, migration, API, worker, and reconciler services:

```bash
# Default profile: MinIO with service-specific capability policies
make saas-local-up

# Optional AWS compatibility profile: Floci 1.6.0 provides S3 locally
make saas-floci-up

# Optional managed-identity rehearsal: Floci plus ephemeral local OIDC
make saas-floci-oidc-up

# Verify the complete signup-to-account-deletion lifecycle
make saas-upload-smoke

# Run an isolated Floci alpha and publish a content-free evidence package
make saas-local-alpha-gate

# Stop either profile without deleting its named volumes
make saas-local-down
```

Both profiles serve the hosted dashboard through the separate local edge at
`http://localhost:58081/dashboard/`. The edge binds only to loopback, replaces
trusted geography assertions, owns request correlation, and keeps internal
metrics off customer ingress. The Floci profile stores its state in a separate
volume and leaves PostgreSQL and NATS on their native local services.
Its digest-pinned base is patched during the local build, runs as a non-root
user with a read-only root filesystem and no Linux capabilities, and exposes
S3 only on loopback. It is intended for functional AWS S3 compatibility; the
MinIO profile remains the required least-privilege object-policy gate, and
neither emulator is production release evidence. See the
[Floci project](https://github.com/floci-io/floci) for its AWS compatibility
scope.

The OIDC profile keeps the same edge URL and exposes its loopback-only provider
on port `58082`. It exercises the production discovery/JWKS verifier with one
fixed synthetic identity and ephemeral signing keys. It is opt-in; restoring
`make saas-floci-up` removes the provider and returns to development identity.
This rehearsal is not evidence of a managed identity provider, production key
custody, MFA, recovery policy, staging, or production readiness.

The alpha gate creates a separate Compose project with dynamic loopback ports
and temporary volumes, so it does not mutate the persistent local product. It
exercises OIDC authentication and rotation/outage recovery, runtime trust-secret
rotation and failed-configuration rollback, scratch-only operator break-glass,
lifecycle, retrieval parity, a two-tenant isolation/timing corpus, bounded
concurrent retrieval load, credential-abuse detection and revocation, a
production-adapter model-provider outage, explicit source/account deletion
evidence, scratch-database backup/restore,
deployment contracts, runtime hardening, image vulnerability scans, and real
PostgreSQL/NATS/Floci impairment and recovery. API readiness checks all three
dependencies while liveness remains process-only. Passed manifests, receipts,
archives, and SHA-256 sidecars are written under
`.local/evidence/`; they are classified as local development evidence and do
not replace staging or accountable-owner approval.

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
