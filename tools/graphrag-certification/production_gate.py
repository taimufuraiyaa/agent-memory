#!/usr/bin/env python3
"""Fail-closed validation for the signed GraphRAG production approval report."""

from __future__ import annotations

import datetime as dt
import json
import re
import sys
from pathlib import Path


DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")


def require_exact(value: dict, keys: set[str], label: str) -> None:
    if set(value) != keys:
        raise SystemExit(f"{label} fields are incomplete or contain unknown values")


def require_true_map(value: dict, keys: set[str], label: str) -> None:
    require_exact(value, keys, label)
    if any(value[key] is not True for key in keys):
        raise SystemExit(f"{label} contains a failed control")


def timestamp(value: str, label: str) -> dt.datetime:
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except (AttributeError, ValueError) as error:
        raise SystemExit(f"{label} is not an RFC3339 timestamp") from error
    if parsed.tzinfo is None:
        raise SystemExit(f"{label} must include a timezone")
    return parsed.astimezone(dt.timezone.utc)


def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit("usage: production_gate.py REPORT RELEASE_COMMIT")
    report = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
    release_commit = sys.argv[2]
    top = {
        "schema", "release_commit", "generated_at", "expires_at",
        "adapter_image_digest", "graphrag_version", "artifact_schema",
        "certifications", "matrices", "observation_window", "approvals",
        "kill_switches", "canonical_safety", "upgrade", "approved",
    }
    require_exact(report, top, "production report")
    if report["schema"] != "agent-memory-graphrag-production-approval/v1":
        raise SystemExit("production report schema is unsupported")
    if not COMMIT.fullmatch(release_commit) or report["release_commit"] != release_commit:
        raise SystemExit("production report is not bound to the checked-out release commit")
    if not DIGEST.fullmatch(report["adapter_image_digest"]):
        raise SystemExit("adapter image is not bound by sha256 digest")
    if report["graphrag_version"] != "3.1.2" or report["artifact_schema"] != "graph-artifact/v1":
        raise SystemExit("production report does not match the supported dependency contract")
    if report["approved"] is not True:
        raise SystemExit("production report is not approved")

    now = dt.datetime.now(dt.timezone.utc)
    generated, expires = timestamp(report["generated_at"], "generated_at"), timestamp(report["expires_at"], "expires_at")
    if generated > now + dt.timedelta(minutes=5) or generated < now - dt.timedelta(days=30) or expires <= now or expires > generated + dt.timedelta(days=30):
        raise SystemExit("production approval is stale, future-dated, expired, or overlong")

    require_true_map(report["certifications"], {"capacity", "chaos", "security", "privacy", "recovery", "accessibility"}, "certifications")
    require_true_map(report["matrices"], {"standalone", "self_managed_tenant_a", "self_managed_tenant_b", "hosted_tenant_a", "hosted_tenant_b"}, "topology matrices")
    require_true_map(report["kill_switches"], {"process", "runtime", "workspace", "exercise_passed"}, "kill switches")
    require_true_map(report["canonical_safety"], {"basic_available_during_failure", "disable_preserves_canonical", "removal_preserves_canonical", "canonical_only_rebuild_passed"}, "canonical safety")

    window = report["observation_window"]
    require_exact(window, {"started_at", "ended_at", "sample_count", "grounded_claim_ratio", "relational_gain_pp", "global_gain_pp", "direct_precision_regression_pp", "basic_p95_regression_pct", "local_p95_ms", "global_p95_ms", "cost_within_budget", "approved_route"}, "observation window")
    started, ended = timestamp(window["started_at"], "observation_window.started_at"), timestamp(window["ended_at"], "observation_window.ended_at")
    if ended <= started or ended - started < dt.timedelta(days=7) or ended > generated:
        raise SystemExit("observation window must be a completed, report-bound period of at least seven days")
    if not isinstance(window["sample_count"], int) or window["sample_count"] < 1000:
        raise SystemExit("observation window has insufficient samples")
    thresholds = (
        window["grounded_claim_ratio"] == 1,
        window["relational_gain_pp"] >= 10,
        window["global_gain_pp"] >= 15,
        window["direct_precision_regression_pp"] <= 1,
        window["basic_p95_regression_pct"] < 2,
        window["local_p95_ms"] <= 75,
        window["global_p95_ms"] <= 250,
        window["cost_within_budget"] is True,
    )
    if not all(thresholds) or window["approved_route"] not in {"basic", "explicit_local", "auto_local", "explicit_global", "auto_global"}:
        raise SystemExit("observation window does not meet the production thresholds")

    approvals = report["approvals"]
    roles = {"graph-index-owner", "security", "privacy", "operations", "product"}
    if not isinstance(approvals, list) or len(approvals) != len(roles):
        raise SystemExit("all five accountable approvals are required")
    seen_roles, seen_subjects = set(), set()
    for approval in approvals:
        require_exact(approval, {"role", "subject", "approved_at", "evidence_digest", "approved"}, "approval")
        if approval["role"] not in roles or approval["role"] in seen_roles or not str(approval["subject"]).strip() or approval["subject"] in seen_subjects:
            raise SystemExit("approval roles and accountable subjects must be unique")
        if approval["approved"] is not True or not DIGEST.fullmatch(approval["evidence_digest"]):
            raise SystemExit("approval is not affirmative or evidence-bound")
        approved_at = timestamp(approval["approved_at"], "approval.approved_at")
        if approved_at < ended or approved_at > generated:
            raise SystemExit("approval must follow the observation window and precede report generation")
        seen_roles.add(approval["role"])
        seen_subjects.add(approval["subject"])
    if seen_roles != roles:
        raise SystemExit("accountable approval set is incomplete")

    upgrade = report["upgrade"]
    require_exact(upgrade, {"report_digest", "signature_verified", "canary_passed", "rollback_passed"}, "upgrade")
    if not DIGEST.fullmatch(upgrade["report_digest"]) or any(upgrade[key] is not True for key in {"signature_verified", "canary_passed", "rollback_passed"}):
        raise SystemExit("upgrade evidence is incomplete")


if __name__ == "__main__":
    main()
