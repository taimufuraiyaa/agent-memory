import Foundation

let reportPath = ProcessInfo.processInfo.environment["AGENT_MEMORY_SCORE_REPORT"]
    ?? FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".gemini/antigravity-ide/scratch/score_report.json").path
let reportData = try Data(contentsOf: URL(fileURLWithPath: reportPath))

guard var report = try JSONSerialization.jsonObject(with: reportData, options: []) as? [String: Any] else {
    print("Failed to parse report as dictionary")
    exit(1)
}

let summary = report["summary"] as? [String: Any] ?? [:]
let offPhase = report["off_phase"] as? [String: Any] ?? [:]
let runManifest = report["run_manifest"] as? [String: Any] ?? [:]

var flat: [String: Any] = [:]
flat["workspace"] = report["workspace"]
flat["run_id"] = report["run_id"]
flat["seed_count"] = runManifest["seed_count"]
flat["case_count"] = summary["cases"]
flat["case_limit"] = runManifest["case_limit"] ?? 0
flat["top_k"] = runManifest["top_k"] ?? 0
flat["budget"] = runManifest["budget"] ?? 0
flat["seed_duration_ms"] = runManifest["seed_duration_ms"] ?? 0
flat["on_duration_ms"] = runManifest["on_duration_ms"] ?? 0
flat["off_duration_ms"] = runManifest["off_duration_ms"] ?? 0
flat["precision"] = summary["precision"]
flat["recall"] = summary["recall"]
flat["gold_recall"] = summary["gold_recall"]
flat["keyword_coverage"] = summary["keyword_coverage"]
flat["ndcg"] = summary["ndcg"]
flat["f1"] = summary["f1"]
flat["token_efficiency"] = summary["token_efficiency"]
flat["baseline_tokens"] = summary["baseline_tokens"]
flat["returned_tokens"] = summary["returned_tokens"]
flat["saved_tokens"] = summary["saved_tokens"]
flat["cost_with_memory"] = summary["cost_with_memory"]
flat["cost_without_memory"] = summary["cost_without_memory"]
flat["cost_saved"] = summary["cost_saved"]
flat["cost_saved_pct"] = summary["cost_saved_pct"]
flat["combined_score"] = summary["combined_score"]
flat["verdict"] = summary["verdict"]
flat["off_cases"] = offPhase["cases"]
flat["off_disabled_count"] = offPhase["disabled_count"]
flat["off_all_disabled"] = offPhase["all_disabled"]
flat["off_returned_tokens"] = offPhase["returned_tokens"]
flat["off_baseline_tokens"] = offPhase["baseline_tokens"]
flat["off_saved_tokens"] = offPhase["saved_tokens"]
flat["task_success_rate"] = summary["task_success_rate"]
flat["off_task_success_rate"] = summary["off_task_success_rate"]
flat["task_success_delta"] = summary["task_success_delta"]
flat["answer_fact_coverage"] = summary["answer_fact_coverage"]
flat["off_answer_fact_coverage"] = summary["off_answer_fact_coverage"]
flat["answer_fact_coverage_delta"] = summary["answer_fact_coverage_delta"]
flat["answer_completeness"] = summary["answer_completeness"]
flat["off_answer_completeness"] = summary["off_answer_completeness"]
flat["answer_completeness_delta"] = summary["answer_completeness_delta"]
flat["avg_on_runtime_ms"] = summary["avg_on_runtime_ms"]
flat["avg_off_runtime_ms"] = summary["avg_off_runtime_ms"]
flat["runtime_delta_ms"] = summary["runtime_delta_ms"]
flat["avg_on_investigation_effort"] = summary["avg_on_investigation_effort"]
flat["avg_off_investigation_effort"] = summary["avg_off_investigation_effort"]
flat["investigation_effort_delta"] = summary["investigation_effort_delta"]
flat["continuation_score"] = summary["continuation_score"]
flat["continuation_verdict"] = summary["continuation_verdict"]
flat["generator_manifest"] = report["generator_manifest"] ?? [:]
flat["run_manifest"] = report["run_manifest"] ?? [:]
flat["clusters"] = report["clusters"] ?? []

var createdAt = ""
if let g = runManifest["generated_at"] as? String, !g.isEmpty {
    createdAt = g
} else if let c = runManifest["created_at"] as? String, !c.isEmpty {
    createdAt = c
} else if let s = runManifest["started_at"] as? String, !s.isEmpty {
    createdAt = s
}
flat["created_at"] = createdAt

let url = URL(string: "http://localhost:58285/api/v1/benchmark/ingest")!
var req = URLRequest(url: url)
req.httpMethod = "POST"
req.setValue("application/json", forHTTPHeaderField: "Content-Type")
req.httpBody = try JSONSerialization.data(withJSONObject: flat, options: [])

let semaphore = DispatchSemaphore(value: 0)
let task = URLSession.shared.dataTask(with: req) { data, response, error in
    if let error = error {
        print("Error POSTing: \(error)")
    } else if let response = response as? HTTPURLResponse {
        print("Status Code: \(response.statusCode)")
        if let data = data, let body = String(data: data, encoding: .utf8) {
            print("Response body: \(body)")
        }
    }
    semaphore.signal()
}
task.resume()
semaphore.wait()
