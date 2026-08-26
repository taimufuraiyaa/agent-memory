from __future__ import annotations

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from agent_memory_graphrag.attestation import attest_manifest, verify_attestation


def test_attestation_binds_manifest_and_workload_identity() -> None:
    private_key = Ed25519PrivateKey.generate()
    attestation = attest_manifest("a" * 64, "workload://adapter/revision-1", private_key)
    verify_attestation(attestation, private_key.public_key())
    tampered = attestation.__class__(**{**attestation.to_dict(), "manifest_sha256": "b" * 64})
    try:
        verify_attestation(tampered, private_key.public_key())
    except ValueError:
        pass
    else:
        raise AssertionError("tampered attestation accepted")


def test_attestation_redacts_error_text() -> None:
    from agent_memory_graphrag.attestation import safe_failure

    failure = safe_failure(ValueError("api_key=secret-value at /private/input.txt"))
    assert failure == {"failure_code": "ValueError", "message": "adapter operation failed"}
