# Benchmark Workspace

This directory contains the end-to-end benchmark pipeline for the Kiro spec in `.kiro/specs/benchmark-toggle-comparison/`.

## Files

- `generate_benchmark.py`: deterministic continuation-scenario generator
- `run_benchmark.sh`: build, seed prior-session fixtures, and execute memory ON/OFF runs
- `score.py`: score continuation outcomes plus supporting retrieval diagnostics and optionally ingest into SQLite
- `test_benchmark.py`: focused benchmark tests
- `testdata/`: generated seed memories, test cases, and manifests
- `results/`: per-run raw outputs and score reports

## Dataset Shape

The generator creates a fixed continuation benchmark corpus:

- `25` topic clusters
- `8` prior-session fixtures per cluster
- `10` canonical questions per cluster
- `40` deterministic variants per canonical question

This yields:

- `200` prior-session fixtures
- `10,000` labeled test cases

## Typical Workflow

Generate continuation benchmark inputs:

```bash
rtk python3 benchmark/generate_benchmark.py
```

Run a reduced validation slice:

```bash
rtk bash benchmark/run_benchmark.sh --run-id reduced-check --case-limit 25 --skip-build
rtk python3 benchmark/score.py --run-dir benchmark/results/reduced-check --db benchmark/results/reduced-check/benchmark.db --ingest --format raw
```

Reset old benchmark artifacts and persisted benchmark history before a clean rerun:

```bash
rtk python3 benchmark/clean_results.py
```

Watch benchmark progress in a terminal:

```bash
rtk bash benchmark/watch_progress.sh continuation-full-10000
```

Print a single snapshot instead of a live loop:

```bash
rtk bash benchmark/watch_progress.sh continuation-full-10000 --once
```

Run the full `10,000`-case comparison against the dashboard workspace DB:

```bash
rtk bash benchmark/run_benchmark.sh \
  --run-id full-10000 \
  --db ~/.agent-memory/benchmark-toggle-comparison.db \
  --skip-build

rtk python3 benchmark/score.py \
  --run-dir benchmark/results/full-10000 \
  --db ~/.agent-memory/benchmark-toggle-comparison.db \
  --ingest \
  --format raw
```

## Output Artifacts

Each runner invocation writes a dedicated results directory containing:

- `seed_results.jsonl`: runtime IDs returned during prior-session fixture writes
- `id_mapping.json`: stable benchmark IDs mapped to runtime memory UUIDs
- `quality-on.jsonl`: per-case raw ON results
- `quality-off.jsonl`: per-case raw OFF results
- `run_manifest.json`: run configuration and artifact locations
- `score_report.json`: aggregate score report
- `score_cases.jsonl`: per-case score breakdown

## Expected Runtime

Runtime depends on embedding model speed and local CPU capacity.

- Generator: a few seconds
- Reduced validation run (`25` cases or fewer): usually under a minute
- Full `10,000`-case run with concurrency `2` to `8`: expect a long-running local job and validate on a reduced slice first
- Scoring and SQLite ingest: typically a few seconds

## Notes On Throughput

- The ON phase uses parallel recall execution with `--concurrency`.
- The scaled benchmark path now uses persistent `benchmark-worker` subprocesses, so each worker reuses its SQLite connection and embedding-provider initialization across many cases instead of paying startup cost per case.
- The OFF phase now executes the same real disabled recall command path rather than a synthetic placeholder row.
- The runner retries transient `SQLITE_BUSY` failures so reduced and full runs stay stable under concurrency.
- Per-case token totals, runtime, answer text, and trace summaries are stored in raw result artifacts so scoring stays deterministic.

## Troubleshooting

- `no such table: benchmark_runs`
  - Re-run `score.py --ingest`. The scorer creates the table if it does not exist yet.

- `seed file not found` or `cases file not found`
  - Run `python3 benchmark/generate_benchmark.py` first, or omit `--skip-generate`.

- Need a clean slate before re-running the reference benchmark
  - Run `rtk python3 benchmark/clean_results.py` to remove stale `benchmark/results/*` directories and clear persisted `benchmark_runs` rows from the default dashboard databases.

- Full run seems slow
  - Lower `--concurrency` if the machine is resource-constrained, or use `--case-limit` for validation before the full run. The runner already reuses long-lived benchmark workers, so remaining slowness is usually retrieval/runtime cost or SQLite contention rather than per-case process startup.

- Dashboard does not show benchmark history
  - Make sure the run was scored with `--ingest` into the same workspace DB that the dashboard API serves, typically `~/.agent-memory/<workspace>.db`.

- Continuation score is negative
  - That means the measured deltas favored OFF, usually because ON returned more context, took longer, or required more memory review than the disabled baseline.

- Retrieval metrics look good but continuation score stays weak
  - That means memory retrieved relevant text, but the real ON-versus-OFF continuation benefit was still limited or negative.

## Interpretation Guidance

- `Task Success Delta`, `Fact Coverage Delta`, `Completeness Delta`, `Locator Success Delta`, `Verification Effort Delta`, and `Operational Cost Delta` are the primary benchmark outputs.
- `Precision`, `Gold Recall`, `NDCG`, and `Keyword Coverage` remain supporting diagnostics that explain ranking quality without replacing the main continuation verdict.
- Retrieval-context token and retrieval-context cost deltas are secondary diagnostics only. They describe context bloat or compression, not the whole end-to-end task economics.
- Negative efficiency deltas are valid and should be preserved. They mean memory increased context size, runtime, verification burden, or estimated operational cost under the chosen baseline.
- `combined_score` remains secondary. `continuation_score` and the paired benefit metrics are the main story.

## Test Command

```bash
rtk python3 -m unittest benchmark.test_benchmark
```
