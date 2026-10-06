from __future__ import annotations

import hashlib

from agent_memory_graphrag.readiness import ReadinessRequest, check_readiness


def test_readiness_distinguishes_disabled_unavailable_incompatible_and_ready() -> None:
    disabled = check_readiness(ReadinessRequest(enabled=False))
    assert disabled.state == "disabled"

    unavailable = check_readiness(ReadinessRequest(enabled=True, storage_ready=False))
    assert unavailable.state == "unavailable"
    assert unavailable.reason_code == "storage_unavailable"

    incompatible = check_readiness(
        ReadinessRequest(enabled=True, expected_lock_fingerprint="sha256:wrong")
    )
    assert incompatible.state == "incompatible"
    assert incompatible.reason_code == "lock_fingerprint_mismatch"

    ready = check_readiness(ReadinessRequest(enabled=True))
    assert ready.state == "ready"
    assert ready.graphrag_version == "3.1.2"
    assert ready.lock_fingerprint.startswith("sha256:")


def test_readiness_rejects_unsupported_python(monkeypatch) -> None:
    monkeypatch.setattr("agent_memory_graphrag.readiness.python_version", lambda: (3, 14, 0))
    result = check_readiness(ReadinessRequest(enabled=True))
    assert result.state == "incompatible"
    assert result.reason_code == "unsupported_python_version"


def test_readiness_uses_runtime_lock_file(monkeypatch, tmp_path) -> None:
    runtime_lock = tmp_path / "uv.lock"
    runtime_lock.write_bytes(b"immutable runtime lock\n")
    monkeypatch.setenv("AGENT_MEMORY_GRAPHRAG_LOCK_FILE", str(runtime_lock))

    result = check_readiness(ReadinessRequest(enabled=True))

    assert result.state == "ready"
    assert result.lock_fingerprint == "sha256:" + hashlib.sha256(runtime_lock.read_bytes()).hexdigest()
