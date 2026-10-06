from __future__ import annotations

import hashlib
import importlib
import importlib.metadata
import os
import sys
from dataclasses import asdict, dataclass
from pathlib import Path

SUPPORTED_GRAPHRAG_VERSION = "3.1.2"


@dataclass(frozen=True)
class ReadinessRequest:
    enabled: bool
    storage_ready: bool = True
    expected_lock_fingerprint: str = ""


@dataclass(frozen=True)
class ReadinessResult:
    state: str
    reason_code: str
    graphrag_version: str = ""
    adapter_version: str = ""
    lock_fingerprint: str = ""

    def to_dict(self) -> dict[str, object]:
        return asdict(self)


def python_version() -> tuple[int, int, int]:
    return sys.version_info[:3]


def lock_fingerprint() -> str:
    configured = os.environ.get("AGENT_MEMORY_GRAPHRAG_LOCK_FILE", "")
    lock = Path(configured) if configured else Path(__file__).parents[2] / "uv.lock"
    return "sha256:" + hashlib.sha256(lock.read_bytes()).hexdigest()


def check_readiness(request: ReadinessRequest) -> ReadinessResult:
    if not request.enabled:
        return ReadinessResult("disabled", "graph_index_disabled")
    if not request.storage_ready:
        return ReadinessResult("unavailable", "storage_unavailable")
    if not ((3, 11, 0) <= python_version() < (3, 14, 0)):
        return ReadinessResult("incompatible", "unsupported_python_version")
    try:
        importlib.import_module("graphrag.api")
        version = importlib.metadata.version("graphrag")
        adapter_version = importlib.metadata.version("agent-memory-graphrag-adapter")
    except (ImportError, importlib.metadata.PackageNotFoundError):
        return ReadinessResult("unavailable", "graphrag_not_installed")
    if version != SUPPORTED_GRAPHRAG_VERSION:
        return ReadinessResult("incompatible", "unsupported_graphrag_version", graphrag_version=version)
    fingerprint = lock_fingerprint()
    if request.expected_lock_fingerprint and request.expected_lock_fingerprint != fingerprint:
        return ReadinessResult("incompatible", "lock_fingerprint_mismatch", version, adapter_version, fingerprint)
    return ReadinessResult("ready", "ready", version, adapter_version, fingerprint)
