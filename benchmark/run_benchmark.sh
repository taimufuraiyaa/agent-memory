#!/usr/bin/env bash
set -euo pipefail

BENCHMARK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${BENCHMARK_DIR}/.." && pwd)"

RUN_ID="$(date -u +"%Y%m%dT%H%M%SZ")"
RESULTS_ROOT="${BENCHMARK_DIR}/results"
RUN_DIR=""
DB_PATH=""
WORKSPACE="benchmark-toggle-comparison"
BINARY_PATH="${BENCHMARK_DIR}/bin/agent-memory-benchmark"
MODEL_DIR=""
SEED_FILE="${BENCHMARK_DIR}/testdata/prior_session_fixtures.jsonl"
CASES_FILE="${BENCHMARK_DIR}/testdata/testcases.jsonl"
MANIFEST_FILE="${BENCHMARK_DIR}/testdata/benchmark_manifest.json"
CASE_LIMIT=""
TOP_K="20"
BUDGET="400"
CONCURRENCY="8"
SKIP_BUILD="0"
SKIP_GENERATE="0"

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Options:
  --run-id <id>           Override the default UTC timestamp run ID
  --results-dir <path>    Parent directory for run artifacts (default: benchmark/results)
  --db <path>             SQLite database path (default: <run-dir>/benchmark.db)
  --workspace <name>      Workspace name (default: benchmark-toggle-comparison)
  --binary <path>         Binary path to build/use (default: benchmark/bin/agent-memory-benchmark)
  --model-dir <path>      Embedding model directory passed to CLI commands
  --seed-file <path>      Prior-session fixtures JSONL file (default: benchmark/testdata/prior_session_fixtures.jsonl)
  --cases-file <path>     Test cases JSONL file (default: benchmark/testdata/testcases.jsonl)
  --case-limit <n>        Optional reduced-case limit for validation runs
  --top-k <n>             Recall candidate count (default: 20)
  --budget <n>            Recall token budget (default: 400)
  --concurrency <n>       Parallel worker count for the ON phase (default: 8)
  --skip-build            Reuse the existing binary without rebuilding
  --skip-generate         Reuse existing generated benchmark inputs
  --help                  Show this help text
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-id)
      RUN_ID="$2"
      shift 2
      ;;
    --results-dir)
      RESULTS_ROOT="$2"
      shift 2
      ;;
    --db)
      DB_PATH="$2"
      shift 2
      ;;
    --workspace)
      WORKSPACE="$2"
      shift 2
      ;;
    --binary)
      BINARY_PATH="$2"
      shift 2
      ;;
    --model-dir)
      MODEL_DIR="$2"
      shift 2
      ;;
    --seed-file)
      SEED_FILE="$2"
      shift 2
      ;;
    --cases-file)
      CASES_FILE="$2"
      shift 2
      ;;
    --case-limit)
      CASE_LIMIT="$2"
      shift 2
      ;;
    --top-k)
      TOP_K="$2"
      shift 2
      ;;
    --budget)
      BUDGET="$2"
      shift 2
      ;;
    --concurrency)
      CONCURRENCY="$2"
      shift 2
      ;;
    --skip-build)
      SKIP_BUILD="1"
      shift
      ;;
    --skip-generate)
      SKIP_GENERATE="1"
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
mkdir -p "${RUN_DIR}"
mkdir -p "$(dirname "${BINARY_PATH}")"

if [[ -z "${DB_PATH}" ]]; then
  DB_PATH="${RUN_DIR}/benchmark.db"
fi

if [[ "${SKIP_GENERATE}" != "1" ]]; then
  python3 "${BENCHMARK_DIR}/generate_benchmark.py"
fi

if [[ ! -f "${SEED_FILE}" ]]; then
  echo "seed file not found: ${SEED_FILE}" >&2
  exit 1
fi

if [[ ! -f "${CASES_FILE}" ]]; then
  echo "cases file not found: ${CASES_FILE}" >&2
  exit 1
fi

if [[ "${SKIP_BUILD}" != "1" ]]; then
  (
    cd "${REPO_ROOT}"
    go build -o "${BINARY_PATH}" ./cmd/agent-memory
  )
fi

rm -f "${DB_PATH}" "${DB_PATH}-shm" "${DB_PATH}-wal"

export BENCHMARK_BINARY="${BINARY_PATH}"
export BENCHMARK_DB_PATH="${DB_PATH}"
export BENCHMARK_WORKSPACE="${WORKSPACE}"
export BENCHMARK_MODEL_DIR="${MODEL_DIR}"
export BENCHMARK_SEED_FILE="${SEED_FILE}"
export BENCHMARK_CASES_FILE="${CASES_FILE}"
export BENCHMARK_MANIFEST_FILE="${MANIFEST_FILE}"
export BENCHMARK_RUN_DIR="${RUN_DIR}"
export BENCHMARK_RUN_ID="${RUN_ID}"
export BENCHMARK_CASE_LIMIT="${CASE_LIMIT}"
export BENCHMARK_TOP_K="${TOP_K}"
export BENCHMARK_BUDGET="${BUDGET}"
export BENCHMARK_CONCURRENCY="${CONCURRENCY}"

python3 - <<'PY'
import concurrent.futures
import json
import os
import queue
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from pathlib import Path


def env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise SystemExit(f"missing required environment variable: {name}")
    return value


def load_jsonl(path: Path) -> list[dict]:
    rows: list[dict] = []
    with path.open("r", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue
            rows.append(json.loads(line))
    return rows


def write_jsonl(path: Path, rows: list[dict]) -> None:
    with path.open("w", encoding="utf-8") as handle:
        for row in rows:
            handle.write(json.dumps(row, sort_keys=True))
            handle.write("\n")


def progress_line(phase: str, current: int, total: int, started_at: float) -> str:
    percent = (current / total * 100.0) if total > 0 else 0.0
    elapsed = max(0.0, time.time() - started_at)
    rate = (current / elapsed) if elapsed > 0 else 0.0
    remaining = ((total - current) / rate) if rate > 0 and total >= current else 0.0
    return (
        f"[benchmark] {phase}: {current}/{total} completed "
        f"({percent:.1f}%) | elapsed {elapsed:.1f}s | eta {remaining:.1f}s"
    )


def maybe_print_progress(
    phase: str,
    current: int,
    total: int,
    started_at: float,
    last_print_at: float,
    *,
    force: bool = False,
    min_interval_seconds: float = 5.0,
) -> float:
    now = time.time()
    if not force and (now - last_print_at) < min_interval_seconds and current < total:
        return last_print_at
    print(progress_line(phase, current, total, started_at), file=sys.stderr, flush=True)
    return now


def run_cli(args: list[str], enabled: bool, run_label: str) -> dict:
    command = [binary_path] + args
    run_env = dict(os.environ)
    run_env["AGENT_MEMORY_ENABLED"] = "1" if enabled else "0"
    run_env["AGENT_MEMORY_RUN_LABEL"] = run_label
    if model_dir:
        command.extend(["--model-dir", model_dir])
    for attempt in range(6):
        completed = subprocess.run(
            command,
            capture_output=True,
            text=True,
            env=run_env,
            check=False,
        )
        if completed.returncode == 0:
            try:
                return json.loads(completed.stdout)
            except json.JSONDecodeError as exc:
                raise RuntimeError(f"invalid JSON from {' '.join(command)}: {completed.stdout!r}") from exc
        message = completed.stderr.strip() or completed.stdout.strip() or f"exit code {completed.returncode}"
        lowered = message.lower()
        if "database is locked" in lowered or "sqlite_busy" in lowered:
            time.sleep(0.2 * (attempt + 1))
            continue
        raise RuntimeError(f"command failed: {' '.join(command)} :: {message}")
    raise RuntimeError(f"command failed after sqlite retry budget: {' '.join(command)}")


class BenchmarkWorker:
    def __init__(self, *, enabled: bool, run_label: str, worker_index: int) -> None:
        command = [
            binary_path,
            "benchmark-worker",
            "--db", db_path,
            "--workspace", workspace,
            "--format", "json",
        ]
        if model_dir:
            command.extend(["--model-dir", model_dir])
        run_env = dict(os.environ)
        run_env["AGENT_MEMORY_ENABLED"] = "1" if enabled else "0"
        run_env["AGENT_MEMORY_RUN_LABEL"] = run_label
        self._process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            env=run_env,
            bufsize=1,
        )
        self._stderr_lines: list[str] = []
        self._stderr_thread = threading.Thread(
            target=self._drain_stderr,
            name=f"benchmark-worker-stderr-{worker_index}",
            daemon=True,
        )
        self._stderr_thread.start()

    def _drain_stderr(self) -> None:
        assert self._process.stderr is not None
        for line in self._process.stderr:
            clean = line.rstrip()
            if clean:
                self._stderr_lines.append(clean)
                if len(self._stderr_lines) > 50:
                    self._stderr_lines = self._stderr_lines[-50:]

    def request(self, payload: dict) -> dict:
        if self._process.stdin is None or self._process.stdout is None:
            raise RuntimeError("benchmark worker pipes are not available")
        if self._process.poll() is not None:
            raise RuntimeError(self._dead_process_message("worker exited before handling request"))
        self._process.stdin.write(json.dumps(payload, sort_keys=True) + "\n")
        self._process.stdin.flush()
        line = self._process.stdout.readline()
        if not line:
            raise RuntimeError(self._dead_process_message("worker produced no response"))
        try:
            response = json.loads(line)
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"benchmark worker returned invalid JSON: {line!r}") from exc
        if not response.get("ok"):
            raise RuntimeError(response.get("error") or "benchmark worker request failed")
        data = response.get("data")
        if not isinstance(data, dict):
            raise RuntimeError(f"benchmark worker returned invalid payload: {response!r}")
        return data

    def close(self) -> None:
        if self._process.stdin is not None and not self._process.stdin.closed:
            self._process.stdin.close()
        try:
            self._process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self._process.kill()
            self._process.wait(timeout=5)

    def _dead_process_message(self, prefix: str) -> str:
        tail = "\n".join(self._stderr_lines[-10:])
        if tail:
            return f"{prefix}: {tail}"
        return prefix


def now_rfc3339() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


binary_path = env("BENCHMARK_BINARY")
db_path = env("BENCHMARK_DB_PATH")
workspace = env("BENCHMARK_WORKSPACE")
seed_file = Path(env("BENCHMARK_SEED_FILE"))
cases_file = Path(env("BENCHMARK_CASES_FILE"))
run_dir = Path(env("BENCHMARK_RUN_DIR"))
run_id = env("BENCHMARK_RUN_ID")
manifest_file = Path(os.environ.get("BENCHMARK_MANIFEST_FILE", ""))
model_dir = os.environ.get("BENCHMARK_MODEL_DIR", "").strip()
case_limit_raw = os.environ.get("BENCHMARK_CASE_LIMIT", "").strip()
top_k = int(env("BENCHMARK_TOP_K"))
budget = int(env("BENCHMARK_BUDGET"))
concurrency = int(env("BENCHMARK_CONCURRENCY"))

if case_limit_raw:
    case_limit = int(case_limit_raw)
    if case_limit <= 0:
        raise SystemExit("BENCHMARK_CASE_LIMIT must be positive when provided")
else:
    case_limit = None

if concurrency <= 0:
    raise SystemExit("BENCHMARK_CONCURRENCY must be positive")

seeds = load_jsonl(seed_file)
cases = load_jsonl(cases_file)
if case_limit is not None:
    cases = cases[:case_limit]

seed_results: list[dict] = []
id_mapping: dict[str, str] = {}
seed_started = time.time()
seed_last_print = seed_started
print(f"[benchmark] seed: starting {len(seeds)} memories", file=sys.stderr, flush=True)
for seed in seeds:
    payload = run_cli(
        [
            "write",
            "--db", db_path,
            "--workspace", workspace,
            "--type", "semantic",
            "--content", seed["content"],
            "--format", "json",
        ],
        enabled=True,
        run_label="benchmark-seed",
    )
    data = payload.get("data", {})
    runtime_id = data.get("ID") or data.get("id") or ""
    if not runtime_id:
        raise RuntimeError(f"write response missing runtime ID for stable seed {seed['stable_id']}")
    id_mapping[seed["stable_id"]] = runtime_id
    seed_results.append(
        {
            "stable_id": seed["stable_id"],
            "runtime_id": runtime_id,
            "cluster_id": seed["cluster_id"],
            "cluster_title": seed["cluster_title"],
            "fixture_index": int(seed.get("fixture_index", seed.get("seed_index", 0))),
            "memory_type": seed.get("memory_type", "semantic"),
            "content": seed["content"],
            "write_data": data,
        }
    )
    seed_last_print = maybe_print_progress(
        "seed",
        len(seed_results),
        len(seeds),
        seed_started,
        seed_last_print,
        force=(len(seed_results) == len(seeds)),
    )
seed_duration_ms = int((time.time() - seed_started) * 1000)


def estimate_baseline_tokens(result_data: dict) -> int:
    observation_tokens = int(result_data.get("observation_tokens", 0) or 0)
    hit_tokens = 0
    for hit in result_data.get("hits", []):
        memory = hit.get("memory", {})
        content = memory.get("content", "")
        if content:
            hit_tokens += len(str(content).split())
    return hit_tokens + observation_tokens


def case_token_totals(result_data: dict) -> tuple[int, int, int]:
    if result_data.get("disabled") is True:
        returned_tokens = int(result_data.get("tokens_used", 0) or 0)
        baseline_tokens = int(result_data.get("baseline_tokens", returned_tokens) or returned_tokens)
        saved_tokens = baseline_tokens - returned_tokens
        return returned_tokens, baseline_tokens, saved_tokens

    observation_tokens = int(result_data.get("observation_tokens", 0) or 0)
    clipping = result_data.get("clipping", {})
    returned_tokens = int(clipping.get("used_tokens", 0) or 0) + observation_tokens
    baseline_tokens = estimate_baseline_tokens(result_data)
    saved_tokens = baseline_tokens - returned_tokens
    return returned_tokens, baseline_tokens, saved_tokens


def answer_text_from_result(result_data: dict) -> str:
    context_block = result_data.get("context_block")
    if isinstance(context_block, str) and context_block.strip():
        return context_block

    hits = result_data.get("hits", [])
    if not isinstance(hits, list):
        return ""
    chunks: list[str] = []
    for hit in hits:
        memory = hit.get("memory", {})
        content = memory.get("content", "")
        if content:
            chunks.append(str(content))
    return "\n".join(chunks)


def build_case_result(case: dict, worker: BenchmarkWorker, *, enabled: bool, run_label: str) -> dict:
    started = time.time()
    data = worker.request(
        {
            "task": case["prompt"],
            "top_k": top_k,
            "budget": budget,
        }
    )
    elapsed_ms = int((time.time() - started) * 1000)
    returned_tokens, baseline_tokens, saved_tokens = case_token_totals(data)
    hits = data.get("hits", [])
    returned_ids = []
    for hit in hits:
        memory = hit.get("memory", {})
        memory_id = memory.get("id")
        if memory_id:
            returned_ids.append(memory_id)
    return {
        "stable_case_id": case["stable_case_id"],
        "cluster_id": case["cluster_id"],
        "cluster_title": case["cluster_title"],
        "question_id": case["question_id"],
        "question_index": case["question_index"],
        "variant_index": case["variant_index"],
        "prompt": case["prompt"],
        "prior_fixture_ids": case.get("prior_fixture_ids", []),
        "expected_facts": case.get("expected_facts", []),
        "expected_fact_groups": case.get("expected_fact_groups", []),
        "expected_files": case.get("expected_files", []),
        "expected_commands": case.get("expected_commands", []),
        "expected_locator_targets": case.get("expected_locator_targets", []),
        "gold_ids": case["gold_ids"],
        "partial_ids": case["partial_ids"],
        "required_keywords": case["required_keywords"],
        "relevance_grades": case["relevance_grades"],
        "memory_enabled": enabled,
        "run_label": run_label,
        "elapsed_ms": elapsed_ms,
        "answer_text": answer_text_from_result(data),
        "returned_memory_ids": returned_ids,
        "returned_count": len(returned_ids),
        "returned_tokens": returned_tokens,
        "baseline_tokens": baseline_tokens,
        "saved_tokens": saved_tokens,
        "trace_summary": {
            "lookup_effort_units": int(data.get("retrieved_hit_count", len(hits)) or len(hits)),
            "retrieved_hit_count": int(data.get("retrieved_hit_count", len(hits)) or len(hits)),
            "observation_count": int(data.get("observation_count", 0) or 0),
            "observation_tokens": int(data.get("observation_tokens", 0) or 0),
            "expected_locator_count": len(case.get("expected_locator_targets", [])),
            "expected_fact_group_count": len(case.get("expected_fact_groups", [])),
            "prior_fixture_count": len(case.get("prior_fixture_ids", [])),
            "disabled": bool(data.get("disabled", False)),
            "retrieval_mode": data.get("retrieval_mode") or data.get("mode") or "",
        },
        "result_data": data,
    }


def execute_phase(enabled: bool, run_label: str) -> tuple[list[dict], int]:
    started = time.time()
    phase_name = "on" if enabled else "off"
    print(f"[benchmark] {phase_name}: starting {len(cases)} cases", file=sys.stderr, flush=True)
    indexed_cases = list(enumerate(cases))
    rows: list[dict | None] = [None] * len(indexed_cases)
    completed = 0
    last_print = started
    worker_count = max(1, min(concurrency, len(indexed_cases))) if indexed_cases else 1
    work_queue: queue.Queue[tuple[int, dict]] = queue.Queue()
    for item in indexed_cases:
        work_queue.put(item)

    completion_queue: queue.Queue[tuple[int, dict]] = queue.Queue()

    def worker_loop(worker: BenchmarkWorker) -> None:
        while True:
            try:
                index, case = work_queue.get_nowait()
            except queue.Empty:
                return
            try:
                completion_queue.put((index, build_case_result(case, worker, enabled=enabled, run_label=run_label)))
            finally:
                work_queue.task_done()

    workers = [BenchmarkWorker(enabled=enabled, run_label=run_label, worker_index=i) for i in range(worker_count)]
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=worker_count) as executor:
            futures = [executor.submit(worker_loop, worker) for worker in workers]
            while completed < len(indexed_cases):
                try:
                    index, row = completion_queue.get(timeout=0.5)
                except queue.Empty:
                    for future in futures:
                        exc = future.exception(timeout=0) if future.done() else None
                        if exc is not None:
                            raise exc
                    continue
                rows[index] = row
                completed += 1
                last_print = maybe_print_progress(
                    phase_name,
                    completed,
                    len(indexed_cases),
                    started,
                    last_print,
                    force=(completed == len(indexed_cases)),
                )
            for future in futures:
                future.result()
    finally:
        for worker in workers:
            worker.close()
    completed_rows = [row for row in rows if row is not None]
    if len(completed_rows) != len(indexed_cases):
        raise RuntimeError("runner did not produce a result for every benchmark case")
    duration_ms = int((time.time() - started) * 1000)
    return completed_rows, duration_ms


on_results, on_duration_ms = execute_phase(True, "benchmark-on")
off_results, off_duration_ms = execute_phase(False, "benchmark-off")

seed_results_path = run_dir / "seed_results.jsonl"
id_mapping_path = run_dir / "id_mapping.json"
quality_on_path = run_dir / "quality-on.jsonl"
quality_off_path = run_dir / "quality-off.jsonl"
run_manifest_path = run_dir / "run_manifest.json"

write_jsonl(seed_results_path, seed_results)
id_mapping_path.write_text(json.dumps(id_mapping, indent=2, sort_keys=True) + "\n", encoding="utf-8")
write_jsonl(quality_on_path, on_results)
write_jsonl(quality_off_path, off_results)

generator_manifest = {}
if manifest_file.is_file():
    generator_manifest = json.loads(manifest_file.read_text(encoding="utf-8"))

run_manifest = {
    "run_id": run_id,
    "generated_at": now_rfc3339(),
    "workspace": workspace,
    "db_path": db_path,
    "binary_path": binary_path,
    "model_dir": model_dir,
    "case_limit": case_limit,
    "top_k": top_k,
    "budget": budget,
    "concurrency": concurrency,
    "execution_mode": "persistent-worker",
    "worker_count_per_phase": max(1, min(concurrency, len(cases))) if cases else 1,
    "seed_file": str(seed_file),
    "prior_session_fixtures_file": str(seed_file),
    "cases_file": str(cases_file),
    "seed_count": len(seeds),
    "executed_case_count_per_phase": len(cases),
    "seed_duration_ms": seed_duration_ms,
    "on_duration_ms": on_duration_ms,
    "off_duration_ms": off_duration_ms,
    "artifacts": {
        "seed_results": str(seed_results_path),
        "id_mapping": str(id_mapping_path),
        "quality_on": str(quality_on_path),
        "quality_off": str(quality_off_path),
    },
    "generator_manifest": generator_manifest,
}
run_manifest_path.write_text(json.dumps(run_manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")

print(json.dumps(
    {
        "run_id": run_id,
        "workspace": workspace,
        "seed_count": len(seeds),
        "executed_case_count_per_phase": len(cases),
        "seed_duration_ms": seed_duration_ms,
        "on_duration_ms": on_duration_ms,
        "off_duration_ms": off_duration_ms,
        "run_dir": str(run_dir),
    },
    sort_keys=True,
))
PY
