#!/usr/bin/env bash
set -euo pipefail

BENCHMARK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_ROOT="${BENCHMARK_DIR}/results"

RUN_ID=""
REFRESH_SECONDS="5"
TOTAL_CASES=""
ONCE="0"

usage() {
  cat <<EOF
Usage: $(basename "$0") <run-id> [options]

Options:
  --refresh <seconds>   Refresh interval in seconds (default: 5)
  --total <cases>       Override total cases per phase
  --once                Print one snapshot and exit
  --help                Show this help text

Examples:
  rtk bash benchmark/watch_progress.sh continuation-full-10000
  rtk bash benchmark/watch_progress.sh continuation-full-10000 --refresh 2
  rtk bash benchmark/watch_progress.sh continuation-full-10000 --once
EOF
}

if [[ $# -lt 1 ]]; then
  usage >&2
  exit 2
fi

RUN_ID="$1"
shift

while [[ $# -gt 0 ]]; do
  case "$1" in
    --refresh)
      REFRESH_SECONDS="$2"
      shift 2
      ;;
    --total)
      TOTAL_CASES="$2"
      shift 2
      ;;
    --once)
      ONCE="1"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

RUN_DIR="${RESULTS_ROOT}/${RUN_ID}"
DB_PATH="${RUN_DIR}/benchmark.db"
MANIFEST_PATH="${RUN_DIR}/run_manifest.json"

if [[ ! -d "${RUN_DIR}" ]]; then
  echo "run directory not found: ${RUN_DIR}" >&2
  exit 1
fi

if [[ ! -f "${DB_PATH}" ]]; then
  echo "benchmark database not found: ${DB_PATH}" >&2
  exit 1
fi

if [[ -z "${TOTAL_CASES}" && -f "${MANIFEST_PATH}" ]]; then
  TOTAL_CASES="$(python3 - <<'PY' "${MANIFEST_PATH}"
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
print(int(data.get("executed_case_count_per_phase") or 0))
PY
)"
fi

if [[ -z "${TOTAL_CASES}" || "${TOTAL_CASES}" == "0" ]]; then
  TOTAL_CASES="10000"
fi

python3 - <<'PY' "${DB_PATH}" "${RUN_DIR}" "${RUN_ID}" "${TOTAL_CASES}" "${REFRESH_SECONDS}" "${ONCE}"
import json
import os
import sqlite3
import sys
import time
from pathlib import Path

db_path = Path(sys.argv[1])
run_dir = Path(sys.argv[2])
run_id = sys.argv[3]
total_cases = max(1, int(sys.argv[4]))
refresh_seconds = max(1.0, float(sys.argv[5]))
once = sys.argv[6] == "1"


def count_rows(conn: sqlite3.Connection, run_label: str, memory_enabled: int) -> int:
    row = conn.execute(
        """
        SELECT COUNT(*)
        FROM token_metrics
        WHERE run_label = ? AND memory_enabled = ?
        """,
        (run_label, memory_enabled),
    ).fetchone()
    return int(row[0] if row else 0)


def render_snapshot() -> str:
    conn = sqlite3.connect(str(db_path))
    try:
        on_count = count_rows(conn, "benchmark-on", 1)
        off_count = count_rows(conn, "benchmark-off", 0)
        seed_count = int(conn.execute("SELECT COUNT(*) FROM memories").fetchone()[0])
    finally:
        conn.close()

    run_manifest_exists = (run_dir / "run_manifest.json").is_file()
    score_report_exists = (run_dir / "score_report.json").is_file()
    phase = "seeding"
    if off_count > 0:
        phase = "off"
    elif on_count > 0:
        phase = "on"

    lines = [
        f"run_id: {run_id}",
        f"run_dir: {run_dir}",
        f"phase: {phase}",
        f"seeded_memories: {seed_count}",
        f"on:  {on_count}/{total_cases} ({on_count / total_cases * 100:.1f}%)",
        f"off: {off_count}/{total_cases} ({off_count / total_cases * 100:.1f}%)",
        f"run_manifest: {'yes' if run_manifest_exists else 'no'}",
        f"score_report: {'yes' if score_report_exists else 'no'}",
        f"last_updated: {time.strftime('%Y-%m-%d %H:%M:%S')}",
    ]
    if run_manifest_exists and off_count >= total_cases:
        lines.append("status: benchmark runner finished")
    elif score_report_exists:
        lines.append("status: scored")
    else:
        lines.append("status: running")
    return "\n".join(lines)


while True:
    if not once:
        print("\033[2J\033[H", end="")
    print(render_snapshot(), flush=True)
    if once:
        break
    time.sleep(refresh_seconds)
PY
