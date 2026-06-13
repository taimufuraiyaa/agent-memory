#!/usr/bin/env python3
"""Score benchmark ON/OFF results into continuation and retrieval summaries."""

from __future__ import annotations

import argparse
import json
import math
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Any


PRICE_PER_1K_TOKENS = 0.03
ESTIMATED_TOKENS_PER_EFFORT_UNIT = 200.0
COMBINED_WEIGHTS = {
    "ndcg": 0.25,
    "keyword_coverage": 0.20,
    "precision": 0.15,
    "gold_recall": 0.15,
    "token_efficiency": 0.15,
    "f1": 0.10,
}
CONTINUATION_WEIGHTS = {
    "task_success_delta": 0.30,
    "answer_fact_coverage_delta": 0.20,
    "answer_completeness_delta": 0.15,
    "locator_success_delta": 0.15,
    "verification_effort_delta": 0.10,
    "operational_cost_delta": 0.10,
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


def token_cost(token_total: float) -> float:
    return (token_total / 1000.0) * PRICE_PER_1K_TOKENS


def symmetric_delta_ratio(baseline: float, variant: float) -> float:
    denominator = max(abs(baseline), abs(variant), 1.0)
    return (baseline - variant) / denominator


def verdict_for(score: float) -> str:
    if score >= 0.5:
        return "STRONG BENEFIT"
    if score >= 0.2:
        return "GOOD BENEFIT"
    if score > 0:
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


def locator_found_count(expected_locators: list[str], text: str) -> int:
    haystack = text.lower()
    found = 0
    for locator in expected_locators:
        if locator.lower() in haystack:
            found += 1
    return found


def answer_text(case_row: dict[str, Any]) -> str:
    text = case_row.get("answer_text")
    if isinstance(text, str) and text.strip():
        return text
    result_data = case_row.get("result_data", {})
    context_block = result_data.get("context_block")
    if isinstance(context_block, str):
        return context_block
    return ""


def fact_found_count(expected_facts: list[str], text: str) -> int:
    haystack = text.lower()
    found = 0
    for fact in expected_facts:
        if fact.lower() in haystack:
            found += 1
    return found


def fact_group_found_count(expected_fact_groups: list[list[str]], text: str) -> int:
    haystack = text.lower()
    found = 0
    for group in expected_fact_groups:
        if not group:
            continue
        if all(part.lower() in haystack for part in group):
            found += 1
    return found


def fetch_token_metrics(
    db_path: Path,
    workspace: str,
    run_label: str,
    memory_enabled: bool,
    operation: str = "recall",
    fallback_db_path: Path | None = None,
) -> list[TokenMetricRow]:
    import sys
    if not db_path.is_file():
        if fallback_db_path and fallback_db_path.is_file() and fallback_db_path != db_path:
            sys.stderr.write(f"DEBUG fetch_token_metrics: db_path {db_path} does not exist, falling back to {fallback_db_path}\n")
            db_path = fallback_db_path
        else:
            sys.stderr.write(f"DEBUG fetch_token_metrics: db_path {db_path} does not exist, returning empty rows\n")
            return []
    db_uri = f"file:{str(db_path)}?mode=ro&immutable=1"
    sys.stderr.write(f"DEBUG fetch_token_metrics: db_path={db_path}, db_uri={db_uri}, workspace={workspace}, run_label={run_label}, memory_enabled={memory_enabled}\n")
    conn = sqlite3.connect(db_uri, uri=True)
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
    except sqlite3.OperationalError as e:
        if "no such table: token_metrics" in str(e) and fallback_db_path and fallback_db_path.is_file() and fallback_db_path != db_path:
            sys.stderr.write(f"DEBUG fetch_token_metrics: table token_metrics not found in {db_path}, falling back to {fallback_db_path}\n")
            try:
                conn.close()
            except Exception:
                pass
            return fetch_token_metrics(fallback_db_path, workspace, run_label, memory_enabled, operation)
        sys.stderr.write(f"WARNING: fetch_token_metrics failed: {e}\n")
        return []
    finally:
        try:
            conn.close()
        except Exception:
            pass
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


def runtime_ms(case_row: dict[str, Any]) -> int:
    value = case_row.get("elapsed_ms")
    if isinstance(value, int):
        return value
    return 0


def retrieved_hit_count(case_row: dict[str, Any]) -> int:
    trace = case_row.get("trace_summary", {})
    value = trace.get("retrieved_hit_count")
    if isinstance(value, int):
        return value
    hits = case_row.get("result_data", {}).get("hits", [])
    if isinstance(hits, list):
        return len(hits)
    return 0


def lookup_effort_units(case_row: dict[str, Any]) -> int:
    trace = case_row.get("trace_summary", {})
    value = trace.get("lookup_effort_units")
    if isinstance(value, int):
        retrieved = trace.get("retrieved_hit_count")
        if retrieved is not None and value == retrieved and value > 1:
            return 1
        return value
    return 1 if case_row.get("memory_enabled") else 0


def unique_preserve_order(values: list[str]) -> list[str]:
    out: list[str] = []
    for value in values:
        if value and value not in out:
            out.append(value)
    return out


def locator_targets(case_row: dict[str, Any]) -> list[str]:
    explicit = case_row.get("expected_locator_targets")
    if isinstance(explicit, list) and explicit:
        return unique_preserve_order([str(item) for item in explicit if str(item).strip()])
    merged: list[str] = []
    for key in ("expected_files", "expected_commands"):
        value = case_row.get(key, [])
        if isinstance(value, list):
            merged.extend(str(item) for item in value if str(item).strip())
    return unique_preserve_order(merged)


def aggregate_case_rows(case_rows: list[dict[str, Any]]) -> dict[str, Any]:
    relevant_returned_sum = 0
    returned_total_sum = 0
    relevant_total_sum = 0
    gold_returned_sum = 0
    gold_total_sum = 0
    keyword_found_sum = 0
    keyword_total_sum = 0
    ndcg_sum = 0.0
    on_returned_tokens_sum = 0
    on_baseline_tokens_sum = 0
    off_returned_tokens_sum = 0
    off_baseline_tokens_sum = 0
    on_fact_found_sum = 0
    off_fact_found_sum = 0
    fact_total_sum = 0
    on_complete_sum = 0
    off_complete_sum = 0
    fact_group_total_sum = 0
    on_success_sum = 0
    off_success_sum = 0
    on_effort_sum = 0
    off_effort_sum = 0
    on_runtime_sum = 0
    off_runtime_sum = 0
    locator_found_on_sum = 0
    locator_found_off_sum = 0
    locator_total_sum = 0
    on_verification_effort_sum = 0
    off_verification_effort_sum = 0
    on_rediscovery_effort_sum = 0
    off_rediscovery_effort_sum = 0
    on_operational_effort_sum = 0
    off_operational_effort_sum = 0
    on_operational_cost_sum = 0.0
    off_operational_cost_sum = 0.0
    amortized_acquisition_cost_sum = 0.0
    memory_roi_sum = 0.0

    for row in case_rows:
        relevant_returned_sum += row["relevant_returned"]
        returned_total_sum += row["returned_count"]
        relevant_total_sum += row["relevant_total"]
        gold_returned_sum += row["gold_returned"]
        gold_total_sum += row["gold_total"]
        keyword_found_sum += row["keyword_found"]
        keyword_total_sum += row["keyword_total"]
        ndcg_sum += row["ndcg"]
        on_returned_tokens_sum += row["on_returned_tokens"]
        on_baseline_tokens_sum += row["on_baseline_tokens"]
        off_returned_tokens_sum += row["off_returned_tokens"]
        off_baseline_tokens_sum += row["off_baseline_tokens"]
        on_fact_found_sum += row["on_fact_found"]
        off_fact_found_sum += row["off_fact_found"]
        fact_total_sum += row["fact_total"]
        on_complete_sum += row["on_complete_groups"]
        off_complete_sum += row["off_complete_groups"]
        fact_group_total_sum += row["fact_group_total"]
        on_success_sum += row["on_task_success"]
        off_success_sum += row["off_task_success"]
        on_effort_sum += row["on_investigation_effort"]
        off_effort_sum += row["off_investigation_effort"]
        on_runtime_sum += row["on_runtime_ms"]
        off_runtime_sum += row["off_runtime_ms"]
        locator_found_on_sum += row["on_locator_found"]
        locator_found_off_sum += row["off_locator_found"]
        locator_total_sum += row["locator_total"]
        on_verification_effort_sum += row["on_verification_effort"]
        off_verification_effort_sum += row["off_verification_effort"]
        on_rediscovery_effort_sum += row["on_rediscovery_effort"]
        off_rediscovery_effort_sum += row["off_rediscovery_effort"]
        on_operational_effort_sum += row["on_operational_effort"]
        off_operational_effort_sum += row["off_operational_effort"]
        on_operational_cost_sum += row["on_operational_cost"]
        off_operational_cost_sum += row["off_operational_cost"]
        amortized_acquisition_cost_sum += row["amortized_acquisition_cost"]
        memory_roi_sum += row["memory_roi"]

    precision = safe_div(relevant_returned_sum, returned_total_sum)
    recall = safe_div(relevant_returned_sum, relevant_total_sum)
    gold_recall = safe_div(gold_returned_sum, gold_total_sum)
    keyword_coverage = safe_div(keyword_found_sum, keyword_total_sum)
    ndcg = safe_div(ndcg_sum, len(case_rows))
    f1 = harmonic_mean(precision, recall)
    saved_tokens_sum = off_returned_tokens_sum - on_returned_tokens_sum
    token_efficiency = safe_div(saved_tokens_sum, off_returned_tokens_sum)
    cost_without_memory = token_cost(off_returned_tokens_sum)
    cost_with_memory = token_cost(on_returned_tokens_sum)
    cost_saved = cost_without_memory - cost_with_memory
    cost_saved_pct = safe_div(cost_saved, cost_without_memory)
    on_answer_fact_coverage = safe_div(on_fact_found_sum, fact_total_sum)
    off_answer_fact_coverage = safe_div(off_fact_found_sum, fact_total_sum)
    answer_fact_coverage_delta = on_answer_fact_coverage - off_answer_fact_coverage
    on_answer_completeness = safe_div(on_complete_sum, fact_group_total_sum)
    off_answer_completeness = safe_div(off_complete_sum, fact_group_total_sum)
    answer_completeness_delta = on_answer_completeness - off_answer_completeness
    on_task_success_rate = safe_div(on_success_sum, len(case_rows))
    off_task_success_rate = safe_div(off_success_sum, len(case_rows))
    task_success_delta = on_task_success_rate - off_task_success_rate
    avg_on_runtime_ms = safe_div(on_runtime_sum, len(case_rows))
    avg_off_runtime_ms = safe_div(off_runtime_sum, len(case_rows))
    runtime_delta_ms = avg_off_runtime_ms - avg_on_runtime_ms
    runtime_delta_ratio = symmetric_delta_ratio(off_runtime_sum, on_runtime_sum)
    avg_on_investigation_effort = safe_div(on_effort_sum, len(case_rows))
    avg_off_investigation_effort = safe_div(off_effort_sum, len(case_rows))
    investigation_effort_delta = avg_off_investigation_effort - avg_on_investigation_effort
    investigation_effort_delta_ratio = symmetric_delta_ratio(off_effort_sum, on_effort_sum)
    locator_success_rate = safe_div(locator_found_on_sum, locator_total_sum)
    off_locator_success_rate = safe_div(locator_found_off_sum, locator_total_sum)
    locator_success_delta = locator_success_rate - off_locator_success_rate
    avg_on_verification_effort = safe_div(on_verification_effort_sum, len(case_rows))
    avg_off_verification_effort = safe_div(off_verification_effort_sum, len(case_rows))
    verification_effort_delta = avg_off_verification_effort - avg_on_verification_effort
    verification_effort_delta_ratio = symmetric_delta_ratio(
        off_verification_effort_sum, on_verification_effort_sum
    )
    avg_on_rediscovery_effort = safe_div(on_rediscovery_effort_sum, len(case_rows))
    avg_off_rediscovery_effort = safe_div(off_rediscovery_effort_sum, len(case_rows))
    rediscovery_effort_delta = avg_off_rediscovery_effort - avg_on_rediscovery_effort
    rediscovery_effort_delta_ratio = symmetric_delta_ratio(
        off_rediscovery_effort_sum, on_rediscovery_effort_sum
    )
    avg_on_operational_effort = safe_div(on_operational_effort_sum, len(case_rows))
    avg_off_operational_effort = safe_div(off_operational_effort_sum, len(case_rows))
    operational_effort_delta = avg_off_operational_effort - avg_on_operational_effort
    operational_effort_delta_ratio = symmetric_delta_ratio(
        off_operational_effort_sum, on_operational_effort_sum
    )
    operational_cost_with_memory = on_operational_cost_sum
    operational_cost_without_memory = off_operational_cost_sum
    operational_cost_saved = operational_cost_without_memory - operational_cost_with_memory
    operational_cost_saved_pct = safe_div(operational_cost_saved, operational_cost_without_memory)
    operational_cost_delta_ratio = symmetric_delta_ratio(
        operational_cost_without_memory, operational_cost_with_memory
    )

    combined_score = (
        ndcg * COMBINED_WEIGHTS["ndcg"]
        + keyword_coverage * COMBINED_WEIGHTS["keyword_coverage"]
        + precision * COMBINED_WEIGHTS["precision"]
        + gold_recall * COMBINED_WEIGHTS["gold_recall"]
        + token_efficiency * COMBINED_WEIGHTS["token_efficiency"]
        + f1 * COMBINED_WEIGHTS["f1"]
    )
    continuation_score = (
        task_success_delta * CONTINUATION_WEIGHTS["task_success_delta"]
        + answer_fact_coverage_delta * CONTINUATION_WEIGHTS["answer_fact_coverage_delta"]
        + answer_completeness_delta * CONTINUATION_WEIGHTS["answer_completeness_delta"]
        + locator_success_delta * CONTINUATION_WEIGHTS["locator_success_delta"]
        + verification_effort_delta_ratio * CONTINUATION_WEIGHTS["verification_effort_delta"]
        + operational_cost_delta_ratio * CONTINUATION_WEIGHTS["operational_cost_delta"]
    )
    continuation_verdict = verdict_for(continuation_score)

    return {
        "cases": len(case_rows),
        "task_success_rate": on_task_success_rate,
        "off_task_success_rate": off_task_success_rate,
        "task_success_delta": task_success_delta,
        "answer_fact_coverage": on_answer_fact_coverage,
        "off_answer_fact_coverage": off_answer_fact_coverage,
        "answer_fact_coverage_delta": answer_fact_coverage_delta,
        "answer_completeness": on_answer_completeness,
        "off_answer_completeness": off_answer_completeness,
        "answer_completeness_delta": answer_completeness_delta,
        "avg_on_runtime_ms": avg_on_runtime_ms,
        "avg_off_runtime_ms": avg_off_runtime_ms,
        "runtime_delta_ms": runtime_delta_ms,
        "runtime_delta_ratio": runtime_delta_ratio,
        "avg_on_investigation_effort": avg_on_investigation_effort,
        "avg_off_investigation_effort": avg_off_investigation_effort,
        "investigation_effort_delta": investigation_effort_delta,
        "investigation_effort_delta_ratio": investigation_effort_delta_ratio,
        "locator_success_rate": locator_success_rate,
        "off_locator_success_rate": off_locator_success_rate,
        "locator_success_delta": locator_success_delta,
        "avg_on_verification_effort": avg_on_verification_effort,
        "avg_off_verification_effort": avg_off_verification_effort,
        "verification_effort_delta": verification_effort_delta,
        "verification_effort_delta_ratio": verification_effort_delta_ratio,
        "avg_on_rediscovery_effort": avg_on_rediscovery_effort,
        "avg_off_rediscovery_effort": avg_off_rediscovery_effort,
        "rediscovery_effort_delta": rediscovery_effort_delta,
        "rediscovery_effort_delta_ratio": rediscovery_effort_delta_ratio,
        "avg_on_operational_effort": avg_on_operational_effort,
        "avg_off_operational_effort": avg_off_operational_effort,
        "operational_effort_delta": operational_effort_delta,
        "operational_effort_delta_ratio": operational_effort_delta_ratio,
        "operational_cost_with_memory": operational_cost_with_memory,
        "operational_cost_without_memory": operational_cost_without_memory,
        "operational_cost_saved": operational_cost_saved,
        "operational_cost_saved_pct": operational_cost_saved_pct,
        "amortized_acquisition_cost": amortized_acquisition_cost_sum,
        "memory_roi": memory_roi_sum,
        "continuation_score": continuation_score,
        "continuation_verdict": continuation_verdict,
        "precision": precision,
        "recall": recall,
        "gold_recall": gold_recall,
        "keyword_coverage": keyword_coverage,
        "ndcg": ndcg,
        "f1": f1,
        "token_efficiency": token_efficiency,
        "returned_tokens": on_returned_tokens_sum,
        "baseline_tokens": off_returned_tokens_sum,
        "saved_tokens": saved_tokens_sum,
        "on_baseline_tokens": on_baseline_tokens_sum,
        "off_baseline_tokens": off_baseline_tokens_sum,
        "off_returned_tokens": off_returned_tokens_sum,
        "cost_without_memory": cost_without_memory,
        "cost_with_memory": cost_with_memory,
        "cost_saved": cost_saved,
        "cost_saved_pct": cost_saved_pct,
        "combined_score": combined_score,
        "verdict": continuation_verdict,
    }


def score_run(run_dir: Path, db_path: Path | None) -> dict[str, Any]:
    run_manifest = load_json(run_dir / "run_manifest.json")
    workspace = run_manifest["workspace"]
    db_file = db_path or Path(run_manifest["db_path"])
    id_mapping: dict[str, str] = load_json(run_dir / "id_mapping.json")
    on_rows = load_jsonl(run_dir / "quality-on.jsonl")
    off_rows = load_jsonl(run_dir / "quality-off.jsonl")
    fixtures_file = Path(
        run_manifest.get("prior_session_fixtures_file")
        or run_manifest.get("seed_file")
        or (run_dir / "prior_session_fixtures.jsonl")
    )
    fixture_rows = load_jsonl(fixtures_file) if fixtures_file.is_file() else []
    fixtures_by_id = {row.get("stable_id"): row for row in fixture_rows if row.get("stable_id")}
    fixture_reuse_counts: dict[str, int] = {}
    for case in on_rows:
        for fixture_id in case.get("prior_fixture_ids", []):
            fixture_reuse_counts[fixture_id] = fixture_reuse_counts.get(fixture_id, 0) + 1

    manifest_db = Path(run_manifest["db_path"])
    on_token_rows = fetch_token_metrics(db_file, workspace, "benchmark-on", True, fallback_db_path=manifest_db)
    off_token_rows = fetch_token_metrics(db_file, workspace, "benchmark-off", False, fallback_db_path=manifest_db)

    per_case_rows: list[dict[str, Any]] = []
    off_rows_by_case = {row["stable_case_id"]: (index, row) for index, row in enumerate(off_rows)}
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
        on_returned_tokens, on_baseline_tokens, _ = case_token_totals(on_case)
        if on_returned_tokens is None or on_baseline_tokens is None:
            if len(on_token_rows) != len(on_rows):
                raise SystemExit(
                    f"ON token metric row count mismatch: expected {len(on_rows)}, got {len(on_token_rows)}"
                )
            token_row = on_token_rows[index]
            on_returned_tokens = token_row.returned_tokens
            on_baseline_tokens = token_row.baseline_tokens
        off_index, off_case = off_rows_by_case.get(on_case["stable_case_id"], (None, None))
        if off_case is None:
            raise SystemExit(f"missing OFF row for case {on_case['stable_case_id']}")
        off_case_returned_tokens, off_case_baseline_tokens, _ = case_token_totals(off_case)
        if off_case_returned_tokens is None or off_case_baseline_tokens is None:
            if len(off_token_rows) != len(off_rows):
                raise SystemExit(
                    f"OFF token metric row count mismatch: expected {len(off_rows)}, got {len(off_token_rows)}"
                )
            token_row = off_token_rows[off_index]
            off_case_returned_tokens = token_row.returned_tokens
            off_case_baseline_tokens = token_row.baseline_tokens
        gold_runtime_ids = [id_mapping[item] for item in on_case["gold_ids"]]
        partial_runtime_ids = [id_mapping[item] for item in on_case["partial_ids"]]
        runtime_relevance = {
            id_mapping[stable_id]: grade
            for stable_id, grade in on_case["relevance_grades"].items()
            if stable_id in id_mapping
        }

        hits = on_case.get("result_data", {}).get("hits", [])
        returned_ids = []
        gold_set = set(gold_runtime_ids)
        partial_set = set(partial_runtime_ids)
        relevant_set = gold_set | partial_set
        relevant_chunks = []
        for hit in hits:
            memory = hit.get("memory", {})
            memory_id = memory.get("id")
            if memory_id:
                returned_ids.append(memory_id)
            content = memory.get("content", "")
            if content and memory_id in relevant_set:
                relevant_chunks.append(content)

        returned_text = "\n".join(relevant_chunks)
        on_answer = answer_text(on_case)
        off_answer = answer_text(off_case)
        expected_facts = on_case.get("expected_facts", [])
        expected_fact_groups = on_case.get("expected_fact_groups", [])
        fact_total = len(expected_facts)
        fact_group_total = len(expected_fact_groups)
        on_fact_found = fact_found_count(expected_facts, on_answer)
        off_fact_found = fact_found_count(expected_facts, off_answer)
        on_complete_groups = fact_group_found_count(expected_fact_groups, on_answer)
        off_complete_groups = fact_group_found_count(expected_fact_groups, off_answer)
        on_completeness = safe_div(on_complete_groups, fact_group_total)
        off_completeness = safe_div(off_complete_groups, fact_group_total)
        on_success = 1 if on_completeness >= 1.0 or safe_div(on_fact_found, fact_total) >= 0.75 else 0
        off_success = 1 if off_completeness >= 1.0 or safe_div(off_fact_found, fact_total) >= 0.75 else 0
        relevant_returned = len([memory_id for memory_id in returned_ids if memory_id in relevant_set])
        gold_returned = len([memory_id for memory_id in returned_ids if memory_id in gold_set])
        keyword_total = len(on_case["required_keywords"])
        keyword_found = keyword_found_count(on_case["required_keywords"], returned_text)
        expected_locators = locator_targets(on_case)
        locator_total = len(expected_locators)
        on_locator_found = locator_found_count(expected_locators, on_answer)
        off_locator_found = locator_found_count(expected_locators, off_answer)
        on_lookup_effort = lookup_effort_units(on_case)
        off_lookup_effort = lookup_effort_units(off_case)
        on_verification_effort = max(0, fact_group_total - on_complete_groups)
        off_verification_effort = max(0, fact_group_total - off_complete_groups)
        on_rediscovery_effort = max(0, locator_total - on_locator_found) + max(0, len(gold_set) - gold_returned)
        off_rediscovery_effort = max(0, locator_total - off_locator_found) + len(gold_set)
        on_operational_effort = on_lookup_effort + on_verification_effort + on_rediscovery_effort
        off_operational_effort = off_lookup_effort + off_verification_effort + off_rediscovery_effort
        on_operational_cost = token_cost(
            float(on_returned_tokens) + (float(on_operational_effort) * ESTIMATED_TOKENS_PER_EFFORT_UNIT)
        )
        off_operational_cost = token_cost(
            float(off_case_returned_tokens) + (float(off_operational_effort) * ESTIMATED_TOKENS_PER_EFFORT_UNIT)
        )
        amortized_acquisition_cost = 0.0
        for fixture_id in on_case.get("prior_fixture_ids", []):
            fixture = fixtures_by_id.get(fixture_id)
            if not isinstance(fixture, dict):
                continue
            acquisition_profile = fixture.get("acquisition_profile", {})
            if not isinstance(acquisition_profile, dict):
                continue
            effort_units = float(acquisition_profile.get("effort_units") or 0.0)
            if effort_units <= 0:
                continue
            reuse_count = max(1, fixture_reuse_counts.get(fixture_id, 1))
            amortized_acquisition_cost += token_cost(
                (effort_units * ESTIMATED_TOKENS_PER_EFFORT_UNIT) / float(reuse_count)
            )
        memory_roi = (off_operational_cost - on_operational_cost) - amortized_acquisition_cost

        per_case_rows.append(
            {
                "stable_case_id": on_case["stable_case_id"],
                "cluster_id": on_case["cluster_id"],
                "cluster_title": on_case["cluster_title"],
                "question_id": on_case["question_id"],
                "variant_index": on_case["variant_index"],
                "expected_files": on_case.get("expected_files", []),
                "expected_commands": on_case.get("expected_commands", []),
                "expected_locator_targets": expected_locators,
                "expected_facts": expected_facts,
                "expected_fact_groups": expected_fact_groups,
                "fact_total": fact_total,
                "fact_group_total": fact_group_total,
                "on_fact_found": on_fact_found,
                "off_fact_found": off_fact_found,
                "on_answer_fact_coverage": safe_div(on_fact_found, fact_total),
                "off_answer_fact_coverage": safe_div(off_fact_found, fact_total),
                "answer_fact_coverage": safe_div(on_fact_found, fact_total),
                "off_answer_fact_coverage": safe_div(off_fact_found, fact_total),
                "on_complete_groups": on_complete_groups,
                "off_complete_groups": off_complete_groups,
                "on_answer_completeness": on_completeness,
                "off_answer_completeness": off_completeness,
                "answer_completeness": on_completeness,
                "off_answer_completeness": off_completeness,
                "on_task_success": on_success,
                "off_task_success": off_success,
                "on_runtime_ms": runtime_ms(on_case),
                "off_runtime_ms": runtime_ms(off_case),
                "on_investigation_effort": retrieved_hit_count(on_case),
                "off_investigation_effort": retrieved_hit_count(off_case),
                "on_lookup_effort": on_lookup_effort,
                "off_lookup_effort": off_lookup_effort,
                "locator_total": locator_total,
                "on_locator_found": on_locator_found,
                "off_locator_found": off_locator_found,
                "on_verification_effort": on_verification_effort,
                "off_verification_effort": off_verification_effort,
                "on_rediscovery_effort": on_rediscovery_effort,
                "off_rediscovery_effort": off_rediscovery_effort,
                "on_operational_effort": on_operational_effort,
                "off_operational_effort": off_operational_effort,
                "on_operational_cost": on_operational_cost,
                "off_operational_cost": off_operational_cost,
                "amortized_acquisition_cost": amortized_acquisition_cost,
                "memory_roi": memory_roi,
                "on_answer_text": on_answer,
                "off_answer_text": off_answer,
                "returned_ids": returned_ids,
                "gold_runtime_ids": gold_runtime_ids,
                "partial_runtime_ids": partial_runtime_ids,
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
                "returned_tokens": on_returned_tokens,
                "baseline_tokens": off_case_returned_tokens,
                "saved_tokens": off_case_returned_tokens - on_returned_tokens,
                "on_returned_tokens": on_returned_tokens,
                "on_baseline_tokens": on_baseline_tokens,
                "off_returned_tokens": off_case_returned_tokens,
                "off_baseline_tokens": off_case_baseline_tokens,
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
        "continuation_weights": CONTINUATION_WEIGHTS,
        "summary": aggregate,
        "off_phase": off_summary,
        "clusters": cluster_breakdown,
        "per_case_rows": per_case_rows,
        "per_case_report_path": str(run_dir / "score_cases.jsonl"),
    }


def write_case_report(path: Path, report: dict[str, Any]) -> None:
    with path.open("w", encoding="utf-8") as handle:
        for row in report.get("per_case_rows", []):
            handle.write(json.dumps(row, sort_keys=True))
            handle.write("\n")


def ensure_column(conn: sqlite3.Connection, table: str, column: str, alter_sql: str) -> None:
    rows = conn.execute(f"PRAGMA table_info({table})").fetchall()
    if any(row[1] == column for row in rows):
        return
    conn.execute(alter_sql)


def ingest_report(db_path: Path, report: dict[str, Any], workspace_override: str | None = None) -> None:
    summary = report["summary"]
    off_phase = report["off_phase"]
    run_manifest = report["run_manifest"]
    generator_manifest = report.get("generator_manifest", {})
    created_at = run_manifest.get("generated_at") or run_manifest.get("created_at") or run_manifest.get("started_at") or ""
    workspace = workspace_override or report["workspace"]
    persisted_manifest = dict(run_manifest)
    persisted_manifest["economic_summary"] = {
        "estimation_notes": {
            "effort_token_proxy": ESTIMATED_TOKENS_PER_EFFORT_UNIT,
            "retrieval_context_cost_is_secondary": True,
            "operational_cost_is_estimated": True,
            "acquisition_cost_is_estimated": True,
        },
        "locator_success_rate": summary["locator_success_rate"],
        "off_locator_success_rate": summary["off_locator_success_rate"],
        "locator_success_delta": summary["locator_success_delta"],
        "avg_on_verification_effort": summary["avg_on_verification_effort"],
        "avg_off_verification_effort": summary["avg_off_verification_effort"],
        "verification_effort_delta": summary["verification_effort_delta"],
        "avg_on_rediscovery_effort": summary["avg_on_rediscovery_effort"],
        "avg_off_rediscovery_effort": summary["avg_off_rediscovery_effort"],
        "rediscovery_effort_delta": summary["rediscovery_effort_delta"],
        "avg_on_operational_effort": summary["avg_on_operational_effort"],
        "avg_off_operational_effort": summary["avg_off_operational_effort"],
        "operational_effort_delta": summary["operational_effort_delta"],
        "operational_cost_with_memory": summary["operational_cost_with_memory"],
        "operational_cost_without_memory": summary["operational_cost_without_memory"],
        "operational_cost_saved": summary["operational_cost_saved"],
        "operational_cost_saved_pct": summary["operational_cost_saved_pct"],
        "amortized_acquisition_cost": summary["amortized_acquisition_cost"],
        "memory_roi": summary["memory_roi"],
    }

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
              task_success_rate REAL NOT NULL DEFAULT 0,
              off_task_success_rate REAL NOT NULL DEFAULT 0,
              task_success_delta REAL NOT NULL DEFAULT 0,
              answer_fact_coverage REAL NOT NULL DEFAULT 0,
              off_answer_fact_coverage REAL NOT NULL DEFAULT 0,
              answer_fact_coverage_delta REAL NOT NULL DEFAULT 0,
              answer_completeness REAL NOT NULL DEFAULT 0,
              off_answer_completeness REAL NOT NULL DEFAULT 0,
              answer_completeness_delta REAL NOT NULL DEFAULT 0,
              avg_on_runtime_ms REAL NOT NULL DEFAULT 0,
              avg_off_runtime_ms REAL NOT NULL DEFAULT 0,
              runtime_delta_ms REAL NOT NULL DEFAULT 0,
              avg_on_investigation_effort REAL NOT NULL DEFAULT 0,
              avg_off_investigation_effort REAL NOT NULL DEFAULT 0,
              investigation_effort_delta REAL NOT NULL DEFAULT 0,
              continuation_score REAL NOT NULL DEFAULT 0,
              continuation_verdict TEXT NOT NULL DEFAULT '',
              generator_manifest_json TEXT NOT NULL DEFAULT '{}',
              run_manifest_json TEXT NOT NULL DEFAULT '{}',
              clusters_json TEXT NOT NULL DEFAULT '[]',
              created_at TEXT NOT NULL,
              UNIQUE(workspace, run_id)
            )
            """
        )
        ensure_column(conn, "benchmark_runs", "task_success_rate", "ALTER TABLE benchmark_runs ADD COLUMN task_success_rate REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "off_task_success_rate", "ALTER TABLE benchmark_runs ADD COLUMN off_task_success_rate REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "task_success_delta", "ALTER TABLE benchmark_runs ADD COLUMN task_success_delta REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "answer_fact_coverage", "ALTER TABLE benchmark_runs ADD COLUMN answer_fact_coverage REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "off_answer_fact_coverage", "ALTER TABLE benchmark_runs ADD COLUMN off_answer_fact_coverage REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "answer_fact_coverage_delta", "ALTER TABLE benchmark_runs ADD COLUMN answer_fact_coverage_delta REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "answer_completeness", "ALTER TABLE benchmark_runs ADD COLUMN answer_completeness REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "off_answer_completeness", "ALTER TABLE benchmark_runs ADD COLUMN off_answer_completeness REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "answer_completeness_delta", "ALTER TABLE benchmark_runs ADD COLUMN answer_completeness_delta REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "avg_on_runtime_ms", "ALTER TABLE benchmark_runs ADD COLUMN avg_on_runtime_ms REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "avg_off_runtime_ms", "ALTER TABLE benchmark_runs ADD COLUMN avg_off_runtime_ms REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "runtime_delta_ms", "ALTER TABLE benchmark_runs ADD COLUMN runtime_delta_ms REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "avg_on_investigation_effort", "ALTER TABLE benchmark_runs ADD COLUMN avg_on_investigation_effort REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "avg_off_investigation_effort", "ALTER TABLE benchmark_runs ADD COLUMN avg_off_investigation_effort REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "investigation_effort_delta", "ALTER TABLE benchmark_runs ADD COLUMN investigation_effort_delta REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "continuation_score", "ALTER TABLE benchmark_runs ADD COLUMN continuation_score REAL NOT NULL DEFAULT 0")
        ensure_column(conn, "benchmark_runs", "continuation_verdict", "ALTER TABLE benchmark_runs ADD COLUMN continuation_verdict TEXT NOT NULL DEFAULT ''")
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
              task_success_rate, off_task_success_rate, task_success_delta,
              answer_fact_coverage, off_answer_fact_coverage, answer_fact_coverage_delta,
              answer_completeness, off_answer_completeness, answer_completeness_delta,
              avg_on_runtime_ms, avg_off_runtime_ms, runtime_delta_ms,
              avg_on_investigation_effort, avg_off_investigation_effort, investigation_effort_delta,
              continuation_score, continuation_verdict,
              generator_manifest_json, run_manifest_json, clusters_json, created_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
              task_success_rate = excluded.task_success_rate,
              off_task_success_rate = excluded.off_task_success_rate,
              task_success_delta = excluded.task_success_delta,
              answer_fact_coverage = excluded.answer_fact_coverage,
              off_answer_fact_coverage = excluded.off_answer_fact_coverage,
              answer_fact_coverage_delta = excluded.answer_fact_coverage_delta,
              answer_completeness = excluded.answer_completeness,
              off_answer_completeness = excluded.off_answer_completeness,
              answer_completeness_delta = excluded.answer_completeness_delta,
              avg_on_runtime_ms = excluded.avg_on_runtime_ms,
              avg_off_runtime_ms = excluded.avg_off_runtime_ms,
              runtime_delta_ms = excluded.runtime_delta_ms,
              avg_on_investigation_effort = excluded.avg_on_investigation_effort,
              avg_off_investigation_effort = excluded.avg_off_investigation_effort,
              investigation_effort_delta = excluded.investigation_effort_delta,
              continuation_score = excluded.continuation_score,
              continuation_verdict = excluded.continuation_verdict,
              generator_manifest_json = excluded.generator_manifest_json,
              run_manifest_json = excluded.run_manifest_json,
              clusters_json = excluded.clusters_json,
              created_at = excluded.created_at
            """,
            (
                workspace,
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
                summary["task_success_rate"],
                summary["off_task_success_rate"],
                summary["task_success_delta"],
                summary["answer_fact_coverage"],
                summary["off_answer_fact_coverage"],
                summary["answer_fact_coverage_delta"],
                summary["answer_completeness"],
                summary["off_answer_completeness"],
                summary["answer_completeness_delta"],
                summary["avg_on_runtime_ms"],
                summary["avg_off_runtime_ms"],
                summary["runtime_delta_ms"],
                summary["avg_on_investigation_effort"],
                summary["avg_off_investigation_effort"],
                summary["investigation_effort_delta"],
                summary["continuation_score"],
                summary["continuation_verdict"],
                json.dumps(generator_manifest, sort_keys=True),
                json.dumps(persisted_manifest, sort_keys=True),
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
        "--ingest-db",
        type=Path,
        help="Optional SQLite database path to ingest into when it differs from the scoring DB",
    )
    parser.add_argument(
        "--ingest-workspace",
        type=str,
        help="Optional workspace name override used only for benchmark_runs ingest",
    )
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
    import sys
    args = parse_args()
    output_path = args.output or (args.run_dir / "score_report.json")
    report = score_run(args.run_dir, args.db)
    per_case_rows = report.pop("per_case_rows", [])
    try:
        write_case_report(output_path.parent / "score_cases.jsonl", {"per_case_rows": per_case_rows})
    except Exception as e:
        sys.stderr.write(f"Warning: could not write score_cases.jsonl: {e}\n")
    try:
        output_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    except Exception as e:
        sys.stderr.write(f"Warning: could not write score_report.json: {e}\n")
    if args.ingest:
        try:
            db_path = args.ingest_db or args.db or Path(report["db_path"])
            ingest_report(db_path, report, args.ingest_workspace)
        except Exception as e:
            sys.stderr.write(f"Warning: could not ingest report: {e}\n")

    if args.format == "raw":
        summary = report["summary"]
        print(f"run_id: {report['run_id']}")
        print(f"workspace: {report['workspace']}")
        print(f"cases: {summary['cases']}")
        print(f"continuation_score: {summary['continuation_score']:.4f}")
        print(f"continuation_verdict: {summary['continuation_verdict']}")
        print(f"task_success_rate: {summary['task_success_rate']:.4f}")
        print(f"task_success_delta: {summary['task_success_delta']:.4f}")
        print(f"answer_fact_coverage: {summary['answer_fact_coverage']:.4f}")
        print(f"answer_fact_coverage_delta: {summary['answer_fact_coverage_delta']:.4f}")
        print(f"answer_completeness: {summary['answer_completeness']:.4f}")
        print(f"answer_completeness_delta: {summary['answer_completeness_delta']:.4f}")
        print(f"locator_success_delta: {summary['locator_success_delta']:.4f}")
        print(f"verification_effort_delta: {summary['verification_effort_delta']:.4f}")
        print(f"rediscovery_effort_delta: {summary['rediscovery_effort_delta']:.4f}")
        print(f"runtime_delta_ms: {summary['runtime_delta_ms']:.2f}")
        print(f"investigation_effort_delta: {summary['investigation_effort_delta']:.4f}")
        print(f"operational_cost_saved: {summary['operational_cost_saved']:.4f}")
        print(f"operational_cost_saved_pct: {summary['operational_cost_saved_pct']:.4f}")
        print(f"amortized_acquisition_cost: {summary['amortized_acquisition_cost']:.4f}")
        print(f"memory_roi: {summary['memory_roi']:.4f}")
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
            print(f"ingested_db: {args.ingest_db or args.db or Path(report['db_path'])}")
            if args.ingest_workspace:
                print(f"ingested_workspace: {args.ingest_workspace}")
        print(f"report: {output_path}")
        return

    print(json.dumps(report, sort_keys=True))


if __name__ == "__main__":
    main()
