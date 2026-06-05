#!/usr/bin/env python3
"""Remove stale benchmark raw artifacts and persisted benchmark history."""

from __future__ import annotations

import argparse
import json
import shutil
import sqlite3
from pathlib import Path


ROOT = Path(__file__).resolve().parent
DEFAULT_RESULTS_DIR = ROOT / "results"
DEFAULT_DBS = [
    Path.home() / ".agent-memory" / "benchmark-toggle-comparison.db",
    Path.home() / ".agent-memory" / "agent-memory.db",
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--results-dir",
        type=Path,
        default=DEFAULT_RESULTS_DIR,
        help="Benchmark results directory whose run subdirectories should be removed.",
    )
    parser.add_argument(
        "--db",
        action="append",
        type=Path,
        default=[],
        help="SQLite DB whose benchmark_runs rows should be deleted. May be passed multiple times.",
    )
    return parser.parse_args()


def delete_results_dir(results_dir: Path) -> list[str]:
    removed: list[str] = []
    if not results_dir.exists():
        return removed
    for child in results_dir.iterdir():
        if child.name == ".gitkeep":
            continue
        if child.is_dir():
            shutil.rmtree(child)
            removed.append(str(child))
    return sorted(removed)


def clear_benchmark_runs(db_path: Path) -> dict[str, object]:
    if not db_path.exists():
        return {"db_path": str(db_path), "exists": False, "deleted_rows": 0}
    conn = sqlite3.connect(str(db_path))
    try:
        try:
            before = conn.execute("SELECT COUNT(*) FROM benchmark_runs").fetchone()[0]
        except sqlite3.OperationalError:
            return {"db_path": str(db_path), "exists": True, "deleted_rows": 0, "table_present": False}
        conn.execute("DELETE FROM benchmark_runs")
        conn.commit()
        return {"db_path": str(db_path), "exists": True, "deleted_rows": int(before), "table_present": True}
    finally:
        conn.close()


def main() -> None:
    args = parse_args()
    dbs = args.db or DEFAULT_DBS
    removed_dirs = delete_results_dir(args.results_dir)
    db_results = [clear_benchmark_runs(path) for path in dbs]
    print(
        json.dumps(
            {
                "results_dir": str(args.results_dir),
                "removed_run_dirs": removed_dirs,
                "removed_run_dir_count": len(removed_dirs),
                "db_cleanup": db_results,
            },
            indent=2,
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
