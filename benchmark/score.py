#!/usr/bin/env python3
"""Score benchmark ON/OFF results into aggregate and per-cluster summaries."""

from __future__ import annotations

import argparse
import json
import math
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Any


PRICE_PER_1K_TOKENS = 0.03
COMBINED_WEIGHTS = {
    "ndcg": 0.25,
    "keyword_coverage": 0.20,
    "precision": 0.15,
    "gold_recall": 0.15,
    "token_efficiency": 0.15,
    "f1": 0.10,
}


@dataclass(frozen=True)
class TokenMetricRow:
    row_id: int
    operation: str
    returned_tokens: int
    baseline_tokens: int
    saved_tokens: int
    run_label: str
    memory_enabled: bool


def load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if line:
                rows.append(json.loads(line))
    return rows


def bool_to_int(value: bool) -> int:
    return 1 if value else 0


def safe_div(numerator: float, denominator: float) -> float:
    if denominator <= 0:
        return 0.0
    return numerator / denominator


def harmonic_mean(a: float, b: float) -> float:
    if a <= 0 or b <= 0:
        return 0.0
    return (2 * a * b) / (a + b)


def verdict_for(score: float) -> str:
    if score >= 0.7:
        return "STRONG BENEFIT"
    if score >= 0.4:
        return "GOOD BENEFIT"
    if score >= 0.2:
        return "MEASURABLE BENEFIT"
    return "MINIMAL BENEFIT"


def compute_dcg(returned_ids: list[str], relevance_grades: dict[str, int]) -> float:
    total = 0.0
    for rank, memory_id in enumerate(returned_ids):
        grade = relevance_grades.get(memory_id, 0)
        if grade <= 0:
            continue
        total += (2**grade - 1) / math.log2(rank + 2)
    return total


def compute_ndcg(returned_ids: list[str], relevance_grades: dict[str, int]) -> float:
    if not relevance_grades:
        return 0.0
    actual = compute_dcg(returned_ids, relevance_grades)
    ideal_grades = sorted(relevance_grades.values(), reverse=True)
    if returned_ids:
        ideal_grades = ideal_grades[: len(returned_ids)]
    ideal = 0.0
    for rank, grade in enumerate(ideal_grades):
        ideal += (2**grade - 1) / math.log2(rank + 2)
    return safe_div(actual, ideal)


def keyword_found_count(required_keywords: list[str], returned_text: str) -> int:
    haystack = returned_text.lower()
    found = 0
    for keyword in required_keywords:
        if keyword.lower() in haystack:
            found += 1
    return found


def fetch_token_metrics(
    db_path: Path,
    workspace: str,
    run_label: str,
    memory_enabled: bool,
    operation: str = "recall",
) -> list[TokenMetricRow]:
    conn = sqlite3.connect(str(db_path))
    try:
        rows = conn.execute(
            """
            SELECT id, operation, returned_tokens, baseline_tokens, saved_tokens, run_label, memory_enabled
            FROM token_metrics
            WHERE workspace = ?
              AND run_label = ?
              AND memory_enabled = ?
              AND operation = ?
            ORDER BY id ASC
            """,
            (workspace, run_label, 1 if memory_enabled else 0, operation),
        ).fetchall()
    finally:
        conn.close()
    return [
        TokenMetricRow(
            row_id=row[0],
            operation=row[1],
            returned_tokens=row[2],
            baseline_tokens=row[3],
            saved_tokens=row[4],
            run_label=row[5],
            memory_enabled=bool(row[6]),
        )
        for row in rows
    ]


def case_token_totals(case_row: dict[str, Any]) -> tuple[int | None, int | None, int | None]:
    returned = case_row.get("returned_tokens")
    baseline = case_row.get("baseline_tokens")
    saved = case_row.get("saved_tokens")
    if all(isinstance(value, int) for value in (returned, baseline, saved)):
        return returned, baseline, saved
    return None, None, None


def aggregate_case_rows(case_rows: list[dict[str, Any]]) -> dict[str, Any]:
    relevant_returned_sum = 0
    returned_total_sum = 0
    relevant_total_sum = 0
    gold_returned_sum = 0
    gold_total_sum = 0
    keyword_found_sum = 0
    keyword_total_sum = 0
    ndcg_sum = 0.0
    returned_tokens_sum = 0
    baseline_tokens_sum = 0
    saved_tokens_sum = 0

    for row in case_rows:
        relevant_returned_sum += row["relevant_returned"]
        returned_total_sum += row["returned_count"]
        relevant_total_sum += row["relevant_total"]
        gold_returned_sum += row["gold_returned"]
        gold_total_sum += row["gold_total"]
        keyword_found_sum += row["keyword_found"]
        keyword_total_sum += row["keyword_total"]
        ndcg_sum += row["ndcg"]
        returned_tokens_sum += row["returned_tokens"]
        baseline_tokens_sum += row["baseline_tokens"]
        saved_tokens_sum += row["saved_tokens"]

    precision = safe_div(relevant_returned_sum, returned_total_sum)
    recall = safe_div(relevant_returned_sum, relevant_total_sum)
    gold_recall = safe_div(gold_returned_sum, gold_total_sum)
    keyword_coverage = safe_div(keyword_found_sum, keyword_total_sum)
    ndcg = safe_div(ndcg_sum, len(case_rows))
    f1 = harmonic_mean(precision, recall)
    token_efficiency = safe_div(saved_tokens_sum, baseline_tokens_sum)
    cost_without_memory = (baseline_tokens_sum / 1000.0) * PRICE_PER_1K_TOKENS
    cost_with_memory = (returned_tokens_sum / 1000.0) * PRICE_PER_1K_TOKENS
    cost_saved = cost_without_memory - cost_with_memory
    cost_saved_pct = safe_div(cost_saved, cost_without_memory)

    combined_score = (
        ndcg * COMBINED_WEIGHTS["ndcg"]
        + keyword_coverage * COMBINED_WEIGHTS["keyword_coverage"]
        + precision * COMBINED_WEIGHTS["precision"]
        + gold_recall * COMBINED_WEIGHTS["gold_recall"]
        + token_efficiency * COMBINED_WEIGHTS["token_efficiency"]
        + f1 * COMBINED_WEIGHTS["f1"]
    )

    return {
        "cases": len(case_rows),
        "precision": precision,
        "recall": recall,
        "gold_recall": gold_recall,
        "keyword_coverage": keyword_coverage,
        "ndcg": ndcg,
        "f1": f1,
        "token_efficiency": token_efficiency,
        "returned_tokens": returned_tokens_sum,
        "baseline_tokens": baseline_tokens_sum,
        "saved_tokens": saved_tokens_sum,
        "cost_without_memory": cost_without_memory,
        "cost_with_memory": cost_with_memory,
        "cost_saved": cost_saved,
        "cost_saved_pct": cost_saved_pct,
        "combined_score": combined_score,
        "verdict": verdict_for(combined_score),
    }


def score_run(run_dir: Path, db_path: Path | None) -> dict[str, Any]:
    run_manifest = load_json(run_dir / "run_manifest.json")
    workspace = run_manifest["workspace"]
    db_file = db_path or Path(run_manifest["db_path"])
    id_mapping: dict[str, str] = load_json(run_dir / "id_mapping.json")
    on_rows = load_jsonl(run_dir / "quality-on.jsonl")
    off_rows = load_jsonl(run_dir / "quality-off.jsonl")

    on_token_rows = fetch_token_metrics(db_file, workspace, "benchmark-on", True)
    off_token_rows = fetch_token_metrics(db_file, workspace, "benchmark-off", False)

    per_case_rows: list[dict[str, Any]] = []
    off_disabled_count = 0
    off_returned_tokens = 0
    off_baseline_tokens = 0
    off_saved_tokens = 0
    for off_case in off_rows:
        if off_case.get("result_data", {}).get("disabled") is True:
            off_disabled_count += 1
        returned, baseline, saved = case_token_totals(off_case)
        if returned is not None and baseline is not None and saved is not None:
            off_returned_tokens += returned
            off_baseline_tokens += baseline
            off_saved_tokens += saved

    if off_returned_tokens == 0 and off_baseline_tokens == 0 and off_saved_tokens == 0 and off_token_rows:
        off_returned_tokens = sum(row.returned_tokens for row in off_token_rows)
        off_baseline_tokens = sum(row.baseline_tokens for row in off_token_rows)
        off_saved_tokens = sum(row.saved_tokens for row in off_token_rows)

    for index, on_case in enumerate(on_rows):
        returned_tokens, baseline_tokens, saved_tokens = case_token_totals(on_case)
        if returned_tokens is None or baseline_tokens is None or saved_tokens is None:
            if len(on_token_rows) != len(on_rows):
                raise SystemExit(
                    f"ON token metric row count mismatch: expected {len(on_rows)}, got {len(on_token_rows)}"
                )
            token_row = on_token_rows[index]
            returned_tokens = token_row.returned_tokens
            baseline_tokens = token_row.baseline_tokens
            saved_tokens = token_row.saved_tokens
        gold_runtime_ids = [id_mapping[item] for item in on_case["gold_ids"]]
        partial_runtime_ids = [id_mapping[item] for item in on_case["partial_ids"]]
        runtime_relevance = {
            id_mapping[stable_id]: grade
            for stable_id, grade in on_case["relevance_grades"].items()
            if stable_id in id_mapping
        }

        hits = on_case.get("result_data", {}).get("hits", [])
        returned_ids = []
        returned_chunks = []
        for hit in hits:
            memory = hit.get("memory", {})
            memory_id = memory.get("id")
            if memory_id:
                returned_ids.append(memory_id)
            content = memory.get("content", "")
            if content:
                returned_chunks.append(content)

        returned_text = "\n".join(returned_chunks)
        gold_set = set(gold_runtime_ids)
        partial_set = set(partial_runtime_ids)
        relevant_set = gold_set | partial_set
        returned_set = set(returned_ids)
        relevant_returned = len([memory_id for memory_id in returned_ids if memory_id in relevant_set])
        gold_returned = len([memory_id for memory_id in returned_ids if memory_id in gold_set])
        keyword_total = len(on_case["required_keywords"])
        keyword_found = keyword_found_count(on_case["required_keywords"], returned_text)

        per_case_rows.append(
            {
                "stable_case_id": on_case["stable_case_id"],
                "cluster_id": on_case["cluster_id"],
                "cluster_title": on_case["cluster_title"],
                "question_id": on_case["question_id"],
                "variant_index": on_case["variant_index"],
                "returned_count": len(returned_ids),
                "relevant_total": len(relevant_set),
                "relevant_returned": relevant_returned,
                "gold_total": len(gold_set),
                "gold_returned": gold_returned,
                "keyword_total": keyword_total,
                "keyword_found": keyword_found,
                "precision": safe_div(relevant_returned, len(returned_ids)),
                "recall": safe_div(relevant_returned, len(relevant_set)),
                "gold_recall": safe_div(gold_returned, len(gold_set)),
                "keyword_coverage": safe_div(keyword_found, keyword_total),
                "ndcg": compute_ndcg(returned_ids, runtime_relevance),
                "returned_tokens": returned_tokens,
                "baseline_tokens": baseline_tokens,
                "saved_tokens": saved_tokens,
            }
        )

    aggregate = aggregate_case_rows(per_case_rows)

    clusters: dict[str, list[dict[str, Any]]] = {}
    for row in per_case_rows:
        clusters.setdefault(row["cluster_id"], []).append(row)
    cluster_breakdown = []
    for cluster_id in sorted(clusters):
        rows = clusters[cluster_id]
        cluster_summary = aggregate_case_rows(rows)
        cluster_summary["cluster_id"] = cluster_id
        cluster_summary["cluster_title"] = rows[0]["cluster_title"]
        cluster_breakdown.append(cluster_summary)

    off_summary = {
        "cases": len(off_rows),
        "disabled_count": off_disabled_count,
        "all_disabled": off_disabled_count == len(off_rows),
        "returned_tokens": off_returned_tokens,
        "baseline_tokens": off_baseline_tokens,
        "saved_tokens": off_saved_tokens,
    }

    return {
        "run_id": run_manifest["run_id"],
        "workspace": workspace,
        "db_path": str(db_file),
        "generator_manifest": run_manifest.get("generator_manifest", {}),
        "run_manifest": run_manifest,
        "weights": COMBINED_WEIGHTS,
        "summary": aggregate,
        "off_phase": off_summary,
        "clusters": cluster_breakdown,
        "per_case_report_path": str(run_dir / "score_cases.jsonl"),
    }


def write_case_report(path: Path, run_dir: Path, db_path: Path | None) -> None:
    run_manifest = load_json(run_dir / "run_manifest.json")
    workspace = run_manifest["workspace"]
    db_file = db_path or Path(run_manifest["db_path"])
    id_mapping: dict[str, str] = load_json(run_dir / "id_mapping.json")
    on_rows = load_jsonl(run_dir / "quality-on.jsonl")
    on_token_rows = fetch_token_metrics(db_file, workspace, "benchmark-on", True)

    with path.open("w", encoding="utf-8") as handle:
        for index, on_case in enumerate(on_rows):
            returned_tokens, baseline_tokens, saved_tokens = case_token_totals(on_case)
            if returned_tokens is None or baseline_tokens is None or saved_tokens is None:
                if len(on_token_rows) != len(on_rows):
                    raise SystemExit(
                        f"ON token metric row count mismatch: expected {len(on_rows)}, got {len(on_token_rows)}"
                    )
                token_row = on_token_rows[index]
                returned_tokens = token_row.returned_tokens
                baseline_tokens = token_row.baseline_tokens
                saved_tokens = token_row.saved_tokens
            gold_runtime_ids = [id_mapping[item] for item in on_case["gold_ids"]]
            partial_runtime_ids = [id_mapping[item] for item in on_case["partial_ids"]]
            runtime_relevance = {
                id_mapping[stable_id]: grade
                for stable_id, grade in on_case["relevance_grades"].items()
                if stable_id in id_mapping
            }
            hits = on_case.get("result_data", {}).get("hits", [])
            returned_ids = [hit.get("memory", {}).get("id", "") for hit in hits if hit.get("memory", {}).get("id")]
            returned_text = "\n".join(
                hit.get("memory", {}).get("content", "")
                for hit in hits
                if hit.get("memory", {}).get("content")
            )
            gold_set = set(gold_runtime_ids)
            partial_set = set(partial_runtime_ids)
            relevant_set = gold_set | partial_set
            relevant_returned = len([memory_id for memory_id in returned_ids if memory_id in relevant_set])
            gold_returned = len([memory_id for memory_id in returned_ids if memory_id in gold_set])
            keyword_total = len(on_case["required_keywords"])
            keyword_found = keyword_found_count(on_case["required_keywords"], returned_text)
            row = {
                "stable_case_id": on_case["stable_case_id"],
                "cluster_id": on_case["cluster_id"],
                "cluster_title": on_case["cluster_title"],
                "question_id": on_case["question_id"],
                "variant_index": on_case["variant_index"],
                "precision": safe_div(relevant_returned, len(returned_ids)),
                "recall": safe_div(relevant_returned, len(relevant_set)),
                "gold_recall": safe_div(gold_returned, len(gold_set)),
                "keyword_coverage": safe_div(keyword_found, keyword_total),
                "ndcg": compute_ndcg(returned_ids, runtime_relevance),
                "returned_tokens": returned_tokens,
                "baseline_tokens": baseline_tokens,
                "saved_tokens": saved_tokens,
                "returned_ids": returned_ids,
                "gold_runtime_ids": gold_runtime_ids,
                "partial_runtime_ids": partial_runtime_ids,
            }
            handle.write(json.dumps(row, sort_keys=True))
            handle.write("\n")


def ingest_report(db_path: Path, report: dict[str, Any]) -> None:
    summary = report["summary"]
    off_phase = report["off_phase"]
    run_manifest = report["run_manifest"]
    generator_manifest = report.get("generator_manifest", {})
    created_at = run_manifest.get("generated_at") or run_manifest.get("created_at") or run_manifest.get("started_at") or ""

    conn = sqlite3.connect(str(db_path))
    try:
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS benchmark_runs (
              id INTEGER PRIMARY KEY AUTOINCREMENT,
              workspace TEXT NOT NULL,
              run_id TEXT NOT NULL,
              seed_count INTEGER NOT NULL DEFAULT 0,
              case_count INTEGER NOT NULL DEFAULT 0,
              case_limit INTEGER NOT NULL DEFAULT 0,
              top_k INTEGER NOT NULL DEFAULT 0,
              budget INTEGER NOT NULL DEFAULT 0,
              seed_duration_ms INTEGER NOT NULL DEFAULT 0,
              on_duration_ms INTEGER NOT NULL DEFAULT 0,
              off_duration_ms INTEGER NOT NULL DEFAULT 0,
              precision REAL NOT NULL DEFAULT 0,
              recall REAL NOT NULL DEFAULT 0,
              gold_recall REAL NOT NULL DEFAULT 0,
              keyword_coverage REAL NOT NULL DEFAULT 0,
              ndcg REAL NOT NULL DEFAULT 0,
              f1 REAL NOT NULL DEFAULT 0,
              token_efficiency REAL NOT NULL DEFAULT 0,
              baseline_tokens INTEGER NOT NULL DEFAULT 0,
              returned_tokens INTEGER NOT NULL DEFAULT 0,
              saved_tokens INTEGER NOT NULL DEFAULT 0,
              cost_with_memory REAL NOT NULL DEFAULT 0,
              cost_without_memory REAL NOT NULL DEFAULT 0,
              cost_saved REAL NOT NULL DEFAULT 0,
              cost_saved_pct REAL NOT NULL DEFAULT 0,
              combined_score REAL NOT NULL DEFAULT 0,
              verdict TEXT NOT NULL DEFAULT '',
              off_cases INTEGER NOT NULL DEFAULT 0,
              off_disabled_count INTEGER NOT NULL DEFAULT 0,
              off_all_disabled INTEGER NOT NULL DEFAULT 0,
              off_returned_tokens INTEGER NOT NULL DEFAULT 0,
              off_baseline_tokens INTEGER NOT NULL DEFAULT 0,
              off_saved_tokens INTEGER NOT NULL DEFAULT 0,
              generator_manifest_json TEXT NOT NULL DEFAULT '{}',
              run_manifest_json TEXT NOT NULL DEFAULT '{}',
              clusters_json TEXT NOT NULL DEFAULT '[]',
              created_at TEXT NOT NULL,
              UNIQUE(workspace, run_id)
            )
            """
        )
        conn.execute(
            """
            INSERT INTO benchmark_runs (
              workspace, run_id, seed_count, case_count, case_limit, top_k, budget,
              seed_duration_ms, on_duration_ms, off_duration_ms,
              precision, recall, gold_recall, keyword_coverage, ndcg, f1, token_efficiency,
              baseline_tokens, returned_tokens, saved_tokens,
              cost_with_memory, cost_without_memory, cost_saved, cost_saved_pct,
              combined_score, verdict,
              off_cases, off_disabled_count, off_all_disabled, off_returned_tokens, off_baseline_tokens, off_saved_tokens,
              generator_manifest_json, run_manifest_json, clusters_json, created_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(workspace, run_id) DO UPDATE SET
              seed_count = excluded.seed_count,
              case_count = excluded.case_count,
              case_limit = excluded.case_limit,
              top_k = excluded.top_k,
              budget = excluded.budget,
              seed_duration_ms = excluded.seed_duration_ms,
              on_duration_ms = excluded.on_duration_ms,
              off_duration_ms = excluded.off_duration_ms,
              precision = excluded.precision,
              recall = excluded.recall,
              gold_recall = excluded.gold_recall,
              keyword_coverage = excluded.keyword_coverage,
              ndcg = excluded.ndcg,
              f1 = excluded.f1,
              token_efficiency = excluded.token_efficiency,
              baseline_tokens = excluded.baseline_tokens,
              returned_tokens = excluded.returned_tokens,
              saved_tokens = excluded.saved_tokens,
              cost_with_memory = excluded.cost_with_memory,
              cost_without_memory = excluded.cost_without_memory,
              cost_saved = excluded.cost_saved,
              cost_saved_pct = excluded.cost_saved_pct,
              combined_score = excluded.combined_score,
              verdict = excluded.verdict,
              off_cases = excluded.off_cases,
              off_disabled_count = excluded.off_disabled_count,
              off_all_disabled = excluded.off_all_disabled,
              off_returned_tokens = excluded.off_returned_tokens,
              off_baseline_tokens = excluded.off_baseline_tokens,
              off_saved_tokens = excluded.off_saved_tokens,
              generator_manifest_json = excluded.generator_manifest_json,
              run_manifest_json = excluded.run_manifest_json,
              clusters_json = excluded.clusters_json,
              created_at = excluded.created_at
            """,
            (
                report["workspace"],
                report["run_id"],
                run_manifest.get("seed_count", 0),
                summary["cases"],
                int(run_manifest.get("case_limit") or 0),
                int(run_manifest.get("top_k") or 0),
                int(run_manifest.get("budget") or 0),
                int(run_manifest.get("seed_duration_ms") or 0),
                int(run_manifest.get("on_duration_ms") or 0),
                int(run_manifest.get("off_duration_ms") or 0),
                summary["precision"],
                summary["recall"],
                summary["gold_recall"],
                summary["keyword_coverage"],
                summary["ndcg"],
                summary["f1"],
                summary["token_efficiency"],
                summary["baseline_tokens"],
                summary["returned_tokens"],
                summary["saved_tokens"],
                summary["cost_with_memory"],
                summary["cost_without_memory"],
                summary["cost_saved"],
                summary["cost_saved_pct"],
                summary["combined_score"],
                summary["verdict"],
                off_phase["cases"],
                off_phase["disabled_count"],
                bool_to_int(off_phase["all_disabled"]),
                off_phase["returned_tokens"],
                off_phase["baseline_tokens"],
                off_phase["saved_tokens"],
                json.dumps(generator_manifest, sort_keys=True),
                json.dumps(run_manifest, sort_keys=True),
                json.dumps(report["clusters"], sort_keys=True),
                created_at,
            ),
        )
        conn.commit()
    finally:
        conn.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-dir", type=Path, required=True, help="Benchmark run directory under benchmark/results/")
    parser.add_argument("--db", type=Path, help="Override the SQLite database path from run_manifest.json")
    parser.add_argument(
        "--ingest",
        action="store_true",
        help="Persist the aggregate benchmark report into benchmark_runs using the selected DB path",
    )
    parser.add_argument(
        "--output",
        type=Path,
        help="Output file for the aggregate score report (default: <run-dir>/score_report.json)",
    )
    parser.add_argument(
        "--format",
        choices=("json", "raw"),
        default="json",
        help="Output format for stdout",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    output_path = args.output or (args.run_dir / "score_report.json")
    report = score_run(args.run_dir, args.db)
    write_case_report(args.run_dir / "score_cases.jsonl", args.run_dir, args.db)
    output_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if args.ingest:
        db_path = args.db or Path(report["db_path"])
        ingest_report(db_path, report)

    if args.format == "raw":
        summary = report["summary"]
        print(f"run_id: {report['run_id']}")
        print(f"workspace: {report['workspace']}")
        print(f"cases: {summary['cases']}")
        print(f"combined_score: {summary['combined_score']:.4f}")
        print(f"verdict: {summary['verdict']}")
        print(f"precision: {summary['precision']:.4f}")
        print(f"recall: {summary['recall']:.4f}")
        print(f"gold_recall: {summary['gold_recall']:.4f}")
        print(f"keyword_coverage: {summary['keyword_coverage']:.4f}")
        print(f"ndcg: {summary['ndcg']:.4f}")
        print(f"f1: {summary['f1']:.4f}")
        print(f"token_efficiency: {summary['token_efficiency']:.4f}")
        print(f"cost_saved_pct: {summary['cost_saved_pct']:.4f}")
        if args.ingest:
            print(f"ingested_db: {args.db or Path(report['db_path'])}")
        print(f"report: {output_path}")
        return

    print(json.dumps(report, sort_keys=True))


if __name__ == "__main__":
    main()
