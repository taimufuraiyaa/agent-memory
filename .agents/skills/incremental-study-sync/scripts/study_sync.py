#!/usr/bin/env python3
"""Incrementally keep an agent-memory workspace in sync with source folders.

Each run hashes every study-eligible file under the given sources, compares
with the manifest from the previous run, studies only new/changed files
(paced under the 100 writes/minute security limit) and deletes memories whose
source file changed or disappeared (through the local API, so deletions are
audited). The first run with no manifest only records hashes unless
--initial-study is passed, because the workspace is usually already studied.
"""
import argparse
import hashlib
import json
import os
import sqlite3
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

# Mirrors isStudyFile in internal/engine/study.go.
STUDY_EXTS = {".md", ".txt", ".go", ".ts", ".tsx", ".js", ".json", ".yaml", ".yml"}
DATA_DIR = Path(os.environ.get("AGENT_MEMORY_DATA_DIR", Path.home() / ".agent-memory"))


def eligible_files(sources):
    for src in sources:
        for root, dirs, files in os.walk(src):
            # Same pruning as shouldIgnoreStudyPath: hidden dirs, node_modules, bin.
            dirs[:] = [d for d in dirs if not d.startswith(".") and d not in ("node_modules", "bin")]
            for name in files:
                if Path(name).suffix.lower() in STUDY_EXTS:
                    yield os.path.join(root, name)


def sha1(path):
    h = hashlib.sha1()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def memory_ids_for(db_path, paths):
    if not paths:
        return {}
    out = {}
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        for p in paths:
            rows = con.execute(
                "select id from memories where json_extract(source_json,'$.file_path') = ?", (p,)
            ).fetchall()
            out[p] = [r[0] for r in rows]
    finally:
        con.close()
    return out


def api_delete(api, workspace, ids):
    body = json.dumps({"workspace": workspace, "memory_ids": ids}).encode()
    req = urllib.request.Request(f"{api}/api/v1/memories/delete", data=body,
                                 headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def api_alive(api):
    try:
        with urllib.request.urlopen(f"{api}/health", timeout=3):
            return True
    except Exception:
        return False


def workspace_db_path(binary, workspace):
    # The registry is authoritative; the CLI does not always honour AGENT_MEMORY_DATA_DIR for DB paths.
    res = subprocess.run([binary, "list"], capture_output=True, text=True, check=True)
    for project in json.loads(res.stdout)["data"]["projects"]:
        if project["name"] == workspace:
            return project["db_path"]
    sys.exit(f"workspace {workspace!r} is not registered; run `agent-memory init` first")


def study(binary, workspace, files):
    args = [binary, "study", "-w", workspace, "--max-files", "100000"]
    for f in files:
        args += ["--source", f]
    res = subprocess.run(args, capture_output=True, text=True)
    try:
        data = json.loads(res.stdout)["data"]
    except Exception:
        print(res.stdout[-500:], res.stderr[-500:], file=sys.stderr)
        raise
    return data


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-w", "--workspace", required=True)
    ap.add_argument("sources", nargs="+")
    ap.add_argument("--api", default=os.environ.get("AGENT_MEMORY_URL", "http://127.0.0.1:3211"))
    ap.add_argument("--binary", default="agent-memory")
    ap.add_argument("--batch", type=int, default=90)
    ap.add_argument("--pause", type=int, default=65)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--initial-study", action="store_true",
                    help="with no manifest, study everything instead of only recording hashes")
    ap.add_argument("--no-delete", action="store_true")
    a = ap.parse_args()

    sources = [os.path.abspath(s) for s in a.sources]
    state_dir = DATA_DIR / "state"
    state_dir.mkdir(parents=True, exist_ok=True)
    manifest_path = state_dir / f"{a.workspace}-study-manifest.json"
    db_path = workspace_db_path(a.binary, a.workspace)

    old = {}
    if manifest_path.exists():
        old = json.loads(manifest_path.read_text()).get("files", {})
    first_run = not manifest_path.exists()

    current = {}
    for p in eligible_files(sources):
        try:
            current[p] = sha1(p)
        except OSError as e:
            print(f"skip {p}: {e}", file=sys.stderr)

    in_scope = lambda p: any(p == s or p.startswith(s + os.sep) for s in sources)
    added = sorted(p for p in current if p not in old)
    changed = sorted(p for p in current if p in old and old[p] != current[p])
    removed = sorted(p for p in old if p not in current and in_scope(p))

    if first_run and not a.initial_study:
        to_study = []
        print(f"first run: recording {len(current)} file hashes as baseline (use --initial-study to study them)")
    else:
        to_study = added + changed
    print(f"added={len(added)} changed={len(changed)} removed={len(removed)} to_study={len(to_study)}")

    stale = {} if a.no_delete else memory_ids_for(db_path, changed + removed)
    if a.dry_run:
        for label, items in (("study", to_study), ("removed", removed)):
            for p in items[:20]:
                print(f"  {label}: {p}")
        print(f"  stale memories to delete: {sum(len(v) for v in stale.values())}")
        return

    new_ids = set()
    for i in range(0, len(to_study), a.batch):
        chunk = to_study[i:i + a.batch]
        data = study(a.binary, a.workspace, chunk)
        ids = data.get("written_ids") or []
        new_ids.update(ids)
        print(f"batch {i // a.batch + 1}: scanned={data.get('scanned_files')} written={len(ids)} "
              f"errors={len(data.get('errors') or [])}", flush=True)
        if i + a.batch < len(to_study):
            time.sleep(a.pause)

    # Deleting stale memories: keep any ID that the fresh study re-produced
    # (unchanged content dedups to the same memory).
    to_delete = sorted({mid for ids in stale.values() for mid in ids} - new_ids)
    if to_delete:
        if api_alive(a.api):
            for i in range(0, len(to_delete), 200):
                api_delete(a.api, a.workspace, to_delete[i:i + 200])
            print(f"deleted {len(to_delete)} stale memories")
        else:
            print(f"WARNING: {a.api} not reachable; {len(to_delete)} stale memories kept. Start `agent-memory serve` and re-run.")
            # Keep the old hashes for changed/removed files so the next run retries the deletion.
            for p in changed:
                current[p] = old[p]
            for p in removed:
                current[p] = old[p]

    manifest_path.write_text(json.dumps({"workspace": a.workspace, "sources": sources,
                                         "updated_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
                                         "files": current}, indent=0))
    print(f"manifest: {manifest_path}")


if __name__ == "__main__":
    main()
