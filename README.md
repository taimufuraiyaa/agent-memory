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

For Claude subagent routing the hook writes the catalog ID verbatim into the Agent call's `model` field, and the Agent tool accepted the aliases `haiku`, `sonnet` and `opus` when this was verified live (a `claude` catalog of those three aliases routed a real call from `sonnet` to `haiku`, confirmed by the host's `resolvedModel`). Full model IDs like the examples above were not tested for Agent calls, so prefer the aliases your Agent tool accepts.

These are example identifiers from the [Claude model catalog](https://platform.claude.com/docs/en/models/overview) and [official OpenAI documentation](https://developers.openai.com/api/docs/models/all), **not** an entitlement check. Configure only models available to your account and host. With `/am listen all`, the Claude prompt hook asks Jev to choose from the Claude entries instead of a generic complexity tier and presents the exact choice as advice. Separately, when Claude invokes an `Agent` subtask, the managed `PreToolUse` hook asks Jev about that bounded, redacted subtask and replaces only the Agent call's `model` field with a confident Claude-catalog choice. It does not change the main session or bypass Claude Code's tool permissions. Missing catalog, Jev failure, or low confidence leaves the original Agent call unchanged. The hook reports the requested subagent model; Claude Code may substitute another allowed model, and its Agent tool result's `resolvedModel` is the actual host observation. This adds a Jev request to the subagent-start critical path; latency and cost improvements are unproven until measured. A local ChatGPT/OpenAI-side dispatcher can request a one-shot recommendation by piping the task into `agent-memory model-choice --workspace <name> --host chatgpt --allow-jev` from the registered project. The command returns a validated candidate ID and `model_switched:false`; it does not change an existing ChatGPT conversation or call the OpenAI API. Without a catalog, Claude retains the complexity-tier advisory and the one-shot command returns no choice. The task and candidate descriptions leave the machine only after the explicit Jev opt-in or one-shot flag.

The `chatgpt` catalog and project [jev-model-router skill](.agents/skills/jev-model-router/SKILL.md) support the ChatGPT app as a recommendation; apply the choice in its model picker if available. The separate `openai_api` catalog supports a bounded, text-only API execution path: set `OPENAI_API_KEY`, then pipe one task into `agent-memory model-run --workspace <name> --allow-jev --allow-openai-api` from the registered project. The command probes access to Jev's selected model and creates one [OpenAI Responses request](https://developers.openai.com/api/reference/cli/resources/responses/methods/create) with `store:false`, no tools, a 512-token default output cap, and no retry. Both the task and model candidates go to TypeSafe; the redacted task also goes to OpenAI. This may incur API charges. It does not change any existing ChatGPT chat, run a coding-agent tool loop, or infer API access from a ChatGPT subscription.

### Local harness client authority (opt-in)

`agent-memory harness authority enable|disable|status` switches the opt-in capability pack, which is off by default. `agent-memory harness grant create --client <id> --workspace <name> --operation start,status [--ttl 24h]` mints a bearer grant and prints its token once; `grant list`, `grant rotate <id>` and `grant revoke <id>` manage it. A grant is bound to a registered client profile, registered workspaces (the canonical project root is recorded and a changed or swapped root is denied), an operation allowlist, an expiry and a revision. `approve` is never grantable: approving a mutation is a trusted local action. Only a SHA-256 of the secret is stored, in `~/.agent-memory/credentials/harness-authority.json` (mode `0600` in a `0700` directory; symlinks, loose permissions and unknown schemas fail closed). Every failed verification returns the same denial, the registry and grant file are re-read on each check so revocation takes effect immediately, and a profile ID, loopback address or caller-supplied principal is never authority. The credential gate is consumed by the opt-in harness API below. Run these commands from a trusted terminal: a coding agent with unrestricted shell access acts as you and is outside this boundary.

### Local harness API and MCP tools (opt-in, fake providers only)

This is a vertical slice that proves the path from an MCP client to the Go runtime. **No real model is called**: the only providers are scripted fakes, and capability discovery labels them as such. Nothing is exposed unless you opt in at every layer.

1. Start the service with the fakes composed in: `AGENT_MEMORY_HARNESS_PROVIDERS=fake agent-memory serve`. Unset, the harness is not composed and its routes do not exist; any other value is an error.
2. Enable the pack and mint a grant with `agent-memory harness authority enable` and `agent-memory harness grant create ...` (above).
3. Give the MCP adapter the token with `AGENT_MEMORY_HARNESS_TOKEN`. Only then do four extra tools appear, after the profile's own tools: `harness_capabilities`, `harness_start`, `harness_status` and `harness_cancel`. They are never part of the default or expanded profile, and there is no approval tool.

The HTTP routes are `GET /api/v1/harness/capabilities`, `POST /api/v1/harness/runs`, `GET /api/v1/harness/runs/{id}` and `POST /api/v1/harness/runs/{id}/cancel`, all requiring `Authorization: Bearer <grant>` (a token in a URL is ignored) and a `workspace`. Start returns at once with a queued run; pass an `idempotency_key` so a retry, including after an adapter reconnect, returns the same run. Mutations carry the `expected_generation` you last read, and a stale one is rejected. Every authentication failure is the same `401`, a run another client started is indistinguishable from one that does not exist, and error text is fixed and never carries runtime detail. Status and events show state codes and counters, never your goal, prompts or provider output. Continue, events and artifact routes are not exposed yet.

### Project read tools (not yet reachable by a real model)

The harness has three read-only project tools: `read_file` (line windows with numbers and a revision), `list_dir` and `search` (literal or regular expression, with context lines, a file glob and a path scope). They are confined to the registered project root by the operating system, so a symlink or `..` cannot leave it; hidden entries (`.env`, `.git`, `.ssh`) and credential-like names (`id_rsa`, `*.pem`, `*.key`, `credentials*`, `secrets*`) are refused; only regular text files are opened; every result is size-bounded, redacted for secrets and personal data, and returned in a fixed order. The default policy denies every tool, and the read tools are allowed only when explicitly configured. Git is separate and off by default (see "Git reads, staging and commits" below), because even `git status` can run programs named in a repository's own configuration. A real model cannot call these yet: the OpenAI provider is text-only until tool schemas and function calling are added, so today only the scripted test model exercises them.

### Guarded edits and approvals (built, not yet reachable by a real model)

The harness can also change files, behind its own switch: with editing off the provider offers only the three read tools. Three tools exist. `edit_file` makes one to twenty exact-text replacements (each old text must appear exactly once in the file as it is now, and edits must not overlap), `create_file` makes a new file and any missing directories, and `delete_file` removes one text file. There is no rename; it is a create plus a delete, and each is reviewed. Hidden locations (`.github`, `.gitignore`, `.kiro`, `.claude`, `.git*`), credential-like names, links, binary or non-UTF-8 files, and anything outside the project root are refused before anyone is asked.

A run that wants to change something **stops and asks**. The change is recorded with its exact arguments, a unified diff built from the edit ranges (control, bidirectional and zero-width characters are shown as `⟨U+XXXX⟩` markers so nothing is hidden from you), and a digest that covers the file's current revision, so you can only ever approve the change to the file as you saw it. Review and decide in a terminal:

```
agent-memory harness approvals list [--all] [--workspace NAME]
agent-memory harness approvals show APPROVAL_ID
agent-memory harness approvals approve APPROVAL_ID
agent-memory harness approvals deny APPROVAL_ID [--stop]
agent-memory harness approvals undo APPROVAL_ID
agent-memory harness approvals audit
```

`approve` needs an interactive terminal on both input and output, prints the change, then asks you to **type the short code printed beneath it**; there is no flag, environment variable or pipe that supplies it, and one approval covers exactly one action. Edits, creates and ordinary changes ask for a four-character code. Deletions, dependency manifests and lockfiles, build and CI definitions, Dockerfiles and Makefiles, shell scripts, migrations and schemas, instruction files (`CLAUDE.md`, `AGENTS.md`), executables, new directories and unusually large changes ask with extra friction: the reasons are printed, a second warning is shown, and the code is nine characters. Five wrong codes deny the approval, an approval lapses after thirty minutes, and a change that cannot be shown in full (a 48 KB preview) is not offered at all, so the model must split it.

If the file changed after you reviewed it, nothing is written: the replay prepares the stored call again, requires the same digest, asks policy once more, and only then runs. Before any change a private copy of the original is saved, the replacement is atomic and rechecked immediately beforehand, and a result that does not read back correctly is rolled back. `undo` restores the file only if it is still exactly as the change left it, and refuses if anything touched it since. Every request, decision, application and undo is appended to a hash-chained log; a log that cannot be written or no longer verifies stops further approvals, and `audit` reports whether it still verifies.

A deny returns a message to the model so it can try something else; `--stop` ends the run. Revoking the client's grant ends its pending approvals and cancels the run. An MCP client sees only that a run `needs_attention` and an opaque approval identifier. It never sees the digest, the preview or the paths, and there is no route, tool, grant operation or run-manager method that approves anything: the manager only applies decisions recorded here.

**Limits.** Nothing composes the project tools into `serve` yet, because the only real provider is text-only and cannot call tools; the whole path is verified with a scripted model, the real manager, the real tools and the real command. The boundary is between callers reachable through MCP and the person at the terminal: a process running as your own user can edit the approval files directly, just as it can edit your project. The recheck and the replacement are not one atomic step, so a local process racing the write can win the gap. Ownership, extended attributes and permissions beyond the mode bits are not preserved on replacement. A run interrupted after its approval was consumed but before the change ran loses that result and never replays it. Windows is untested.

### Running commands (built, not yet reachable by a real model)

With commands enabled the harness offers one more tool, `run_command`, which runs a program with an **exact argument list and no shell**: pipes, redirection, globbing, `&&`, background operators and `VAR=value` prefixes are not interpreted, they are just characters in an argument. It is off unless enabled, it never runs without your approval, and the approval names the exact executable, so replacing the program afterwards makes the approval stale instead of running something else.

**This is not a sandbox.** An approved command runs as you and can read or change anything you can, including opening network connections. Running a test runs the project's code, including code a model wrote and you approved as an edit. What the harness adds is the envelope: you see precisely what will run before it runs; the command gets no standard input, a built environment (a minimal `PATH`, your home directory so toolchain caches work, locale and plain-output settings, a private temporary directory that is deleted afterwards, and offline settings for Go, Cargo and npm) and none of your other variables, so API keys and tokens in your environment never reach it; it runs in its own process group in a checked project directory, for at most the timeout (default one minute, at most eight); on timeout, cancellation or shutdown the group is terminated, then killed, and anything left behind is killed after the command ends; and the model receives only redacted, escape-sequence-free output that keeps the beginning and the end. The offline settings discourage network use but cannot prevent a command that sets out to use it.

Commands fall into three groups. A **recognized** toolchain command asks with the ordinary four-character code: `go test|vet|build`, `gofmt -l|-d`, `cargo test|check|build|clippy`, `pytest` or `python -m pytest|unittest`, and `node --test`, each with a fixed set of flags and only local package or path arguments. Flags that run other programs, write elsewhere or change where code comes from (`-exec`, `-toolexec`, `-overlay`, `-o`, `-coverprofile`, `--` and similar) make a command unrecognized. Commands that run a **recipe defined in the project** (`npm test`, `npm run test|build|lint`, `make test|build|lint`) and **anything else** (a script in the project written as `./scripts/x.sh`, an interpreter given code on its command line, an unfamiliar program) ask with extra friction: the reason is printed and the code is nine characters. **Refused outright, with no question:** shells and launchers (`sh`, `bash`, `env`, `xargs`, `nohup`...), privilege and permission tools (`sudo`, `chmod`...), network tools (`curl`, `ssh`...), deletion, move and link tools (`rm`, `mv`, `ln`...), process killers, schedulers and desktop automation, container tools, and version control (a later stage). The check applies to the name given and to what it resolves to, so a link or a rename does not get past it. A bare program name is looked up only on the harness's own search path, never in the project, so a binary placed in the project cannot stand in for a toolchain.

After the command ends the harness reports what it changed: files created, modified and removed (up to a limit), and, loudly and separately, any change to places an attacker would use to persist, which the read tools refuse to show: git hooks and repository settings, the ignore file, the automation and tool-settings directories and any dotfile in the project root. A change there is also written to the approval audit. This is detection after the fact, not rollback, and it cannot see changes outside the project. `undo` has nothing to restore for a command and says so. A command's run time counts against the run's time budget.

**Limits.** No sandbox, as above. Processes that move themselves into a new session can outlive a command. Only time and output are limited, not memory or CPU. The change report misses hidden files other than the listed ones. The tool is not composed into `serve` yet because the only real provider is text-only; it is verified with a scripted model, the real manager, the real approval command and real processes. Windows refuses to run commands rather than half-support them.

### Git reads, staging and commits (built, not yet reachable by a real model)

With Git enabled the harness offers five structured tools: `git_status`, `git_diff` (unstaged or staged, optionally one path), `git_log` (up to 50 commits, optionally one path), `git_stage` and `git_commit`. There is no passthrough: the model cannot give Git arguments, and the operations that would change history, move data or reach another machine do not exist at all rather than being denied: push, fetch, pull, remotes, configuration changes, reset, clean, checkout and restore, rebase, merge, stash, tag, submodules, worktrees and branch operations.

**Why this needs care.** Git runs programs that a repository's own settings name. A hostile clone can make a plain `git status` or `git diff` execute a command through a filesystem monitor, an external diff or text-conversion program, a pager, a hook, or a clean filter (and a clean filter runs even for status and cannot be switched off from the command line). So the harness does two things. First, before running Git it **reads `.git/config` itself** and refuses the repository, with a fixed reason code, unless every setting is on a short allowlist of settings that cannot start a program or reach outside the project; it also refuses a repository that is a link or a pointer file (`.git` must be a real directory under the project root), uses includes, alternate object stores or shared storage, or whose configuration it cannot read with certainty. A refusal names the offending setting but never its value, and applies to every Git tool, reads included. Second, it runs the Git it finds on the harness's own search path (never one inside the project) from a built environment, with the settings that start programs overridden on the command line, no hooks, literal path arguments and no terminal prompts. Your own global Git configuration (in your home directory) is trusted and applies, so your identity and preferences work; it is not scanned. The system-wide configuration is not read.

**Reads** are allowed without asking once the repository passes. Results are size-bounded (500 status entries, 200 diff paths), redacted, free of terminal escape sequences, and never mention protected paths: hidden entries and credential-like files are left out and counted (`omitted_protected`) so nothing is silently missing. Submodules are ignored. The log shows author names but not email addresses.

**Staging** names exact files, up to 32, one by one. A directory, a pattern, a path outside the project, a hidden or credential-like file, a link, a nested repository, a file over 8 MB, a file with a conflict, and a file with nothing to stage are all refused before anyone is asked. The approval shows the branch, the commit it sits on, each file and whether it is new, modified or deleted, the diff of tracked files and the first lines of new files (control and invisible characters appear as `⟨U+XXXX⟩`). The digest covers every named file's content, the branch and commit, and the Git program, so a file edited after review, or a branch that moved, makes the approval stale instead of staging something you did not see. A change too large to show in full (48 KB) is not offered, so the model must split it. Staging more than 20 files asks with extra friction.

**Committing** records exactly what is already staged, on the current branch, with a message you can read: the approval shows the branch, the parent, the author, the message, a summary and the staged diff. The author is whatever your own Git configuration says; if none is configured the commit is refused, nothing is invented. The digest binds the staged change, the message, the author, the parent and the Git program, so a change slipped into the index, a different author setting or a moved branch after review makes the approval stale. Hooks are not run. After the commit the harness re-reads it and compares it with what was approved; a mismatch is reported to the model, shown first in the result and written to the approval audit as `commit_mismatch`. A diff shortened in the preview, or more than 20 files, asks with extra friction, and a commit that includes dependency manifests, build or CI files, instruction files or other control-plane paths asks with extra friction too, whichever paths were listed first. A protected file that was forced into the index, or a submodule entry, is refused.

Approvals for staging and committing use the same terminal flow as edits (`agent-memory harness approvals ...`), the same four-character code, thirty-minute lifetime and hash-chained audit. Reads, stage and commit are only offered when Git is on the search path and the platform can run programs.

**Limits.** This does not make a repository safe in general: an approved `run_command` or edit still runs as you, and the settings check is an allowlist by name: a section or key it does not list is refused, but a few sections that hold only harmless settings today (user, branch, push, pull, fetch, color, advice, pack, gc and similar) are accepted whole, so a program-starting key that a future Git adds to one of them would not be noticed. Your global Git configuration is trusted and not scanned. There is no commit undo and no way to unstage; fix mistakes in your own terminal. Branch creation or switching is not offered, so work happens on whatever branch is checked out. Submodules and nested repositories are ignored. Windows refuses Git rather than half-support it. The tools are not composed into `serve` yet, because the only real provider is text-only; they are verified with a scripted model, the real manager, the real approval command and a real Git against deliberately hostile repositories.

### Jev decisions in the harness (opt-in, openai mode)

With the OpenAI provider composed, the harness can ask TypeSafe Jev for **advice** on three choices: which eligible model to prefer (`model`), which context chunks deserve more room (`visibility`) and how to order evidence for the provider's prompt cache once cache hits have been measured (`cache`). Advice only chooses among options the harness already allowed; it can never grant access, approve, lower friction or run anything, and any failure (slow, denied, malformed, low confidence, unreachable) leaves the deterministic choice in place. Four more kinds exist in code and are wired where their consumers are composed (tool narrowing, command-risk friction, file sensitivity, subgoal deduplication); they are refused here because this mode has no tools yet.

Enable it with all of: `AGENT_MEMORY_HARNESS_PROVIDERS=openai` and its settings, a Jev credential stored through the TUI, `AGENT_MEMORY_HARNESS_JEV=model,visibility,cache` (any subset), and `AGENT_MEMORY_HARNESS_JEV_EGRESS=typesafe`. Optional: `AGENT_MEMORY_HARNESS_JEV_BUDGET` (requests per run, default 40) and `AGENT_MEMORY_HARNESS_JEV_PER_MINUTE` (default 30). Anything missing, misspelled or duplicated stops startup.

**What is sent.** Only positional aliases, counts, sizes and a fixed vocabulary: never prompts, goals, file contents, paths, run identifiers or the credential. Chunk identifiers are replaced by aliases before anything leaves the process; model names are the ones you configured. Questions that would need project text are not asked at all in this mode.

**Safety net.** Each request has its own deadline, requests are bounded in flight, per run and per minute, and repeated failures pause asking (every kind on provider faults, one kind on malformed answers). The capabilities endpoint reports a content-free `decisions` view: reachability, what is paused, and counts per kind and status.

**Limits.** The structure of every answer is verified, not the quality of Jev's judgment. The live check of the decision provider against the real service has not been run (`AGENT_MEMORY_LIVE_JEV_TEST=1 go test ./internal/harnessjev -run TestLiveTypeSafeDecision -v` sends a few synthetic questions with your stored token). Tools, risk and sensitivity advice await composing tools into `serve`.

### Tools, workers and the full MCP controls (openai mode, opt-in)

**Project tools in `serve`.** With the openai composition, `AGENT_MEMORY_HARNESS_TOOLS=read,edit,git,commands` (any subset; read is implied) composes the project tools described above into the runtime with the project policy: reads run freely; every edit, stage, commit and command parks the run and waits for you at the terminal (`agent-memory harness approvals ...`), exactly as in the guides above. The OpenAI adapter now offers the composed tools to the model through function calling; a call to a tool that was not offered is a failure and is never passed on, and the model can ask you a question through a built-in `clarify` request. `AGENT_MEMORY_HARNESS_MAX_TOOLS` limits how many tools one call describes (the Jev `tools` decision, if enabled, chooses which). Jev's `tools` and `command_risk` decisions can now be listed in `AGENT_MEMORY_HARNESS_JEV` when tools are composed. Tools are refused in fake mode.

**Workers.** `internal/harnessworker` runs subgoals as child runs: exact repeats are merged (and, with a decision service, near repeats), each worker gets an equal share of what the parent has left so the workers together cannot exceed it, `MaxParallel: 1` serializes them, they stop when the parent stops, and an ownership policy denies an action that changes a file another run owns. A shared snapshot records the first revision read of each file so a change under the workers is noticed. It is a library the runtime uses; a model cannot spawn workers itself yet.

**More MCP tools.** After the four above, a granted adapter also gets `harness_continue` (answer a clarification; it can never approve), `harness_events` (cursor-paged event log), `harness_artifact` (read one bounded result) and `harness_readiness` (what is composed and healthy, with no provider call). HTTP: `GET .../runs/{id}/events`, `POST .../runs/{id}/continue`, `GET .../runs/{id}/artifacts/{aid}` and `GET .../readiness`.

**Limits.** Windows and sandboxing limits of the command tool apply; no hosted path exists; the live coding task has not been run against OpenAI or Claude.

**Ask Jev directly.** A granted client can use the Jev decisions without starting a run: MCP `harness_decide`, or `POST /api/v1/harness/decide`. It needs the grantable `decide` operation (a token minted with all operations has it; older explicit grants do not) and the decision kind listed in `AGENT_MEMORY_HARNESS_JEV`. The kinds are `visibility`, `model`, `tools`, `cache` and `command_risk`, asked from identifiers you choose, a short class word and integer facts only; there is no field for text, a path or a note, and the two kinds that carry project text are not offered. The reply is a status plus advice (the identifiers it picked, or one label) marked `advisory`: it approves nothing, and your own rules still decide. Each client has its own decision budget.

### Real model provider (OpenAI, opt-in)

By default the harness composes only fakes. A single real provider is available for text-only runs; it **sends assembled prompts to OpenAI and may incur charges**, so it needs every one of these, and refuses to start if any is missing or unrecognized:

| Setting | Meaning |
|---|---|
| `AGENT_MEMORY_HARNESS_PROVIDERS=openai` | compose the real provider instead of the fakes |
| `AGENT_MEMORY_HARNESS_ALLOW_EGRESS=openai` | confirm prompts leave this machine |
| `OPENAI_API_KEY` | your key; never stored, logged or echoed |
| `AGENT_MEMORY_HARNESS_OPENAI_MODEL` | the exact model ID; none is assumed |
| `AGENT_MEMORY_HARNESS_OPENAI_PRICE` | `input,cached,output` in micro currency units per million tokens; required so spend is never misreported by a stale default |
| `AGENT_MEMORY_HARNESS_OPENAI_MAX_CLASS` | optional, `internal` (default) or `public`; a third-party cloud is never eligible for more sensitive data |
| `AGENT_MEMORY_HARNESS_OPENAI_CONTEXT_TOKENS` | optional window, default 32000 |

What is sent is one stateless request: the model, the assembled prompt (the fixed policy, the project's `CLAUDE.md` and `AGENTS.md` as pinned instructions, and the run's own goal and history, all redacted for secrets and personal data), the output cap, and `store` and `stream` turned off. No tools, files or stored conversations. Prompt caching is never assumed: a saving is counted only when the provider itself reports cached tokens for a stable prompt prefix. The adapter has been tested against a fake service that mimics the documented API; **it has not been run against the live service**. To make one tiny real call yourself, which costs a few tokens: `AGENT_MEMORY_LIVE_OPENAI=1 AGENT_MEMORY_LIVE_OPENAI_MODEL=<model> OPENAI_API_KEY=<key> go test ./internal/harnessmodel -run TestOpenAILive -v`.

### Real model provider (Anthropic Claude, opt-in)

The same opt-ins, with Anthropic names: `AGENT_MEMORY_HARNESS_PROVIDERS=anthropic`, `AGENT_MEMORY_HARNESS_ALLOW_EGRESS=anthropic`, `ANTHROPIC_API_KEY`, `AGENT_MEMORY_HARNESS_ANTHROPIC_MODEL`, `AGENT_MEMORY_HARNESS_ANTHROPIC_PRICE` (`input,cached,output`), and optionally `..._MAX_CLASS` and `..._CONTEXT_TOKENS`. It uses the official Go SDK for one stateless, non-streaming Messages request with no retries and no redirects; the key is read at call time and never stored or logged. When tools are composed in (`AGENT_MEMORY_HARNESS_TOOLS`), the offered tools are sent as Claude tools with parallel tool use disabled, and a tool call is returned only if the tool was offered. A prompt-cache marker is sent only when the run supplies a stable prefix, and cached tokens are credited only when Anthropic reports them. It has been tested against a fake service; **it has not been run against the live service**.

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
