from __future__ import annotations

import base64
import json
import re
from dataclasses import asdict, dataclass

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey


@dataclass(frozen=True)
class AdapterAttestation:
    schema: str
    manifest_sha256: str
    workload_identity: str
    signature: str

    def to_dict(self) -> dict[str, str]:
        return asdict(self)


def _payload(manifest_sha256: str, workload_identity: str) -> bytes:
    return json.dumps({"schema": "agent-memory-graphrag-attestation/v1", "manifest_sha256": manifest_sha256, "workload_identity": workload_identity}, sort_keys=True, separators=(",", ":")).encode()


def attest_manifest(manifest_sha256: str, workload_identity: str, private_key: Ed25519PrivateKey) -> AdapterAttestation:
    if not re.fullmatch(r"[a-f0-9]{64}", manifest_sha256) or not 1 <= len(workload_identity) <= 256:
        raise ValueError("attestation identity is invalid")
    signature = private_key.sign(_payload(manifest_sha256, workload_identity))
    return AdapterAttestation("agent-memory-graphrag-attestation/v1", manifest_sha256, workload_identity, base64.b64encode(signature).decode("ascii"))


def verify_attestation(attestation: AdapterAttestation, public_key: Ed25519PublicKey) -> None:
    if attestation.schema != "agent-memory-graphrag-attestation/v1":
        raise ValueError("attestation schema is invalid")
    try:
        public_key.verify(base64.b64decode(attestation.signature, validate=True), _payload(attestation.manifest_sha256, attestation.workload_identity))
    except (InvalidSignature, ValueError) as error:
        raise ValueError("adapter attestation signature is invalid") from error


def safe_failure(error: BaseException) -> dict[str, str]:
    return {"failure_code": type(error).__name__[:64], "message": "adapter operation failed"}
