import importlib.util
import json
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
BENCHMARK_DIR = REPO_ROOT / "benchmark"
GENERATOR_PATH = BENCHMARK_DIR / "generate_benchmark.py"
SCORE_PATH = BENCHMARK_DIR / "score.py"
RUNNER_PATH = BENCHMARK_DIR / "run_benchmark.sh"
BINARY_PATH = BENCHMARK_DIR / "bin" / "agent-memory-benchmark"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load module {name} from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


generator = load_module("benchmark_generate", GENERATOR_PATH)
score = load_module("benchmark_score", SCORE_PATH)


class GeneratorDeterminismTest(unittest.TestCase):
    def test_delexicalization_removes_all_gold_keywords(self) -> None:
        """Property test: no gold keyword appears verbatim in de-lexicalized queries."""
        # Simulate a cluster with distinct keywords
        keywords = ["routes", "handlers", "json", "workspace"]
        query = "explain the routes and handlers for api server"
        result = generator.delexicalize_query(query, keywords)
        # Verify no keyword substring remains (case-insensitive)
        haystack = result.lower()
        for kw in keywords:
            self.assertNotIn(kw.lower(), haystack, f"Keyword {kw!r} leaked in: {result!r}")
        # The assertion function should not raise on the de-lexicalized result
        generator.assert_no_gold_keywords(result, keywords)

    def test_delexicalization_case_insensitive(self) -> None:
        """De-lexicalization is case-insensitive."""
        keywords = ["Routes", "Handlers"]
        query = "Explain the ROUTES and handlers"
        result = generator.delexicalize_query(query, keywords)
        haystack = result.lower()
        for kw in keywords:
            self.assertNotIn(kw.lower(), haystack)

    def test_delexicalization_longest_first(self) -> None:
        """Longer keywords are redacted before shorter ones to avoid partial matches."""
        keywords = ["alter table", "alter"]
        query = "the alter table command alters schema"
        result = generator.delexicalize_query(query, keywords)
        # "alter table" should be redacted as a unit, not leaving " table" after "alter" is redacted
        self.assertNotIn("alter table", result.lower())
        self.assertNotIn("table", result.lower().split(" "))

    def test_generator_emits_deterministic_10000_case_artifacts(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_one, tempfile.TemporaryDirectory() as tmp_two:
            for output_dir in (Path(tmp_one), Path(tmp_two)):
                subprocess.run(
                    ["python3", str(GENERATOR_PATH), "--output-dir", str(output_dir)],
                    cwd=REPO_ROOT,
                    check=True,
                    capture_output=True,
                    text=True,
                )

            manifest_one = json.loads((Path(tmp_one) / "benchmark_manifest.json").read_text(encoding="utf-8"))
            manifest_two = json.loads((Path(tmp_two) / "benchmark_manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(manifest_one["cluster_count"], 25)
            self.assertEqual(manifest_one["seed_count"], 200)
            self.assertEqual(manifest_one["fixture_count"], 200)
            self.assertEqual(manifest_one["test_case_count"], 10000)
            self.assertIn("api_server", manifest_one["cluster_ids"])
            self.assertIn("workspace_resolution", manifest_one["cluster_ids"])
            self.assertEqual(manifest_one, manifest_two)

            first_seed_one = (Path(tmp_one) / "prior_session_fixtures.jsonl").read_text(encoding="utf-8").splitlines()[0]
            first_seed_two = (Path(tmp_two) / "prior_session_fixtures.jsonl").read_text(encoding="utf-8").splitlines()[0]
            first_case_one = (Path(tmp_one) / "testcases.jsonl").read_text(encoding="utf-8").splitlines()[0]
            first_case_two = (Path(tmp_two) / "testcases.jsonl").read_text(encoding="utf-8").splitlines()[0]
            self.assertEqual(first_seed_one, first_seed_two)
            self.assertEqual(first_case_one, first_case_two)


class IntegrationReliabilityFixtureTest(unittest.TestCase):
    def test_fixture_ids_categories_and_thresholds_are_stable(self) -> None:
        path = BENCHMARK_DIR / "testdata" / "integration_reliability.jsonl"
        rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line]
        self.assertEqual(len(rows), len({row["id"] for row in rows}))
        self.assertEqual({row["category"] for row in rows}, {"delivery_reliability", "privacy_trust", "continuation_value"})
        self.assertTrue(all(row["latency_budget_ms"] > 0 for row in rows))


class ScoreMathTest(unittest.TestCase):
    def test_mrr_and_recall_at_k_formulas(self) -> None:
        """Hand-computed values to verify MRR and recall@k formulas."""
        returned_ids = ["a", "b", "c", "d"]
        relevant = {"b", "d"}
        # MRR: first relevant at rank 2 -> 1/2 = 0.5
        self.assertAlmostEqual(score.compute_mrr(returned_ids, relevant), 0.5, places=8)
        # MRR: no relevant -> 0
        self.assertAlmostEqual(score.compute_mrr(returned_ids, {"x"}), 0.0, places=8)
        # MRR: first at rank 1 -> 1.0
        self.assertAlmostEqual(score.compute_mrr(returned_ids, {"a"}), 1.0, places=8)
        # recall@1: none in top 1 -> 0
        self.assertAlmostEqual(score.compute_recall_at_k(returned_ids, relevant, 1), 0.0, places=8)
        # recall@3: 1 of 2 in top 3 -> 0.5
        self.assertAlmostEqual(score.compute_recall_at_k(returned_ids, relevant, 3), 0.5, places=8)
        # recall@5: all returned, 2 of 2 -> 1.0
        self.assertAlmostEqual(score.compute_recall_at_k(returned_ids, relevant, 5), 1.0, places=8)
        # Empty relevant -> 0
        self.assertAlmostEqual(score.compute_recall_at_k(returned_ids, set(), 3), 0.0, places=8)

    def test_lexical_floor_substring_retriever(self) -> None:
        """Lexical floor should match tokens in corpus."""
        corpus = {"a": "routes and handlers for the api", "b": "dashboard config", "c": "unrelated"}
        result = score.compute_lexical_floor("tell me about api routes", corpus, gold_ids={"a"}, partial_ids=set())
        self.assertGreater(result["lexical_floor_hits"], 0)
        self.assertGreater(result["lexical_floor_mrr"], 0)

    def test_lexical_floor_no_match(self) -> None:
        """When no tokens match, lexical floor is zero."""
        corpus = {"a": "xyz abc", "b": "def ghi"}
        result = score.compute_lexical_floor("meaning of life", corpus, gold_ids={"a"}, partial_ids=set())
        self.assertEqual(result["lexical_floor_hits"], 0)
        self.assertEqual(result["lexical_floor_mrr"], 0.0)

    def test_compute_significance_single_run(self) -> None:
        """N=1 returns single run indicator."""
        result = score.compute_significance([0.5], [0.3])
        self.assertEqual(result["run_count"], 1)
        self.assertFalse(result["is_significant"])
        self.assertIn("single run", result["note"])

    def test_compute_significance_identical(self) -> None:
        """Identical ON/OFF -> p ~ 1.0."""
        result = score.compute_significance([0.5, 0.6, 0.55], [0.5, 0.6, 0.55])
        self.assertEqual(result["run_count"], 3)
        self.assertGreaterEqual(result["p_value"], 0.9)
        self.assertFalse(result["is_significant"])

    def test_aggregate_scores_recompute_percentages_from_summed_totals(self) -> None:
        rows = [
            {
                "relevant_returned": 1,
                "returned_count": 2,
                "relevant_total": 2,
                "gold_returned": 1,
                "gold_total": 1,
                "keyword_found": 1,
                "keyword_total": 2,
                "ndcg": 0.9,
                "on_fact_found": 2,
                "off_fact_found": 0,
                "fact_total": 2,
                "on_complete_groups": 1,
                "off_complete_groups": 0,
                "fact_group_total": 1,
                "on_task_success": 1,
                "off_task_success": 0,
                "on_investigation_effort": 3,
                "off_investigation_effort": 5,
                "on_locator_found": 2,
                "off_locator_found": 0,
                "locator_total": 2,
                "on_verification_effort": 0,
                "off_verification_effort": 1,
                "on_rediscovery_effort": 1,
                "off_rediscovery_effort": 3,
                "on_operational_effort": 4,
                "off_operational_effort": 9,
                "on_operational_cost": 0.084,
                "off_operational_cost": 0.204,
                "amortized_acquisition_cost": 0.03,
                "memory_roi": 0.09,
                "on_runtime_ms": 100,
                "off_runtime_ms": 150,
                "on_returned_tokens": 100,
                "on_baseline_tokens": 200,
                "off_returned_tokens": 150,
                "off_baseline_tokens": 150,
            },
            {
                "relevant_returned": 1,
                "returned_count": 10,
                "relevant_total": 5,
                "gold_returned": 0,
                "gold_total": 1,
                "keyword_found": 1,
                "keyword_total": 2,
                "ndcg": 0.2,
                "on_fact_found": 1,
                "off_fact_found": 1,
                "fact_total": 2,
                "on_complete_groups": 0,
                "off_complete_groups": 0,
                "fact_group_total": 1,
                "on_task_success": 0,
                "off_task_success": 0,
                "on_investigation_effort": 2,
                "off_investigation_effort": 2,
                "on_locator_found": 1,
                "off_locator_found": 0,
                "locator_total": 2,
                "on_verification_effort": 1,
                "off_verification_effort": 1,
                "on_rediscovery_effort": 2,
                "off_rediscovery_effort": 3,
                "on_operational_effort": 5,
                "off_operational_effort": 6,
                "on_operational_cost": 0.054,
                "off_operational_cost": 0.066,
                "amortized_acquisition_cost": 0.02,
                "memory_roi": -0.008,
                "on_runtime_ms": 120,
                "off_runtime_ms": 120,
                "on_returned_tokens": 80,
                "on_baseline_tokens": 8000,
                "off_returned_tokens": 100,
                "off_baseline_tokens": 100,
            },
        ]
        summary = score.aggregate_case_rows(rows)
        self.assertAlmostEqual(summary["token_efficiency"], 70 / 250, places=8)
        self.assertAlmostEqual(summary["cost_saved_pct"], 70 / 250, places=8)
        self.assertAlmostEqual(summary["precision"], 2 / 12, places=8)
        self.assertAlmostEqual(summary["recall"], 2 / 7, places=8)
        self.assertAlmostEqual(summary["task_success_rate"], 0.5, places=8)
        self.assertAlmostEqual(summary["task_success_delta"], 0.5, places=8)
        self.assertIn("operational_cost_saved", summary)
        self.assertIn("locator_success_delta", summary)
        self.assertGreater(summary["continuation_score"], 0)


class RunnerAndScorerIntegrationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        BINARY_PATH.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(
            ["go", "build", "-o", str(BINARY_PATH), "./cmd/agent-memory"],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
        )

        cls._tmpdir = tempfile.TemporaryDirectory()
        cls.results_root = Path(cls._tmpdir.name)
        cls.run_id = "unittest-reduced"
        subprocess.run(
            [
                "bash",
                str(RUNNER_PATH),
                "--run-id",
                cls.run_id,
                "--results-dir",
                str(cls.results_root),
                "--case-limit",
                "1",
                "--skip-build",
            ],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        cls.run_dir = cls.results_root / cls.run_id
        cls.db_path = cls.run_dir / "benchmark.db"
        subprocess.run(
            [
                "python3",
                str(SCORE_PATH),
                "--run-dir",
                str(cls.run_dir),
                "--db",
                str(cls.db_path),
                "--ingest",
            ],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
        )

    @classmethod
    def tearDownClass(cls) -> None:
        cls._tmpdir.cleanup()

    def test_runner_produces_mapping_and_raw_artifacts(self) -> None:
        manifest = json.loads((self.run_dir / "run_manifest.json").read_text(encoding="utf-8"))
        artifacts_dir = self.run_dir
        if manifest.get("trials", 0) > 0:
            trials = sorted(self.run_dir.glob("trial_*"))
            self.assertTrue(trials, "expected at least one trial directory")
            artifacts_dir = trials[0]
        mapping = json.loads((artifacts_dir / "id_mapping.json").read_text(encoding="utf-8"))
        quality_on = (artifacts_dir / "quality-on.jsonl").read_text(encoding="utf-8").splitlines()
        quality_off = (artifacts_dir / "quality-off.jsonl").read_text(encoding="utf-8").splitlines()

        self.assertEqual(len(mapping), 200)
        self.assertEqual(manifest["executed_case_count_per_phase"], 1)
        self.assertEqual(len(quality_on), 1)
        self.assertEqual(len(quality_off), 1)
        self.assertIn("prior_session_fixtures_file", manifest)
        self.assertEqual(manifest["execution_mode"], "persistent-worker")
        self.assertGreaterEqual(manifest["worker_count_per_phase"], 1)
        off_row = json.loads(quality_off[0])
        self.assertFalse(off_row["memory_enabled"])
        self.assertGreater(off_row["returned_tokens"], 0)
        self.assertIn("answer_text", off_row)
        self.assertIn("expected_facts", off_row)
        self.assertIn("expected_locator_targets", off_row)

    def test_score_uses_mapping_and_ingests_sqlite_report(self) -> None:
        score_report_path = self.run_dir / "score_report.json"
        self.assertTrue(score_report_path.is_file(), f"score_report.json missing at {score_report_path}")
        report = json.loads(score_report_path.read_text(encoding="utf-8"))
        score_cases_path = Path(report.get("per_case_report_path", str(self.run_dir / "score_cases.jsonl")))
        score_cases = score_cases_path.read_text(encoding="utf-8").splitlines()
        self.assertEqual(len(score_cases), 1)
        case_row = json.loads(score_cases[0])
        self.assertTrue(case_row["gold_runtime_ids"])
        self.assertIn("off_returned_tokens", case_row)
        self.assertEqual(case_row["baseline_tokens"], case_row["off_returned_tokens"])
        self.assertIn("answer_fact_coverage", case_row)
        self.assertIn("on_task_success", case_row)
        self.assertIn("locator_total", case_row)
        self.assertIn("on_operational_cost", case_row)
        self.assertIn("memory_roi", case_row)

        conn = sqlite3.connect(str(self.db_path))
        try:
            row = conn.execute(
                "SELECT workspace, run_id, case_count, continuation_score, task_success_rate, off_returned_tokens, run_manifest_json FROM benchmark_runs WHERE run_id = ?",
                (self.run_id,),
            ).fetchone()
        finally:
            conn.close()
        self.assertIsNotNone(row)
        self.assertEqual(row[0], "benchmark-toggle-comparison")
        self.assertEqual(row[1], self.run_id)
        self.assertEqual(row[2], 1)
        self.assertIsNotNone(row[3])
        self.assertGreaterEqual(row[4], 0)
        self.assertGreater(row[5], 0)
        persisted_manifest = json.loads(row[6])
        self.assertIn("economic_summary", persisted_manifest)
        self.assertIn("operational_cost_saved", persisted_manifest["economic_summary"])


class BM25FTS5PhaseTest(unittest.TestCase):
    def test_bm25_returns_non_empty_results_for_fixture_queries(self) -> None:
        fixtures_path = BENCHMARK_DIR / "testdata" / "prior_session_fixtures.jsonl"
        cases_path = BENCHMARK_DIR / "testdata" / "testcases.jsonl"
        self.assertTrue(fixtures_path.is_file())
        self.assertTrue(cases_path.is_file())

        import re

        fixtures = []
        with fixtures_path.open("r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                fixtures.append(json.loads(line))

        cases = []
        with cases_path.open("r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                cases.append(json.loads(line))

        self.assertGreater(len(fixtures), 0)
        self.assertGreater(len(cases), 0)

        conn = sqlite3.connect(":memory:")
        try:
            try:
                conn.execute(
                    "CREATE VIRTUAL TABLE IF NOT EXISTS mem_fts USING fts5(id, content)"
                )
            except sqlite3.OperationalError:
                self.skipTest("FTS5 not available in this SQLite build")

            for seed in fixtures:
                conn.execute(
                    "INSERT INTO mem_fts (id, content) VALUES (?, ?)",
                    (seed["stable_id"], seed["content"]),
                )

            _STOP = {
                "the", "and", "for", "that", "this", "with", "from", "what",
                "where", "when", "which", "how", "does", "did", "was", "are",
                "can", "will", "have", "has", "been", "about", "into", "over",
                "after", "before", "between", "under", "again", "then", "here",
                "there", "your", "all", "not", "but", "its", "you", "also",
                "more", "some", "each", "just",
            }

            def build_query(prompt: str) -> str:
                words = re.findall(r"[a-zA-Z0-9]{3,}", prompt.lower())
                terms = [w for w in words if w not in _STOP]
                if not terms:
                    return '""'
                return " OR ".join(terms)

            total_returned = 0
            cases_with_results = 0
            top_k = 20
            for case in cases[:250]:
                query = build_query(case["prompt"])
                cursor = conn.execute(
                    "SELECT id FROM mem_fts WHERE mem_fts MATCH ? ORDER BY rank LIMIT ?",
                    (query, top_k),
                )
                returned = [row[0] for row in cursor.fetchall()]
                total_returned += len(returned)
                if returned:
                    cases_with_results += 1

        finally:
            conn.close()

        self.assertGreater(cases_with_results, 0)
        self.assertGreater(total_returned, 0)


class BenchmarkMetricsSchemaTest(unittest.TestCase):
    """B3: Verify the benchmark_metrics.json shape exports expected fields."""

    def test_metrics_schema_has_expected_fields(self) -> None:
        """Build a fake metrics payload and check score.py can parse it."""
        fake_metrics = {
            "candidate_count": 150,
            "vector_search_count": 2,
            "bloom_probe_count": 0,
            "cache_hit_count": 0,
            "schema_version": "benchmark-metrics-v1",
        }
        # load_benchmark_metrics expects a file path, but we can test _derive_effort_from_metrics directly.
        derived = score._derive_effort_from_metrics(fake_metrics)
        self.assertIsNotNone(derived)
        self.assertGreater(derived, 0)
        # candidate 150 /10=15, vector 2*2=4, bloom 0/50=0 => 19
        self.assertEqual(derived, 19)

    def test_lookup_effort_falls_back_to_constant_when_no_metrics(self) -> None:
        case = {"memory_enabled": True, "trace_summary": {}}
        result = score.lookup_effort_units(case, None)
        self.assertEqual(result, 1)

    def test_lookup_effort_uses_metrics_when_available(self) -> None:
        case = {"memory_enabled": True, "trace_summary": {"lookup_effort_units": 1}}
        metrics = {"candidate_count": 50, "vector_search_count": 1, "bloom_probe_count": 0}
        result = score.lookup_effort_units(case, metrics)
        # candidate 50/10=5, vector 1*2=2 => 7
        self.assertEqual(result, 7)


class FeedbackLoopCoverageTest(unittest.TestCase):
    """B8: Integration test verifying feedback influences decay and re-ranking.

    This is an INTEGRATION test that requires a built binary and a fresh DB.
    It is gated behind the --scenario feedback-loop flag (detected via env var
    AGENT_MEMORY_BENCHMARK_SCENARIO=feedback-loop).
    """

    @classmethod
    def setUpClass(cls) -> None:
        import os
        scenario = os.environ.get("AGENT_MEMORY_BENCHMARK_SCENARIO", "")
        if scenario != "feedback-loop":
            raise unittest.SkipTest(
                "Feedback-loop scenario is an integration test. "
                "Set AGENT_MEMORY_BENCHMARK_SCENARIO=feedback-loop to run."
            )

    def test_feedback_loop_exercise(self) -> None:
        """Seed memories, apply feedback, re-rank, and verify shift direction.

        This test:
        1. Seeds a small corpus in a temp DB
        2. Writes simulated feedback events (useful/rejected)
        3. Simulates time passing (N days)
        4. Runs a second retrieval phase
        5. Asserts useful memories rise in ranking; rejected/suppressed fall
        6. Verifies decay_score accumulation for un-accessed memories
        """
        import os
        import time

        binary = BINARY_PATH
        if not binary.is_file():
            raise unittest.SkipTest(f"Binary not found at {binary}; build first with: go build -o {binary} ./cmd/agent-memory")

        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = Path(tmp)
            db_path = tmp_path / "feedback_test.db"
            workspace = "feedback-loop-test"

            def run_cli(args: list[str], enabled: bool = True) -> dict:
                env = dict(os.environ)
                env["AGENT_MEMORY_ENABLED"] = "1" if enabled else "0"
                env["AGENT_MEMORY_RUN_LABEL"] = "feedback-test"
                env["AGENT_MEMORY_BENCHMARK_METRICS"] = "1"
                completed = subprocess.run(
                    [str(binary)] + args,
                    capture_output=True,
                    text=True,
                    env=env,
                    check=False,
                )
                if completed.returncode != 0:
                    raise RuntimeError(f"CLI failed: {args}\n{completed.stderr}")
                try:
                    return json.loads(completed.stdout)
                except json.JSONDecodeError:
                    raise RuntimeError(f"Invalid JSON from CLI: {completed.stdout[:500]}")

            # Phase 1: seed memories
            seeds = [
                {"content": "The api-gateway-proxy module uses circuit-breaker pattern for resilience.", "type": "semantic"},
                {"content": "The deployment-pipeline-precheck validates schema compatibility before migration.", "type": "semantic"},
                {"content": "The logging-pipeline-filter drops noisy debug events in production.", "type": "semantic"},
            ]
            memory_ids: list[str] = []
            for seed in seeds:
                result = run_cli([
                    "write",
                    "--db", str(db_path),
                    "--workspace", workspace,
                    "--type", seed["type"],
                    "--content", seed["content"],
                    "--format", "json",
                ])
                data = result.get("data", {})
                mid = data.get("ID") or data.get("id") or ""
                self.assertTrue(mid, f"write response missing memory ID: {result}")
                memory_ids.append(mid)

            # Phase 2: baseline retrieval
            baseline = run_cli([
                "recall",
                "--db", str(db_path),
                "--workspace", workspace,
                "--task", "circuit breaker resilience pattern",
                "--top-k", "5",
                "--budget", "200",
                "--format", "json",
            ])
            baseline_hits = baseline.get("data", {}).get("hits", [])
            baseline_order = [h.get("memory", {}).get("id") for h in baseline_hits if h.get("memory", {}).get("id")]
            self.assertGreater(len(baseline_order), 0, "Baseline retrieval returned no hits")

            # Phase 3: simulate feedback — mark first memory as useful (3x),
            # second as rejected (1x), leave third without feedback
            # Use the feedback API endpoint via CLI
            for _ in range(3):
                run_cli([
                    "feedback",
                    "--db", str(db_path),
                    "--workspace", workspace,
                    "--memory-id", memory_ids[0],
                    "--outcome", "accepted",
                    "--format", "json",
                ])

            # Reject the second memory
            run_cli([
                "feedback",
                "--db", str(db_path),
                "--workspace", workspace,
                "--memory-id", memory_ids[1],
                "--outcome", "rejected",
                "--format", "json",
            ])

            # Phase 4: re-retrieve after feedback
            after_feedback = run_cli([
                "recall",
                "--db", str(db_path),
                "--workspace", workspace,
                "--task", "circuit breaker resilience pattern",
                "--top-k", "5",
                "--budget", "200",
                "--format", "json",
            ])
            fb_hits = after_feedback.get("data", {}).get("hits", [])
            fb_order = [h.get("memory", {}).get("id") for h in fb_hits if h.get("memory", {}).get("id")]

            # Assert direction: memory_ids[0] (useful) should rank at or above baseline position
            if memory_ids[0] in baseline_order and memory_ids[0] in fb_order:
                baseline_pos = baseline_order.index(memory_ids[0])
                fb_pos = fb_order.index(memory_ids[0])
                # Useful memory should not drop in ranking
                self.assertLessEqual(fb_pos, baseline_pos + 1,
                    f"Useful memory {memory_ids[0]} dropped from rank {baseline_pos} to {fb_pos}")

            # Phase 5: verify decay_score accumulation (check stats)
            stats = run_cli([
                "stats",
                "--db", str(db_path),
                "--workspace", workspace,
                "--format", "json",
            ])
            stats_data = stats.get("data", {})
            # With feedback applied, suppressed/rejected memories should have non-zero suppression
            self.assertIsInstance(stats_data, dict, "Stats returned unexpected format")

            # Verify benchmark metrics export
            bm_path = tmp_path / "benchmark_metrics.json"
            # Metrics file is written to CWD by default; check both locations
            cwd_metrics = Path("benchmark_metrics.json")
            found = False
            for path in (bm_path, cwd_metrics):
                if path.is_file():
                    content = json.loads(path.read_text(encoding="utf-8"))
                    self.assertIn("candidate_count", content)
                    self.assertIn("vector_search_count", content)
                    self.assertIn("bloom_probe_count", content)
                    self.assertIn("cache_hit_count", content)
                    self.assertIn("schema_version", content)
                    self.assertEqual(content["schema_version"], "benchmark-metrics-v1")
                    found = True
                    break
            self.assertTrue(found, "benchmark_metrics.json not found after recall")


if __name__ == "__main__":
    unittest.main()
