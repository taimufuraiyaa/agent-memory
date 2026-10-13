---
name: claude-session-autosave
description: Opt-in Claude Code hooks that automatically save session knowledge into agent-memory (session-end) and re-sync studied sources (study_sync) when a session ends, before compaction, and when context usage crosses 30/50/80/100%. Use when the user wants agent-memory to save automatically, asks to enable/disable/check autosave, or wonders why memory was not saved at session end or compaction.
---

# Claude session autosave (opt-in)

This feature is off by default. A user enables it per machine (user scope) or per project.

## Enable, disable, status

Run these from the agent-memory repo root:

```bash
python3 scripts/claude-autosave/autosave_ctl.py enable                       # all projects (~/.claude/settings.json)
python3 scripts/claude-autosave/autosave_ctl.py enable --scope project --project /path/to/project   # one project (.claude/settings.local.json)
python3 scripts/claude-autosave/autosave_ctl.py status
python3 scripts/claude-autosave/autosave_ctl.py disable [--scope project --project DIR]
```

`enable` does three things:
- copies `scripts/claude-autosave/claude_autosave.py` and `.agents/skills/incremental-study-sync/scripts/study_sync.py` into `~/.agent-memory/hooks/`;
- adds `Stop`, `PreCompact` and `SessionEnd` hook entries tagged `# agent-memory autosave`;
- is idempotent, and re-running it refreshes the installed scripts.

`disable` removes only the tagged entries and leaves other hooks alone. After either command, start a new Claude Code session so the settings reload.

## Prerequisites

- The `agent-memory` CLI is installed (`~/.local/bin` or `~/go/bin`).
- The project is registered (`agent-memory init`) and its `CLAUDE.md` or `AGENTS.md` contains `workspace: <name>`. If not, add the section with `agent-memory reinstall --ide claude -n <name>`. Projects without a workspace line are skipped.
- Optional source sync needs a manifest at `~/.agent-memory/state/<ws>-study-manifest.json`. You create it by running `study_sync.py` once (see the `incremental-study-sync` skill).
- Optional stale-memory deletion during sync needs `agent-memory serve` running on 3211.

## Behaviour

| Event | Saves when |
|---|---|
| `SessionEnd` | always |
| `PreCompact` (manual or auto) | always |
| `Stop` | context usage first crosses 30, 50, 80 or 100% in that session; each threshold fires once |

Each save does the following:
1. It pipes the user and assistant text that has not been saved yet (tool results and `<system-reminder>` blocks dropped, last 60k characters) to `agent-memory session-end -w <ws>`.
2. It runs `study_sync.py` for that workspace if a manifest exists. A lock file prevents overlapping syncs.
3. The hook detaches a worker and returns at once, so the session is never blocked.

Context percentage comes from the last main-thread assistant `usage` in the transcript (input + cache read + cache creation + output). The window is assumed to be 200k, switching to 1M if the model id has `[1m]` or usage exceeds 200k. Set `CLAUDE_AUTOSAVE_CONTEXT_WINDOW` (in settings `env`) for an exact value.

## Files and debugging

- Log: `~/.agent-memory/logs/autosave.log`. It has one line per save with the workspace, character count, `session-end` result and sync summary.
- Per-session state: `~/.agent-memory/state/autosave/<session>.json` (lines already saved) and `<session>.thresholds.json` (thresholds already fired).
- Manual test:
  `echo '{"hook_event_name":"PreCompact","session_id":"t","transcript_path":"<jsonl>","cwd":"<project>"}' | python3 ~/.agent-memory/hooks/claude_autosave.py`, then read the log.
- Environment overrides:
  - `AGENT_MEMORY_AUTOSAVE_MAX_CHARS`
  - `AGENT_MEMORY_STUDY_SYNC` (path to `study_sync.py`)
  - `AGENT_MEMORY_DATA_DIR`

## Validation (2026-10-12)

- A pipe-tested `Stop` event at about 40% context saved 6 memories to workspace `agent-memory`.
- `PreCompact` on `oms` synced 50 changed and 20 removed source files.
- A project with no workspace line was a no-op.
- In a fake HOME, `enable` and `disable` were idempotent and preserved unrelated hooks; project scope wrote `.claude/settings.local.json`.
