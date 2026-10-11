from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
from collections.abc import Sequence
from pathlib import Path

from .artifacts import ArtifactContext, finalize_artifacts
from .indexer import IndexRequest, Indexer
from .normalization import normalize_graphrag_artifacts
from .readiness import check_readiness, lock_fingerprint, ReadinessRequest
from .settings import SettingsRequest, generate_settings

CONTRACT_VERSION = "graph-adapter/v1"
COMMANDS = {
    "readiness",
    "full-index",
    "incremental-update",
    "cancel",
    "inspect-artifacts",
}


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="agent-memory-graphrag",
        description="Agent Memory's pinned GraphRAG indexing adapter",
    )
    parser.add_argument("command", choices=sorted(COMMANDS))
    parser.add_argument(
        "--request",
        help="Path to an Agent Memory-owned request manifest; stdin and arbitrary inline JSON are not accepted.",
    )
    return parser


def _readiness() -> tuple[int, dict[str, object]]:
    result = check_readiness(ReadinessRequest(enabled=True))
    response = {"contract_version": CONTRACT_VERSION, "adapter": "agent-memory-graphrag-adapter", **result.to_dict()}
    return (0 if result.state == "ready" else 2), response


def _load_request(path_value: str | None, command: str) -> tuple[Path, dict[str, object]]:
    if not path_value:
        raise ValueError("request_manifest_required")
    path = Path(path_value)
    cwd = Path.cwd().resolve(strict=True)
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 16 << 20:
        raise ValueError("unsafe_request_manifest")
    resolved = path.resolve(strict=True)
    if resolved.parent != cwd:
        raise ValueError("request_manifest_outside_job")
    envelope = json.loads(resolved.read_text(encoding="utf-8"))
    if envelope.get("contract_version") != CONTRACT_VERSION or envelope.get("command") != command:
        raise ValueError("request_contract_mismatch")
    if Path(str(envelope.get("job_root", ""))).resolve() != cwd:
        raise ValueError("job_root_mismatch")
    request = envelope.get("request")
    if not isinstance(request, dict):
        raise ValueError("request_payload_required")
    return cwd, request


def _index(command: str, path_value: str | None) -> tuple[int, dict[str, object]]:
    job_root, payload = _load_request(path_value, command)
    settings_request = SettingsRequest(
        completion_provider=str(payload["completion_provider"]),
        completion_model=str(payload["completion_model"]),
        embedding_provider=str(payload["embedding_provider"]),
        embedding_model=str(payload["embedding_model"]),
        job_root=str(job_root),
        chunk_size=int(payload.get("chunk_size", 1200)),
        chunk_overlap=int(payload.get("chunk_overlap", 100)),
        concurrent_requests=int(payload.get("concurrent_requests", 8)),
        max_gleanings=int(payload.get("max_gleanings", 1)),
        max_cluster_size=int(payload.get("max_cluster_size", 10)),
    )
    generated = generate_settings(settings_request)
    documents = payload.get("documents")
    if not isinstance(documents, list):
        raise ValueError("documents_required")
    request = IndexRequest(
        revision_id=str(payload["revision_id"]), method=str(payload.get("method", "standard")),
        settings=generated.settings, documents=documents,
        base_revision_id=str(payload.get("base_revision_id", "")),
        base_manifest_path=str(payload.get("base_manifest_path", "")),
        base_manifest_sha256=str(payload.get("base_manifest_sha256", "")),
        max_retries=int(payload.get("max_retries", 2)),
    )
    indexer = Indexer()
    result = asyncio.run(indexer.incremental_index(request) if command == "incremental-update" else indexer.full_index(request))
    response: dict[str, object] = {
        "contract_version": CONTRACT_VERSION, "state": result.status,
        "workflows_completed": result.workflows_completed, "retries": result.retries,
        "progress_events": result.progress_events, "usage": result.usage,
    }
    if result.failure_code:
        response["reason_code"] = result.failure_code
    # GraphRAG merges update workflows back into output_storage. Normalize the
    # merged table set so each activated revision is a complete snapshot.
    output_root = job_root / "output"
    if result.status == "completed" and output_root.is_dir():
        correlations = payload.get("correlations")
        if not isinstance(correlations, dict):
            raise ValueError("correlations_required")
        normalized_root = normalize_graphrag_artifacts(output_root, correlations)
        scope = payload.get("scope")
        if not isinstance(scope, dict):
            raise ValueError("scope_required")
        projection_hash = str(payload.get("input_manifest_hash") or payload["projection_sha256"])
        if not projection_hash.startswith("sha256:"):
            projection_hash = "sha256:" + projection_hash
        context = ArtifactContext(
            scope={"tenant_id": str(scope.get("tenant_id", "")), "workspace_id": str(scope.get("workspace_id", ""))},
            configuration_id=str(payload["configuration_id"]), job_id=str(payload["job_id"]), revision_id=request.revision_id,
            input_manifest_hash=projection_hash, adapter_version="0.1.0", graphrag_version="3.1.2",
            python_version=".".join(map(str, sys.version_info[:3])), environment_fingerprint=lock_fingerprint(),
            configuration_fingerprint=generated.settings_fingerprint, prompt_fingerprint=generated.prompt_fingerprint,
            index_method=request.method, mode="incremental" if command == "incremental-update" else "full",
            models=(settings_request.completion_model, settings_request.embedding_model),
            producer_identity=str(payload["producer_identity"]),
            build_digest=os.environ.get("AGENT_MEMORY_GRAPHRAG_BUILD_DIGEST", str(payload["build_digest"])),
            signature=str(payload["attestation_signature"]), requests=result.workflows_completed,
            input_tokens=int(result.usage.get("tokens", 0)), retries=result.retries,
            estimated_cost_usd=float(result.usage.get("estimated_cost_usd", 0.0)),
        )
        manifest = finalize_artifacts(normalized_root, context, result.status)
        response["artifact_manifest"] = str(normalized_root / "artifact-manifest.json")
        response["artifact_files"] = len(manifest.outputs)
    return (0 if result.status == "completed" else 2), response


def _inspect(path_value: str | None) -> tuple[int, dict[str, object]]:
    job_root, _ = _load_request(path_value, "inspect-artifacts")
    candidates = sorted(job_root.glob("*/normalized/artifact-manifest.json"))
    if len(candidates) != 1 or candidates[0].is_symlink():
        return 2, {"contract_version": CONTRACT_VERSION, "state": "unavailable", "reason_code": "artifact_manifest_not_found"}
    manifest = json.loads(candidates[0].read_text(encoding="utf-8"))
    return 0, {"contract_version": CONTRACT_VERSION, "state": "completed", "manifest": manifest}


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.command == "readiness":
            exit_code, response = _readiness()
        elif args.command in {"full-index", "incremental-update"}:
            exit_code, response = _index(args.command, args.request)
        elif args.command == "inspect-artifacts":
            exit_code, response = _inspect(args.request)
        else:
            exit_code, response = 0, {"contract_version": CONTRACT_VERSION, "state": "cancelled"}
    except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
        exit_code, response = 2, {"contract_version": CONTRACT_VERSION, "state": "failed", "reason_code": str(error)[:128] or "invalid_request"}

    json.dump(response, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
