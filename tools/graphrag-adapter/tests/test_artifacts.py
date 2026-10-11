from __future__ import annotations

import json
from pathlib import Path
import sys

import pytest

from agent_memory_graphrag.artifacts import ArtifactContext, finalize_artifacts, verify_artifact_manifest


def _context() -> ArtifactContext:
    return ArtifactContext(
        scope={"tenant_id": "tenant-a", "workspace_id": "workspace-a"}, configuration_id="configuration-a",
        job_id="job-a", revision_id="revision-1", input_manifest_hash="sha256:" + "a" * 64,
        adapter_version="0.1.0", graphrag_version="3.1.2", python_version=".".join(map(str, sys.version_info[:3])),
        environment_fingerprint="sha256:environment", configuration_fingerprint="sha256:settings",
        prompt_fingerprint="sha256:prompts", index_method="standard", mode="full", models=("c", "e"),
        producer_identity="graph-worker", build_digest="sha256:" + "b" * 64, signature="workload-signature",
        input_tokens=123, estimated_cost_usd=0.42, duration_ms=1000, cache_hits=2,
    )


def test_artifact_manifest_detects_post_finalization_modification(tmp_path: Path) -> None:
    (tmp_path / "entities.jsonl").write_bytes(b'{"id":"e1"}\n')
    (tmp_path / "relationships.jsonl").write_bytes(b'{"id":"r1"}\n')
    manifest = finalize_artifacts(tmp_path, _context(), "completed")
    verify_artifact_manifest(tmp_path, manifest)
    (tmp_path / "entities.jsonl").write_bytes(b"tampered")
    with pytest.raises(ValueError, match="digest"):
        verify_artifact_manifest(tmp_path, manifest)


def test_artifact_manifest_rejects_unknown_symlink_and_noncompleted_status(tmp_path: Path) -> None:
    (tmp_path / "unknown.bin").write_bytes(b"x")
    with pytest.raises(ValueError, match="allowlisted"):
        finalize_artifacts(tmp_path, _context(), "completed")
    (tmp_path / "unknown.bin").unlink()
    (tmp_path / "entities.jsonl").write_bytes(b'{"id":"e1"}\n')
    partial = finalize_artifacts(tmp_path, _context(), "cancelled")
    assert partial.status == "cancelled"
    assert not (tmp_path / "FINALIZED").exists()


def test_artifact_manifest_contains_bounded_accounting_and_no_credentials(tmp_path: Path) -> None:
    (tmp_path / "entities.jsonl").write_bytes(b'{"id":"e1"}\n')
    manifest = finalize_artifacts(tmp_path, _context(), "completed")
    encoded = json.dumps(manifest.to_dict(), sort_keys=True)
    assert "api_key" not in encoded
    assert manifest.contract_version == "graph-adapter/v1"
    assert manifest.artifact_schema_version == "graph-artifact/v1"
    assert manifest.usage["input_tokens"] == 123
    assert manifest.usage["estimated_cost_micros"] == 420_000
    assert manifest.outputs[0].rows == 1
    assert manifest.attestation["producer_identity"] == "graph-worker"


def test_python_manifest_contract_matches_go_golden_field_set() -> None:
    golden_path = Path(__file__).parents[3] / "internal" / "contracts" / "testdata" / "graph_artifact_v1.json"
    golden = json.loads(golden_path.read_text(encoding="utf-8"))
    context = _context()
    expected = {
        "contract_version", "artifact_schema_version", "scope", "configuration_id", "job_id", "revision_id",
        "adapter_name", "adapter_version", "graphrag_version", "python_version", "environment_fingerprint",
        "input_manifest_hash", "configuration_fingerprint", "prompt_fingerprint", "index_method", "mode",
        "outputs", "models", "usage", "duration_millis", "status", "completed_at", "attestation",
    }
    assert set(golden) == expected
    assert set(context.scope) == set(golden["scope"])
