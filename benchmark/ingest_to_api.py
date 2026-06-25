import json
import urllib.request
from pathlib import Path

report_path = Path("/Users/time/.gemini/antigravity-ide/scratch/score_report.json")
with report_path.open("r", encoding="utf-8") as f:
    report = json.load(f)

summary = report["summary"]
off_phase = report["off_phase"]
run_manifest = report["run_manifest"]

flat = {
    "workspace": report["workspace"],
    "run_id": report["run_id"],
    "seed_count": run_manifest.get("seed_count", 0),
    "case_count": summary["cases"],
    "case_limit": int(run_manifest.get("case_limit") or 0),
    "top_k": int(run_manifest.get("top_k") or 0),
    "budget": int(run_manifest.get("budget") or 0),
    "seed_duration_ms": int(run_manifest.get("seed_duration_ms") or 0),
    "on_duration_ms": int(run_manifest.get("on_duration_ms") or 0),
    "off_duration_ms": int(run_manifest.get("off_duration_ms") or 0),
    "precision": summary["precision"],
    "recall": summary["recall"],
    "gold_recall": summary["gold_recall"],
    "keyword_coverage": summary["keyword_coverage"],
    "ndcg": summary["ndcg"],
    "f1": summary["f1"],
    "token_efficiency": summary["token_efficiency"],
    "baseline_tokens": summary["baseline_tokens"],
    "returned_tokens": summary["returned_tokens"],
    "saved_tokens": summary["saved_tokens"],
    "cost_with_memory": summary["cost_with_memory"],
    "cost_without_memory": summary["cost_without_memory"],
    "cost_saved": summary["cost_saved"],
    "cost_saved_pct": summary["cost_saved_pct"],
    "combined_score": summary["combined_score"],
    "verdict": summary["verdict"],
    "off_cases": off_phase["cases"],
    "off_disabled_count": off_phase["disabled_count"],
    "off_all_disabled": off_phase["all_disabled"],
    "off_returned_tokens": off_phase["returned_tokens"],
    "off_baseline_tokens": off_phase["baseline_tokens"],
    "off_saved_tokens": off_phase["saved_tokens"],
    "task_success_rate": summary["task_success_rate"],
    "off_task_success_rate": summary["off_task_success_rate"],
    "task_success_delta": summary["task_success_delta"],
    "answer_fact_coverage": summary["answer_fact_coverage"],
    "off_answer_fact_coverage": summary["off_answer_fact_coverage"],
    "answer_fact_coverage_delta": summary["answer_fact_coverage_delta"],
    "answer_completeness": summary["answer_completeness"],
    "off_answer_completeness": summary["off_answer_completeness"],
    "answer_completeness_delta": summary["answer_completeness_delta"],
    "avg_on_runtime_ms": summary["avg_on_runtime_ms"],
    "avg_off_runtime_ms": summary["avg_off_runtime_ms"],
    "runtime_delta_ms": summary["runtime_delta_ms"],
    "avg_on_investigation_effort": summary["avg_on_investigation_effort"],
    "avg_off_investigation_effort": summary["avg_off_investigation_effort"],
    "investigation_effort_delta": summary["investigation_effort_delta"],
    "continuation_score": summary["continuation_score"],
    "continuation_verdict": summary["continuation_verdict"],
    "generator_manifest": report.get("generator_manifest", {}),
    "run_manifest": report["run_manifest"],
    "clusters": report["clusters"],
}

created_at = run_manifest.get("generated_at") or run_manifest.get("created_at") or run_manifest.get("started_at") or ""
flat["created_at"] = created_at

req_data = json.dumps(flat).encode("utf-8")
url = "http://localhost:3211/api/v1/benchmark/ingest"
req = urllib.request.Request(url, data=req_data, headers={"Content-Type": "application/json"}, method="POST")

import urllib.error
try:
    with urllib.request.urlopen(req) as res:
        print("Status Code:", res.status)
        print("Response:", res.read().decode("utf-8"))
except urllib.error.HTTPError as e:
    print("HTTP Error:", e.code, e.reason)
    print("Response body:", e.read().decode("utf-8"))
except Exception as e:
    print("Error POSTing:", e)
