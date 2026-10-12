---
name: incremental-study-sync
description: Keep an agent-memory workspace up to date with source folders that change often (git repos switching branches, vaults, docs). Studies only new or changed files, paced under the 100 writes/minute limit, and deletes memories of changed or removed files. Use when the user says sources changed, there are many commits, memory is stale, or wants to re-sync/update a studied workspace.
---

# Incremental study sync

`agent-memory study` has no update semantics. Re-studying a modified file creates a new memory and leaves the old version behind. It also silently stops writing after 100 writes per minute per workspace (`MaxWritesPerMinute`, `internal/engine/security.go:52`), and `--source .` scans nothing (the root is treated as a hidden dir). `scripts/study_sync.py` works around all three.

## How it works

1. It hashes every study-eligible file under the given sources. The eligible extensions match `isStudyFile`: `.md .txt .go .ts .tsx .js .json .yaml .yml`. Like study, it skips hidden dirs, `node_modules` and `bin`.
2. It diffs the hashes against `$AGENT_MEMORY_DATA_DIR/state/<workspace>-study-manifest.json`, giving added, changed and removed files.
3. It studies added and changed files with absolute `--source` paths, in batches of 90 with a 65 s pause between batches.
4. It looks up the old memory IDs by `source_json.file_path` (read-only SQLite on the DB path from `agent-memory list`) and deletes them through `POST /api/v1/memories/delete`. This path is audited. IDs that the fresh study re-produced through content dedup are kept.
5. It writes the new manifest. If the API is down, the hashes of the stale files are kept so the next run retries the deletions.

The first run with no manifest only records a baseline, because the workspace is assumed to be already studied. Pass `--initial-study` to study everything on the first run.

## Usage

The `agent-memory serve` service must be running, because deletions go through its API (default `http://127.0.0.1:3211`).

```bash
python3 .agents/skills/incremental-study-sync/scripts/study_sync.py -w oms \
  "/Users/dainam.banh/Desktop/aqxoms/old oms/Sources" \
  /Users/dainam.banh/Desktop/aqxoms/ai-domain-knowledge --dry-run
```

Drop `--dry-run` to apply. Useful flags:
- `--binary ~/go/bin/agent-memory` when the CLI is not on PATH (for example in a non-login shell).
- `--no-delete` to only add new or changed files.
- `--api URL` to target another service port, such as the dashboard API on 3100.
- `--batch` and `--pause` to tune pacing.

The `oms` workspace baseline (1,052 files) was recorded on 2026-10-12.

## Automatic trigger (instead of a schedule)

This is opt-in through the `claude-session-autosave` skill (`python3 scripts/claude-autosave/autosave_ctl.py enable`). Once enabled, `~/.agent-memory/hooks/claude_autosave.py` is wired to `Stop`, `PreCompact` and `SessionEnd`.
- **SessionEnd and PreCompact** always fire. **Stop** fires only when context usage first crosses 30, 50, 80 or 100% for that session. The context window is 200k, switching to 1M when the model has `[1m]` or usage exceeds 200k. Override it with `CLAUDE_AUTOSAVE_CONTEXT_WINDOW`.
- **What a save does:**
  1. Detects the workspace from `workspace: <name>` in the project's rule files.
  2. Pipes the not-yet-saved user and assistant text (up to 60k characters) to `agent-memory session-end -w <ws>`.
  3. If `~/.agent-memory/state/<ws>-study-manifest.json` exists, runs this script on the manifest's sources, guarded by a lock.
- **Non-blocking:** the hook detaches a worker and returns immediately.
- **State and logs:** state is in `~/.agent-memory/state/autosave/`. The log is at `~/.agent-memory/logs/autosave.log`.
- **Validated 2026-10-12:**
  - a Stop event at about 40% context saved 6 memories;
  - PreCompact on `oms` synced 50 changed and 20 removed files.

## Validation (2026-10-12)

The script was tested end-to-end on a throwaway workspace:
- initial study: 2 files gave 2 memories;
- one file edited and one deleted: 1 new memory and 2 stale memories deleted, leaving exactly 1 memory with the new content;
- re-run: no-op.

## Gotchas

- Files studied before the manifest existed are not tracked until they change.
- Removing a whole source folder from the command line does not delete its memories, because only paths under the given sources are considered "removed".
- `.java`, `.sql`, `.xml` and `.proto` are never studied, because of the extension whitelist in `internal/engine/study.go`.
