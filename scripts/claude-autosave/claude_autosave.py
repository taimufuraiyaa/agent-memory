#!/usr/bin/env python3
"""Claude Code hook: auto-save session memory into agent-memory and sync sources.

Opt-in: enable with `python3 scripts/claude-autosave/autosave_ctl.py enable`, which
installs this file to ~/.agent-memory/hooks/ and wires it to SessionEnd, PreCompact
and Stop in ~/.claude/settings.json (or a project's .claude/settings.local.json).
- SessionEnd / PreCompact: always save.
- Stop: save only when context usage first crosses 30/50/80/100%.
"Save" = feed the not-yet-saved part of the transcript to `agent-memory session-end`
for the workspace named in the project's rule file (`workspace: <name>`), then run
study_sync.py for that workspace if it has a sync manifest. The hook detaches
immediately so Claude Code is never blocked; output goes to ~/.agent-memory/logs/autosave.log.
"""
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

HOME = Path.home()
DATA = Path(os.environ.get("AGENT_MEMORY_DATA_DIR", HOME / ".agent-memory"))
STATE = DATA / "state" / "autosave"
LOG = DATA / "logs" / "autosave.log"
THRESHOLDS = [30, 50, 80, 100]
RULE_FILES = ["CLAUDE.md", "AGENTS.md", ".cursorrules", ".aierules", ".windsurfrules",
              ".agents/rules/agent-memory.md", ".cursor/rules/agent-memory.mdc"]
MAX_CHARS = int(os.environ.get("AGENT_MEMORY_AUTOSAVE_MAX_CHARS", "60000"))
# autosave_ctl.py installs study_sync.py next to this file.
SYNC_SCRIPT = os.environ.get("AGENT_MEMORY_STUDY_SYNC", str(Path(__file__).resolve().parent / "study_sync.py"))


def log(msg):
    LOG.parent.mkdir(parents=True, exist_ok=True)
    with open(LOG, "a") as f:
        f.write(f"{time.strftime('%Y-%m-%d %H:%M:%S')} {msg}\n")


def find_binary():
    for p in (HOME / ".local/bin/agent-memory", HOME / "go/bin/agent-memory", Path("/opt/homebrew/bin/agent-memory")):
        if p.exists():
            return str(p)
    return "agent-memory"


def detect_workspace(cwd):
    d = Path(cwd).resolve()
    for _ in range(12):
        for name in RULE_FILES:
            p = d / name
            if p.is_file():
                try:
                    m = re.search(r"^workspace:\s*(\S+)\s*$", p.read_text(errors="ignore"), re.M)
                except OSError:
                    m = None
                if m:
                    return m.group(1)
        if d.parent == d:
            break
        d = d.parent
    return None


def read_transcript(path):
    lines = []
    try:
        with open(path) as f:
            lines = f.readlines()
    except OSError:
        pass
    return lines


def context_percent(lines):
    for raw in reversed(lines):
        try:
            d = json.loads(raw)
        except ValueError:
            continue
        msg = d.get("message") or {}
        u = msg.get("usage")
        if d.get("type") == "assistant" and u and not d.get("isSidechain"):
            used = sum(u.get(k) or 0 for k in ("input_tokens", "cache_read_input_tokens",
                                                "cache_creation_input_tokens", "output_tokens"))
            window = int(os.environ.get("CLAUDE_AUTOSAVE_CONTEXT_WINDOW", "0") or 0)
            if not window:
                window = 1_000_000 if ("[1m]" in (msg.get("model") or "") or used > 200_000) else 200_000
            return 100.0 * used / window
    return 0.0


def text_of(content):
    if isinstance(content, str):
        return content
    out = []
    for part in content or []:
        if isinstance(part, dict) and part.get("type") == "text":
            out.append(part.get("text", ""))
    return "\n".join(out)


def conversation_text(lines, start):
    chunks = []
    for raw in lines[start:]:
        try:
            d = json.loads(raw)
        except ValueError:
            continue
        if d.get("isSidechain") or d.get("type") not in ("user", "assistant"):
            continue
        if d.get("toolUseResult") is not None:
            continue
        t = text_of((d.get("message") or {}).get("content")).strip()
        # Drop harness-injected blocks; keep what the user and assistant actually said.
        t = re.sub(r"<system-reminder>.*?</system-reminder>", "", t, flags=re.S).strip()
        if t:
            chunks.append(f"{d['type'].upper()}: {t}")
    text = "\n\n".join(chunks)
    return text[-MAX_CHARS:]


def run_save(payload, reason):
    session = payload.get("session_id") or "unknown"
    cwd = payload.get("cwd") or os.getcwd()
    ws = detect_workspace(cwd)
    if not ws:
        log(f"{session} {reason}: no agent-memory workspace for {cwd}; skipped")
        return
    STATE.mkdir(parents=True, exist_ok=True)
    state_path = STATE / f"{session}.json"
    state = json.loads(state_path.read_text()) if state_path.exists() else {}
    lines = read_transcript(payload.get("transcript_path") or "")
    start = state.get("saved_lines", 0)
    text = conversation_text(lines, start)
    binary = find_binary()
    if len(text) >= 200:
        res = subprocess.run([binary, "session-end", "-w", ws, "--format", "json"],
                             input=text, capture_output=True, text=True, timeout=600)
        log(f"{session} {reason} ws={ws} chars={len(text)} rc={res.returncode} out={res.stdout.strip()[:300]} err={res.stderr.strip()[:200]}")
        if res.returncode == 0:
            state["saved_lines"] = len(lines)
    else:
        log(f"{session} {reason} ws={ws}: nothing new to save")
    state_path.write_text(json.dumps(state))

    manifest = DATA / "state" / f"{ws}-study-manifest.json"
    if manifest.exists() and Path(SYNC_SCRIPT).exists():
        lock = DATA / "state" / f"{ws}-study-sync.lock"
        if lock.exists() and time.time() - lock.stat().st_mtime < 3600:
            log(f"{session} {reason} ws={ws}: study sync already running")
            return
        lock.write_text(str(os.getpid()))
        try:
            sources = json.loads(manifest.read_text()).get("sources", [])
            res = subprocess.run([sys.executable, SYNC_SCRIPT, "--binary", binary, "-w", ws, *sources],
                                 capture_output=True, text=True, timeout=3600)
            log(f"{session} {reason} ws={ws} study_sync rc={res.returncode} {res.stdout.strip().splitlines()[:2]}")
        finally:
            lock.unlink(missing_ok=True)


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "--worker":
        payload = json.loads(Path(sys.argv[2]).read_text())
        Path(sys.argv[2]).unlink(missing_ok=True)
        run_save(payload, sys.argv[3])
        return

    try:
        payload = json.load(sys.stdin)
    except ValueError:
        return
    event = payload.get("hook_event_name", "")
    session = payload.get("session_id") or "unknown"
    reason = None
    if event in ("SessionEnd", "PreCompact"):
        reason = event
    elif event == "Stop":
        if payload.get("stop_hook_active"):
            return
        pct = context_percent(read_transcript(payload.get("transcript_path") or ""))
        STATE.mkdir(parents=True, exist_ok=True)
        tpath = STATE / f"{session}.thresholds.json"
        fired = set(json.loads(tpath.read_text())) if tpath.exists() else set()
        crossed = [t for t in THRESHOLDS if pct >= t and t not in fired]
        if crossed:
            fired.update(crossed)
            tpath.write_text(json.dumps(sorted(fired)))
            reason = f"context-{max(crossed)}%"
    if not reason:
        return

    # Detach so the hook returns immediately (SessionEnd/Stop must not wait on embedding + sync).
    STATE.mkdir(parents=True, exist_ok=True)
    job = STATE / f"job-{session}-{int(time.time() * 1000)}.json"
    job.write_text(json.dumps(payload))
    LOG.parent.mkdir(parents=True, exist_ok=True)
    subprocess.Popen([sys.executable, __file__, "--worker", str(job), reason],
                     stdin=subprocess.DEVNULL, stdout=open(LOG, "a"), stderr=subprocess.STDOUT,
                     start_new_session=True, env={**os.environ})


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # never break the Claude session
        log(f"hook error: {e!r}")
