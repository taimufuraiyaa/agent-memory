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
            self.assertEqual(manifest_one["test_case_count"], 10000)
            self.assertEqual(manifest_one, manifest_two)

            first_seed_one = (Path(tmp_one) / "seed_memories.jsonl").read_text(encoding="utf-8").splitlines()[0]
            first_seed_two = (Path(tmp_two) / "seed_memories.jsonl").read_text(encoding="utf-8").splitlines()[0]
            first_case_one = (Path(tmp_one) / "testcases.jsonl").read_text(encoding="utf-8").splitlines()[0]
            first_case_two = (Path(tmp_two) / "testcases.jsonl").read_text(encoding="utf-8").splitlines()[0]
            self.assertEqual(first_seed_one, first_seed_two)
            self.assertEqual(first_case_one, first_case_two)


class ScoreMathTest(unittest.TestCase):
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
                "returned_tokens": 100,
                "baseline_tokens": 200,
                "saved_tokens": 100,
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
                "returned_tokens": 7990,
                "baseline_tokens": 8000,
                "saved_tokens": 10,
            },
        ]
        summary = score.aggregate_case_rows(rows)
        self.assertAlmostEqual(summary["token_efficiency"], 110 / 8200, places=8)
        self.assertAlmostEqual(summary["cost_saved_pct"], 110 / 8200, places=8)
        self.assertAlmostEqual(summary["precision"], 2 / 12, places=8)
        self.assertAlmostEqual(summary["recall"], 2 / 7, places=8)


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
        mapping = json.loads((self.run_dir / "id_mapping.json").read_text(encoding="utf-8"))
        manifest = json.loads((self.run_dir / "run_manifest.json").read_text(encoding="utf-8"))
        quality_on = (self.run_dir / "quality-on.jsonl").read_text(encoding="utf-8").splitlines()
        quality_off = (self.run_dir / "quality-off.jsonl").read_text(encoding="utf-8").splitlines()

        self.assertEqual(len(mapping), 200)
        self.assertEqual(manifest["executed_case_count_per_phase"], 1)
        self.assertEqual(len(quality_on), 1)
        self.assertEqual(len(quality_off), 1)

    def test_score_uses_mapping_and_ingests_sqlite_report(self) -> None:
        score_cases = (self.run_dir / "score_cases.jsonl").read_text(encoding="utf-8").splitlines()
        self.assertEqual(len(score_cases), 1)
        case_row = json.loads(score_cases[0])
        self.assertTrue(case_row["gold_runtime_ids"])
        self.assertGreaterEqual(case_row["baseline_tokens"], case_row["returned_tokens"])

        conn = sqlite3.connect(str(self.db_path))
        try:
            row = conn.execute(
                "SELECT workspace, run_id, case_count, combined_score FROM benchmark_runs WHERE run_id = ?",
                (self.run_id,),
            ).fetchone()
        finally:
            conn.close()
        self.assertIsNotNone(row)
        self.assertEqual(row[0], "benchmark-toggle-comparison")
        self.assertEqual(row[1], self.run_id)
        self.assertEqual(row[2], 1)
        self.assertGreater(row[3], 0)


if __name__ == "__main__":
    unittest.main()
