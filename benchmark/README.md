# Benchmark Workspace

This directory contains the end-to-end benchmark pipeline for the Kiro spec in `.kiro/specs/benchmark-toggle-comparison/`.

## Files

- `generate_benchmark.py`: deterministic benchmark generator
- `run_benchmark.sh`: build, seed, and execute memory ON/OFF runs
- `score.py`: score raw artifacts and optionally ingest into SQLite
- `test_benchmark.py`: focused benchmark tests
- `testdata/`: generated seed memories, test cases, and manifests
- `results/`: per-run raw outputs and score reports

## Dataset Shape

The generator creates a fixed benchmark corpus:

- `25` topic clusters
- `8` seed memories per cluster
- `10` canonical questions per cluster
- `40` deterministic variants per canonical question

This yields:

- `200` seed memories
- `10,000` labeled test cases

## Typical Workflow

Generate benchmark inputs:

```bash
python3 benchmark/generate_benchmark.py
```

Run a reduced validation slice:

```bash
bash benchmark/run_benchmark.sh --run-id reduced-check --case-limit 25 --skip-build
python3 benchmark/score.py --run-dir benchmark/results/reduced-check --db benchmark/results/reduced-check/benchmark.db --ingest --format raw
```

Run the full `10,000`-case comparison against the dashboard workspace DB:

```bash
bash benchmark/run_benchmark.sh \
  --run-id full-10000 \
  --db ~/.agent-memory/benchmark-toggle-comparison.db \
  --skip-build

python3 benchmark/score.py \
  --run-dir benchmark/results/full-10000 \
  --db ~/.agent-memory/benchmark-toggle-comparison.db \
  --ingest \
  --format raw
```

## Output Artifacts

Each runner invocation writes a dedicated results directory containing:

- `seed_results.jsonl`: runtime IDs returned during seed writes
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
- Full `10,000`-case run with concurrency `8`: expect several minutes to tens of minutes
- Scoring and SQLite ingest: typically a few seconds

## Notes On Throughput

- The ON phase uses parallel recall execution with `--concurrency` (default `8`).
- The OFF phase short-circuits without invoking recall subprocesses.
- Per-case token totals are stored in raw result artifacts so scoring stays deterministic even when ON cases run concurrently.

## Troubleshooting

- `no such table: benchmark_runs`
  - Re-run `score.py --ingest`. The scorer creates the table if it does not exist yet.

- `seed file not found` or `cases file not found`
  - Run `python3 benchmark/generate_benchmark.py` first, or omit `--skip-generate`.

- Full run seems slow
  - Lower `--concurrency` if the machine is resource-constrained, or use `--case-limit` for validation before the full run.

- Dashboard does not show benchmark history
  - Make sure the run was scored with `--ingest` into the same workspace DB that the dashboard API serves, typically `~/.agent-memory/<workspace>.db`.

- OFF phase shows zero tokens
  - That is expected. The OFF benchmark path intentionally short-circuits and records disabled results only.

## Test Command

```bash
python3 -m unittest benchmark.test_benchmark
```
