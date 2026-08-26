#!/usr/bin/env python3

from __future__ import annotations

import copy
import datetime as dt
import json
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
VALIDATOR = ROOT / "tools/graphrag-certification/production_gate.py"
COMMIT = "a" * 40
DIGEST = "sha256:" + "b" * 64


def stamp(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def valid_report() -> dict:
    now = dt.datetime.now(dt.timezone.utc)
    generated = now - dt.timedelta(hours=1)
    ended = generated - dt.timedelta(hours=1)
    started = ended - dt.timedelta(days=8)
    approval_time = ended + dt.timedelta(minutes=30)
    roles = ["graph-index-owner", "security", "privacy", "operations", "product"]
    return {
        "schema": "agent-memory-graphrag-production-approval/v1",
        "release_commit": COMMIT,
        "generated_at": stamp(generated),
        "expires_at": stamp(generated + dt.timedelta(days=7)),
        "adapter_image_digest": DIGEST,
        "graphrag_version": "3.1.2",
        "artifact_schema": "graph-artifact/v1",
        "certifications": {key: True for key in ["capacity", "chaos", "security", "privacy", "recovery", "accessibility"]},
        "matrices": {key: True for key in ["standalone", "self_managed_tenant_a", "self_managed_tenant_b", "hosted_tenant_a", "hosted_tenant_b"]},
        "observation_window": {
            "started_at": stamp(started), "ended_at": stamp(ended), "sample_count": 1000,
            "grounded_claim_ratio": 1, "relational_gain_pp": 10, "global_gain_pp": 15,
            "direct_precision_regression_pp": 1, "basic_p95_regression_pct": 1.99,
            "local_p95_ms": 75, "global_p95_ms": 250, "cost_within_budget": True,
            "approved_route": "explicit_local",
        },
        "approvals": [
            {"role": role, "subject": f"owner-{index}", "approved_at": stamp(approval_time), "evidence_digest": DIGEST, "approved": True}
            for index, role in enumerate(roles)
        ],
        "kill_switches": {key: True for key in ["process", "runtime", "workspace", "exercise_passed"]},
        "canonical_safety": {key: True for key in ["basic_available_during_failure", "disable_preserves_canonical", "removal_preserves_canonical", "canonical_only_rebuild_passed"]},
        "upgrade": {"report_digest": DIGEST, "signature_verified": True, "canary_passed": True, "rollback_passed": True},
        "approved": True,
    }


class ProductionGateTest(unittest.TestCase):
    def validate(self, report: dict) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            return subprocess.run(["python3", str(VALIDATOR), str(path), COMMIT], text=True, capture_output=True, check=False)

    def test_accepts_complete_current_evidence(self) -> None:
        self.assertEqual(self.validate(valid_report()).returncode, 0)

    def test_rejects_unknown_fields(self) -> None:
        report = valid_report()
        report["unreviewed_override"] = True
        self.assertNotEqual(self.validate(report).returncode, 0)

    def test_rejects_stale_approval(self) -> None:
        report = valid_report()
        report["generated_at"] = stamp(dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=31))
        self.assertNotEqual(self.validate(report).returncode, 0)

    def test_rejects_missed_quality_threshold(self) -> None:
        report = copy.deepcopy(valid_report())
        report["observation_window"]["grounded_claim_ratio"] = 0.999
        self.assertNotEqual(self.validate(report).returncode, 0)


if __name__ == "__main__":
    unittest.main()
