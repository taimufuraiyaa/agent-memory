from __future__ import annotations

import hashlib
import json
import os
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path

ALLOWED_OUTPUTS = {"entities.jsonl", "relationships.jsonl", "communities.jsonl", "community_reports.jsonl"}
MAX_ARTIFACT_BYTES = 2 << 30


@dataclass(frozen=True)
class ArtifactContext:
    scope: dict[str, str]
    configuration_id: str
    job_id: str
    revision_id: str
    input_manifest_hash: str
    adapter_version: str
    graphrag_version: str
    python_version: str
    environment_fingerprint: str
    configuration_fingerprint: str
    prompt_fingerprint: str
    index_method: str
    mode: str
    models: tuple[str, ...]
    producer_identity: str
    build_digest: str
    signature: str
    requests: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    estimated_cost_usd: float = 0.0
    cache_hits: int = 0
    retries: int = 0
    duration_ms: int = 0


@dataclass(frozen=True)
class ArtifactFile:
    name: str
    kind: str
    required: bool
    bytes: int
    rows: int
    schema_fingerprint: str
    content_hash: str


@dataclass(frozen=True)
class ArtifactManifest:
    contract_version: str
    artifact_schema_version: str
    scope: dict[str, str]
    configuration_id: str
    job_id: str
    revision_id: str
    adapter_name: str
    adapter_version: str
    graphrag_version: str
    python_version: str
    environment_fingerprint: str
    input_manifest_hash: str
    configuration_fingerprint: str
    prompt_fingerprint: str
    index_method: str
    mode: str
    outputs: tuple[ArtifactFile, ...]
    models: tuple[str, ...]
    usage: dict[str, int]
    duration_millis: int
    status: str
    failure_code: str
    completed_at: str
    attestation: dict[str, str]

    def to_dict(self) -> dict[str, object]:
        result = asdict(self)
        if not self.failure_code:
            result.pop("failure_code")
        return result


def finalize_artifacts(root: Path, context: ArtifactContext, status: str, failure_code: str = "") -> ArtifactManifest:
    root = root.resolve(strict=True)
    if status not in {"completed", "failed", "cancelled"}:
        raise ValueError("artifact status is invalid")
    _validate_context(context)
    files: list[ArtifactFile] = []
    total = 0
    for entry in sorted(root.iterdir(), key=lambda item: item.name):
        if entry.name in {"FINALIZED", "artifact-manifest.json"}:
            continue
        if entry.name not in ALLOWED_OUTPUTS:
            raise ValueError("artifact output is not allowlisted")
        stat = entry.lstat()
        if entry.is_symlink() or not entry.is_file() or stat.st_size > MAX_ARTIFACT_BYTES:
            raise ValueError("artifact output must be a bounded regular file")
        contents = entry.read_bytes()
        if len(contents) != stat.st_size:
            raise ValueError("artifact changed while reading")
        total += len(contents)
        if total > MAX_ARTIFACT_BYTES:
            raise ValueError("artifact bundle exceeds size policy")
        digest = hashlib.sha256(contents).hexdigest()
        files.append(ArtifactFile(
            entry.name, entry.stem, entry.name in {"entities.jsonl", "relationships.jsonl"}, len(contents),
            contents.count(b"\n"), _schema_fingerprint(contents), "sha256:" + digest,
        ))
    if status == "completed" and not files:
        raise ValueError("completed artifact bundle is empty")
    if status != "completed" and not failure_code:
        failure_code = status
    manifest = ArtifactManifest(
        "graph-adapter/v1", "graph-artifact/v1", dict(context.scope), context.configuration_id,
        context.job_id, context.revision_id, "agent-memory-graphrag-adapter", context.adapter_version,
        context.graphrag_version, context.python_version, context.environment_fingerprint,
        context.input_manifest_hash, context.configuration_fingerprint, context.prompt_fingerprint,
        context.index_method, context.mode, tuple(files), tuple(context.models),
        {"requests": context.requests, "input_tokens": context.input_tokens, "output_tokens": context.output_tokens,
         "estimated_cost_micros": round(context.estimated_cost_usd * 1_000_000), "cache_hits": context.cache_hits, "retries": context.retries},
        context.duration_ms, status, failure_code, datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        {"producer_identity": context.producer_identity, "build_digest": context.build_digest, "signature": context.signature},
    )
    encoded = json.dumps(manifest.to_dict(), sort_keys=True, separators=(",", ":")).encode()
    with (root / "artifact-manifest.json").open("xb") as output:
        output.write(encoded + b"\n")
        output.flush()
        os.fsync(output.fileno())
    if status == "completed":
        with (root / "FINALIZED").open("x", encoding="utf-8") as output:
            output.write(hashlib.sha256(encoded).hexdigest() + "\n")
    return manifest


def verify_artifact_manifest(root: Path, manifest: ArtifactManifest) -> None:
    for file in manifest.outputs:
        path = root / file.name
        if path.is_symlink() or not path.is_file():
            raise ValueError("artifact file is missing or unsafe")
        if "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest() != file.content_hash:
            raise ValueError("artifact digest mismatch")


def _validate_context(context: ArtifactContext) -> None:
    required = [context.scope.get("workspace_id", ""), context.configuration_id, context.job_id, context.revision_id,
                context.input_manifest_hash, context.adapter_version, context.graphrag_version, context.python_version,
                context.environment_fingerprint, context.configuration_fingerprint, context.prompt_fingerprint,
                context.producer_identity, context.build_digest, context.signature]
    if not all(isinstance(value, str) and value.strip() for value in required):
        raise ValueError("artifact context identity is incomplete")
    if context.index_method not in {"standard", "fast"} or context.mode not in {"full", "incremental"} or not context.models:
        raise ValueError("artifact context mode is invalid")
    numeric = (context.requests, context.input_tokens, context.output_tokens, context.cache_hits, context.retries, context.duration_ms)
    if any(value < 0 for value in numeric) or not 0 <= context.estimated_cost_usd <= 1_000_000:
        raise ValueError("artifact context accounting is outside bounds")


def _schema_fingerprint(contents: bytes) -> str:
    first = contents.splitlines()[0] if contents else b"{}"
    try:
        keys = sorted(json.loads(first).keys())
    except (json.JSONDecodeError, AttributeError) as error:
        raise ValueError("normalized artifact is not JSONL") from error
    return "sha256:" + hashlib.sha256(json.dumps(keys, separators=(",", ":")).encode()).hexdigest()
