#!/usr/bin/env python3
"""Enable, disable or inspect the opt-in Claude Code autosave hooks for agent-memory.

  python3 scripts/claude-autosave/autosave_ctl.py enable  [--scope user|project] [--project DIR]
  python3 scripts/claude-autosave/autosave_ctl.py disable [--scope user|project] [--project DIR]
  python3 scripts/claude-autosave/autosave_ctl.py status

enable copies claude_autosave.py and study_sync.py into ~/.agent-memory/hooks/ and adds
Stop, PreCompact and SessionEnd hooks to ~/.claude/settings.json (scope user, default) or
<project>/.claude/settings.local.json (scope project). Entries are tagged so disable removes
only what enable added; other hooks are left untouched.
"""
import argparse
import json
import os
import shutil
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
HOOK_SRC = Path(__file__).resolve().parent / "claude_autosave.py"
SYNC_SRC = REPO / ".agents/skills/incremental-study-sync/scripts/study_sync.py"
DATA = Path(os.environ.get("AGENT_MEMORY_DATA_DIR", Path.home() / ".agent-memory"))
HOOK_DIR = DATA / "hooks"
EVENTS = ("Stop", "PreCompact", "SessionEnd")
MARKER = "# agent-memory autosave"


def settings_path(scope, project):
    if scope == "user":
        return Path.home() / ".claude" / "settings.json"
    return Path(project).resolve() / ".claude" / "settings.local.json"


def load(path):
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text())
    except ValueError as e:
        sys.exit(f"{path} is not valid JSON ({e}); fix it before enabling autosave")


def save(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".autosave.tmp")
    tmp.write_text(json.dumps(data, indent=2) + "\n")
    os.replace(tmp, path)


def is_ours(group):
    return any(MARKER in h.get("command", "") or "claude_autosave.py" in h.get("command", "")
               for h in group.get("hooks", []))


def strip(data):
    hooks = data.get("hooks", {})
    removed = 0
    for event in EVENTS:
        groups = hooks.get(event, [])
        kept = [g for g in groups if not is_ours(g)]
        removed += len(groups) - len(kept)
        if kept:
            hooks[event] = kept
        else:
            hooks.pop(event, None)
    if not hooks:
        data.pop("hooks", None)
    return removed


def enable(path):
    HOOK_DIR.mkdir(parents=True, exist_ok=True)
    shutil.copy2(HOOK_SRC, HOOK_DIR / "claude_autosave.py")
    if SYNC_SRC.exists():
        shutil.copy2(SYNC_SRC, HOOK_DIR / "study_sync.py")
    os.chmod(HOOK_DIR / "claude_autosave.py", 0o755)
    data = load(path)
    strip(data)
    command = f'python3 "$HOME/.agent-memory/hooks/claude_autosave.py" {MARKER}'
    hooks = data.setdefault("hooks", {})
    for event in EVENTS:
        hooks.setdefault(event, []).append({"hooks": [{"type": "command", "command": command, "timeout": 10}]})
    save(path, data)
    print(f"enabled: {', '.join(EVENTS)} in {path}")
    print(f"installed: {HOOK_DIR / 'claude_autosave.py'}" + (f", {HOOK_DIR / 'study_sync.py'}" if SYNC_SRC.exists() else ""))
    print("Start a new Claude Code session to load the hooks. Projects need `workspace: <name>` in CLAUDE.md/AGENTS.md (agent-memory init).")


def disable(path):
    data = load(path)
    removed = strip(data)
    save(path, data)
    print(f"disabled: removed {removed} autosave hook group(s) from {path}")


def status(project):
    for scope in ("user", "project"):
        path = settings_path(scope, project)
        data = load(path)
        on = [e for e in EVENTS if any(is_ours(g) for g in data.get("hooks", {}).get(e, []))]
        print(f"{scope:8} {path}: {'enabled for ' + ', '.join(on) if on else 'disabled'}")
    installed = HOOK_DIR / "claude_autosave.py"
    print(f"hook script: {installed if installed.exists() else 'not installed'}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("action", choices=["enable", "disable", "status"])
    ap.add_argument("--scope", choices=["user", "project"], default="user")
    ap.add_argument("--project", default=os.getcwd(), help="project root for --scope project (default: cwd)")
    a = ap.parse_args()
    if a.action == "status":
        status(a.project)
    elif a.action == "enable":
        enable(settings_path(a.scope, a.project))
    else:
        disable(settings_path(a.scope, a.project))


if __name__ == "__main__":
    main()
