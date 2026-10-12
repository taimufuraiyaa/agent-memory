#!/usr/bin/env python3
"""Verify the real terminal lifecycle and a guarded note write in an isolated DB."""

import argparse
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import sqlite3
import struct
import subprocess
import tempfile
import termios
import time

from terminal_screen import TerminalScreen


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--model-dir", required=True)
    parser.add_argument("--runtime-lib")
    parser.add_argument("--command", choices=["tui", "start", "restart"], default="tui")
    parser.add_argument("--pick-workspace", action="store_true")
    parser.add_argument("--source-dir", help="Required for the isolated restart rebuild")
    args = parser.parse_args()
    if args.pick_workspace and args.command == "tui":
        parser.error("the tui command requires an explicit workspace; use start or restart for the picker")
    if args.command == "restart" and not args.source_dir:
        parser.error("restart verification requires --source-dir")
    binary = str(Path(args.binary).resolve())
    model = str(Path(args.model_dir).resolve())
    with tempfile.TemporaryDirectory(prefix="agent-memory-terminal-smoke-") as temporary:
        root = Path(temporary)
        home = root / "home"
        home.mkdir()
        db = root / "isolated.db"
        if args.command == "restart":
            install = root / "bin"
            install.mkdir()
            shutil.copy2(binary, install / "agent-memory")
            shutil.copy2(binary, install / "am")
            binary = str(install / "agent-memory")
        environment = dict(os.environ)
        environment["HOME"] = str(home)
        environment["AGENT_MEMORY_DATA_DIR"] = str(home / ".agent-memory")
        environment["AGENT_MEMORY_ENABLED"] = "1"
        environment["TERM"] = "xterm-256color"
        environment.pop("MEMORY_API_URL", None)
        for key in list(environment):
            if key.startswith("AGENT_MEMORY_HARNESS_"):
                environment.pop(key)
        if args.runtime_lib:
            environment["AGENT_MEMORY_ONNX_RUNTIME_PATH"] = str(Path(args.runtime_lib).resolve())
        common = ["--workspace", "agent-memory", "--db", str(db), "--model-dir", model]
        seed = subprocess.run(
            [binary, "write", *common, "--type", "semantic", "--content", "Terminal smoke preserves isolated workspace data."],
            env=environment, cwd=root, capture_output=True, text=True, timeout=60,
        )
        if seed.returncode != 0:
            raise RuntimeError("Cannot seed the isolated database: " + seed.stderr[-1000:])
        empty_db = root / "empty.db"
        empty = subprocess.run([binary, "stats", "--workspace", "empty", "--db", str(empty_db), "--model-dir", model], env=environment, cwd=root, capture_output=True, timeout=30)
        if empty.returncode != 0:
            raise RuntimeError("Cannot initialize the empty test project: " + empty.stderr.decode(errors="replace")[-500:])
        data_dir = home / ".agent-memory"
        data_dir.mkdir(exist_ok=True)
        (data_dir / "workspaces.json").write_text(json.dumps({"projects": [{"name": "agent-memory", "db_path": str(db), "workspace_root": str(root)}, {"name": "empty", "db_path": str(empty_db), "workspace_root": str(root)}]}))
        launch_common = common[4:] if args.pick_workspace else common
        launch_args = [binary, args.command, *launch_common]
        if args.command == "restart":
            launch_args += ["--source-dir", str(Path(args.source_dir).resolve())]
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
        process = subprocess.Popen(
            launch_args, stdin=slave, stdout=slave, stderr=slave,
            env=environment, cwd=root, start_new_session=True,
        )
        os.close(slave)
        transcript = bytearray()
        screen = TerminalScreen(100, 30)

        def read_until(needle, timeout=30, transcript_only=False):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                observed = needle.encode() in transcript if transcript_only else needle in screen.text
                if observed:
                    return
                if process.poll() is not None:
                    raise RuntimeError("Terminal exited early: " + transcript[-1500:].decode(errors="replace"))
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        chunk = os.read(master, 65536)
                        transcript.extend(chunk)
                        screen.feed(chunk)
                    except OSError:
                        break
            raise RuntimeError("Terminal did not render " + repr(needle) + ":\n" + screen.text)

        def send(data):
            os.write(master, data)

        try:
            if args.pick_workspace:
                read_until("Choose a registered workspace")
                read_until("agent-memory · 1 memory")
                read_until("empty · 0 memories")
                send(b"\r")
            if args.command == "restart":
                read_until("Both binaries updated", timeout=120, transcript_only=True)
            read_until("Workspace overview")
            if b"WORKSPACE" not in transcript:
                raise RuntimeError("The wide-terminal workspace navigation is missing")
            send(b"\x1b[B\x1b[A\x1b[C")  # Home actions, then the right workspace panel.
            read_until("Workspace panel")
            send(b"\x1b[B\x1b[A\x1b[D")
            read_until("Start ")
            send(b"3")
            read_until("Recent memories")
            read_until("Terminal smoke")
            send(b"\x1b[C")
            read_until("Reading ")
            send(b"\x1b[B\x1b[A\x1b[C")
            read_until("Workspace panel")
            send(b"\x1b[D")
            read_until("Reading ")
            send(b"\x1b[D")
            read_until("Records ")
            send(b"\x17")  # Ctrl-W shows all registered projects and counts.
            read_until("agent-memory · current")
            read_until("1 memory")
            send(b"\x1b[B\r")
            read_until("Overview  · Project: empty")
            send(b"3")
            read_until("0 saved memories")
            read_until("All projects")
            send(b"w")
            read_until("empty · current")
            send(b"\r")
            read_until("Overview  · Project: agent-memory")
            send(b"/Terminal smoke\r")
            read_until("Search: Terminal smoke")
            read_until("Relevance ")
            send(b"\x1b[C")
            read_until("Reading ")
            send(b"\x1b[D")
            read_until("Records ")
            send(b"5")
            read_until("Question *")
            send(b"Terminal smoke preserves isolated workspace data.\r")
            read_until("Grounded context")
            send(b"\x0b")  # Ctrl-K destinations.
            read_until("Workspace destinations")
            send(b"\x1b[200~notes\x1b[201~\r")
            read_until("Active notes")
            send(b"\x10")  # Ctrl-P commands.
            read_until("Command palette")
            send(b"\x1b[200~create a note\x1b[201~\r")
            read_until("Title *")
            send(b"Smoke note\t")
            send(b"\x1b[200~Draft body\x1b[201~\x13")
            read_until("Confirm Create a note")
            send(b"n")
            read_until("Title *")
            with sqlite3.connect(db) as connection:
                cancelled_count = connection.execute("SELECT COUNT(*) FROM notes WHERE workspace = ? AND title = ?", ("agent-memory", "Smoke note")).fetchone()[0]
            if cancelled_count != 0:
                raise RuntimeError("Cancelling review wrote a note")
            send(b"\x13")
            read_until("Confirm Create a note")
            send(b"y")
            read_until("Create a note completed")
            send(b"e")
            read_until("Title *")
            send(b"\t\x1b[F\x1b[200~\n\nUpdated in the terminal.\x1b[201~\x13")
            read_until("Confirm Edit a note")
            send(b"y")
            read_until("Edit a note completed")
            send(b"\x1b[D")
            time.sleep(0.1)
            send(b"f\x1b[200~smoke\x1b[201~")
            read_until("Filter: smoke")
            send(b"\r\x1b[C")
            read_until("Esc List")
            send(b"\x1b[C")
            read_until("Workspace panel")
            send(b"\x1b[D")
            read_until("Esc List")
            send(b"\x1b[D")
            read_until("Records ")
            send(b"q")
            deadline = time.monotonic() + 10
            while process.poll() is None and time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        screen.feed(os.read(master, 65536))
                    except OSError:
                        break
            result = process.wait(timeout=1)
            if result != 0:
                raise RuntimeError("Terminal exited with status " + str(result))
            with sqlite3.connect(db) as connection:
                tables = [row[0] for row in connection.execute("SELECT name FROM sqlite_master WHERE type='table'")]
                note_table = next(name for name in tables if name in ("notes", "note_documents"))
                count = connection.execute(
                    "SELECT COUNT(*) FROM " + note_table + " WHERE workspace = ? AND title = ?",
                    ("agent-memory", "Smoke note"),
                ).fetchone()[0]
            if count != 1:
                with sqlite3.connect(db) as connection:
                    observed = connection.execute("SELECT workspace, title, path FROM " + note_table).fetchall()
                raise RuntimeError("Cancellation/confirmation did not produce exactly one note: " + repr(observed))
            with sqlite3.connect(db) as connection:
                body, revision = connection.execute("SELECT body, revision FROM " + note_table + " WHERE workspace = ? AND title = ?", ("agent-memory", "Smoke note")).fetchone()
            if not body.startswith("# Smoke note\n") or "Updated in the terminal." not in body or revision != 2:
                raise RuntimeError("The title/body/revision edit workflow did not persist correctly")
            if args.command == "restart" and (install / "agent-memory").read_bytes() != (install / "am").read_bytes():
                raise RuntimeError("restart did not synchronize both installed binaries")
            print(json.dumps({"ok": True, "command": args.command, "workspace_picker": args.pick_workspace, "checks": ["project counts and switching", "empty project recovery and search", "visible right workspace panel", "four-direction Home/list/read/panel navigation", "direct grounded Ask", "filtered destinations", "filtered command palette", "primary note form", "cancel without write", "confirm write", "arrow filter/read/edit", "one note with revision 2", "clean terminal exit"]}))
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
            os.close(master)


if __name__ == "__main__":
    main()
