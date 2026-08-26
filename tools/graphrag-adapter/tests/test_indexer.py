from __future__ import annotations

import asyncio
import json
from pathlib import Path
from types import SimpleNamespace

import pytest

from agent_memory_graphrag.indexer import IndexRequest, Indexer
from agent_memory_graphrag.progress import CancellationToken
from agent_memory_graphrag.settings import SettingsRequest, generate_settings


def _settings(root: Path):
    return generate_settings(SettingsRequest(completion_provider="openai", completion_model="c", embedding_provider="openai", embedding_model="e", job_root=str(root))).settings


def test_full_index_uses_public_api_and_reports_structured_progress(tmp_path: Path) -> None:
    calls = []

    async def fake_build_index(**kwargs):
        calls.append(kwargs)
        callback = kwargs["callbacks"][0]
        callback.pipeline_start(["extract_graph", "cluster_graph"])
        callback.workflow_start("extract_graph", object())
        callback.workflow_end("extract_graph", object())
        callback.pipeline_end([])
        return [SimpleNamespace(workflow="extract_graph", error=None, result={"rows": 2})]

    request = IndexRequest(revision_id="revision-1", method="standard", settings=_settings(tmp_path), documents=[{"id": "m1", "title": "Book A", "text": "Day one"}], max_retries=1)
    result = asyncio.run(Indexer(fake_build_index).full_index(request))

    assert result.status == "completed"
    assert result.workflows_completed == 1
    assert calls and calls[0]["is_update_run"] is False
    assert calls[0]["input_documents"].iloc[0]["id"] == "m1"
    assert result.progress_events


def test_incremental_index_requires_explicit_immutable_base(tmp_path: Path) -> None:
    base = tmp_path / "base-manifest.json"
    fixture = Path(__file__).parent / "fixtures" / "book_day10" / "manifest.json"
    base.write_bytes(fixture.read_bytes())

    async def fake_build_index(**kwargs):
        assert kwargs["is_update_run"] is True
        assert kwargs["additional_context"]["base_revision_id"] == "revision-day1"
        return [SimpleNamespace(workflow="update_entities_relationships", error=None, result={})]

    request = IndexRequest(revision_id="revision-day10", method="standard", settings=_settings(tmp_path / "job"), documents=[{"id": "m10", "title": "Book A", "text": "Day ten"}], base_revision_id="revision-day1", base_manifest_path=str(base), base_manifest_sha256=__import__("hashlib").sha256(base.read_bytes()).hexdigest())
    result = asyncio.run(Indexer(fake_build_index).incremental_index(request))
    assert result.status == "completed"

    base.write_text(json.dumps({"revision_id": "tampered"}), encoding="utf-8")
    with pytest.raises(ValueError, match="digest"):
        asyncio.run(Indexer(fake_build_index).incremental_index(request))


def test_cancellation_stops_at_callback_and_never_finalizes(tmp_path: Path) -> None:
    token = CancellationToken()

    async def fake_build_index(**kwargs):
        callback = kwargs["callbacks"][0]
        callback.pipeline_start(["extract_graph"])
        token.cancel()
        callback.workflow_start("extract_graph", object())
        return []

    request = IndexRequest(revision_id="revision-cancel", method="standard", settings=_settings(tmp_path), documents=[{"id": "m1", "title": "Book", "text": "text"}])
    result = asyncio.run(Indexer(fake_build_index).full_index(request, token))
    assert result.status == "cancelled"
    assert not (tmp_path / "FINALIZED").exists()


def test_finalized_revision_directory_is_never_mutated(tmp_path: Path) -> None:
    (tmp_path / "FINALIZED").write_text("done", encoding="utf-8")
    request = IndexRequest(revision_id="revision-final", method="standard", settings=_settings(tmp_path), documents=[{"id": "m1", "title": "Book", "text": "text"}])
    with pytest.raises(ValueError, match="finalized"):
        asyncio.run(Indexer(lambda **_: []).full_index(request))
