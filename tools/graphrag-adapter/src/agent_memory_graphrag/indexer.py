from __future__ import annotations

import asyncio
import hashlib
import inspect
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Awaitable, Callable

import pandas as pd
from graphrag.api import build_index
from graphrag.config.models.graph_rag_config import GraphRagConfig

from .progress import CancellationToken, ProgressRecorder


@dataclass(frozen=True)
class IndexRequest:
    revision_id: str
    method: str
    settings: dict[str, object]
    documents: list[dict[str, str]]
    base_revision_id: str = ""
    base_manifest_path: str = ""
    base_manifest_sha256: str = ""
    max_retries: int = 2


@dataclass(frozen=True)
class IndexResult:
    status: str
    workflows_completed: int
    retries: int
    progress_events: list[dict[str, object]] = field(default_factory=list)
    usage: dict[str, int | float] = field(default_factory=dict)
    failure_code: str = ""


BuildIndex = Callable[..., Awaitable[list[Any]]]


class Indexer:
    def __init__(self, build_index_fn: BuildIndex = build_index) -> None:
        self._build_index = build_index_fn

    async def full_index(self, request: IndexRequest, cancellation: CancellationToken | None = None) -> IndexResult:
        return await self._run(request, False, cancellation or CancellationToken())

    async def incremental_index(self, request: IndexRequest, cancellation: CancellationToken | None = None) -> IndexResult:
        self._validate_base(request)
        return await self._run(request, True, cancellation or CancellationToken())

    async def _run(self, request: IndexRequest, update: bool, cancellation: CancellationToken) -> IndexResult:
        if not request.revision_id or request.method not in {"standard", "fast"} or not 0 <= request.max_retries <= 5:
            raise ValueError("index request is invalid")
        config = GraphRagConfig.model_validate(request.settings)
        job_root = Path(config.output_storage.base_dir).parent
        if (job_root / "FINALIZED").exists():
            raise ValueError("finalized revision directory cannot be mutated")
        if not request.documents or len(request.documents) > 100_000:
            raise ValueError("document count is outside policy")
        frame = pd.DataFrame(request.documents, columns=["id", "title", "text"])
        if frame[["id", "text"]].isnull().any().any():
            raise ValueError("documents are incomplete")
        progress = ProgressRecorder(cancellation)
        additional_context = {"revision_id": request.revision_id}
        if update:
            additional_context["base_revision_id"] = request.base_revision_id
            additional_context["base_manifest_sha256"] = request.base_manifest_sha256
        for attempt in range(request.max_retries + 1):
            try:
                cancellation.checkpoint()
                result = self._build_index(config=config, method=request.method, is_update_run=update, callbacks=[progress], additional_context=additional_context, verbose=False, input_documents=frame)
                outputs = await result if inspect.isawaitable(result) else result
                failures = [output for output in outputs if getattr(output, "error", None) is not None]
                if failures:
                    raise RuntimeError("workflow_failed")
                return IndexResult("completed", len(outputs), attempt, [event.to_dict() for event in progress.events], _usage(outputs))
            except asyncio.CancelledError:
                return IndexResult("cancelled", 0, attempt, [event.to_dict() for event in progress.events], failure_code="cancelled")
            except Exception as error:
                if attempt >= request.max_retries:
                    return IndexResult("failed", 0, attempt, [event.to_dict() for event in progress.events], failure_code=type(error).__name__)
        raise AssertionError("unreachable")

    @staticmethod
    def _validate_base(request: IndexRequest) -> None:
        if not request.base_revision_id or not request.base_manifest_path or len(request.base_manifest_sha256) != 64:
            raise ValueError("incremental index requires an explicit base revision")
        manifest_path = Path(request.base_manifest_path)
        if not manifest_path.is_file() or manifest_path.is_symlink():
            raise ValueError("base manifest must be a regular file")
        contents = manifest_path.read_bytes()
        if hashlib.sha256(contents).hexdigest() != request.base_manifest_sha256:
            raise ValueError("base manifest digest mismatch")
        manifest = json.loads(contents)
        if manifest.get("revision_id") != request.base_revision_id or manifest.get("status") != "completed":
            raise ValueError("base revision manifest is not finalized")


def _usage(outputs: list[Any]) -> dict[str, int | float]:
    usage: dict[str, int | float] = {"workflows": len(outputs), "tokens": 0, "estimated_cost_usd": 0.0}
    for output in outputs:
        result = getattr(output, "result", None)
        if isinstance(result, dict):
            usage["tokens"] += int(result.get("tokens", 0))
            usage["estimated_cost_usd"] += float(result.get("estimated_cost_usd", 0.0))
    return usage
