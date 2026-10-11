from __future__ import annotations

import asyncio
import hashlib
import importlib
import json
import shutil
from pathlib import Path

import pandas as pd
import pytest
from graphrag.index.typing.workflow import WorkflowFunctionOutput
from graphrag.index.workflows.factory import PipelineFactory

from agent_memory_graphrag.indexer import IndexRequest, Indexer
from agent_memory_graphrag.normalization import normalize_graphrag_artifacts
from agent_memory_graphrag.settings import SettingsRequest, generate_settings


def _settings(root: Path, workflows: list[str]) -> dict[str, object]:
    root.mkdir(mode=0o700)
    settings = generate_settings(SettingsRequest(
        completion_provider="ollama_chat", completion_model="offline-completion",
        embedding_provider="ollama", embedding_model="offline-embedding", job_root=str(root),
    )).settings
    settings["workflows"] = workflows
    return settings


def test_full_then_incremental_uses_real_document_merge_without_model_calls(tmp_path: Path) -> None:
    # These are the installed public API's real file/table/chunking/update
    # workflows. None invokes a completion or embedding provider.
    base = tmp_path / "base"
    first = IndexRequest(
        revision_id="base", method="standard",
        settings=_settings(base, ["create_base_text_units", "create_final_documents"]),
        documents=[{"id": "memory-token-1", "title": "memory", "text": "Billing uses pricing."}],
        max_retries=0,
    )
    initial = asyncio.run(Indexer().full_index(first))
    assert initial.status == "completed"

    update = tmp_path / "update"
    settings = _settings(update, ["create_base_text_units", "create_final_documents", "update_final_documents", "update_clean_state"])
    shutil.copytree(base / "output", update / "output")
    manifest = update / "base-manifest.json"
    manifest.write_text(json.dumps({"revision_id": "base", "status": "completed"}), encoding="utf-8")
    request = IndexRequest(
        revision_id="next", method="standard", settings=settings,
        documents=[{"id": "memory-token-2", "title": "memory", "text": "Throughput affects capacity pricing."}],
        base_revision_id="base", base_manifest_path=str(manifest),
        base_manifest_sha256=hashlib.sha256(manifest.read_bytes()).hexdigest(), max_retries=0,
    )
    result = asyncio.run(Indexer().incremental_index(request))
    assert result.status == "completed", result.failure_code
    documents = pd.read_parquet(update / "output" / "documents.parquet")
    assert set(documents["id"]) == {"memory-token-1", "memory-token-2"}
    assert documents["human_readable_id"].tolist() == [0, 1]
    assert documents["text_unit_ids"].map(len).tolist() == [1, 1]
    assert not documents["human_readable_id"].isnull().any()


def _graph_tables(prefix: str, token: str) -> dict[str, pd.DataFrame]:
    entity_ids = [f"{prefix}-billing", f"{prefix}-runtime"]
    text_unit = f"{prefix}-text"
    return {
        "documents": pd.DataFrame([{"id": token, "human_readable_id": 0, "title": "memory", "text": f"{prefix} billing uses runtime", "text_unit_ids": [text_unit], "creation_date": "", "raw_data": None}]),
        "entities": pd.DataFrame([
            {"id": entity_id, "human_readable_id": index, "title": title, "type": "service", "description": "Synthetic service", "text_unit_ids": [text_unit], "frequency": 1, "degree": 1}
            for index, (entity_id, title) in enumerate(zip(entity_ids, ["BILLING", "RUNTIME"], strict=True))
        ]),
        "relationships": pd.DataFrame([{"id": f"{prefix}-edge", "human_readable_id": 0, "source": "BILLING", "target": "RUNTIME", "description": "Synthetic relation", "weight": 1.0, "combined_degree": 2, "text_unit_ids": [text_unit]}]),
        "text_units": pd.DataFrame([{"id": text_unit, "human_readable_id": 0, "text": f"{prefix} billing uses runtime", "n_tokens": 4, "document_id": token, "entity_ids": entity_ids, "relationship_ids": [f"{prefix}-edge"], "covariate_ids": []}]),
        "communities": pd.DataFrame([{"id": f"{prefix}-community", "human_readable_id": 0, "community": 0, "level": 0, "parent": -1, "children": [], "title": "Community 0", "entity_ids": entity_ids, "relationship_ids": [f"{prefix}-edge"], "text_unit_ids": [text_unit], "period": "", "size": 2}]),
        "community_reports": pd.DataFrame([{"id": f"{prefix}-report", "human_readable_id": 0, "community": 0, "level": 0, "parent": -1, "children": [], "title": "Billing context", "summary": "Billing uses runtime", "full_content": "Billing uses runtime", "rank": 1.0, "rating_explanation": "Synthetic fixture", "findings": [], "full_content_json": "{}", "period": "", "size": 2}]),
    }


@pytest.mark.parametrize("case", ["reused", "new_and_duplicate", "unknown_member", "missing_mapping", "invalid_mapping"])
def test_real_incremental_merge_reconciles_reused_entity_ids_in_communities(tmp_path: Path, monkeypatch: pytest.MonkeyPatch, case: str) -> None:
    update = tmp_path / "update"
    settings = _settings(update, ["offline_seed_delta", "update_final_documents", "update_entities_relationships", "update_text_units", "update_covariates", "offline_alter_state", "update_communities", "update_community_reports", "update_clean_state"])
    output = update / "output"
    output.mkdir()
    for table, frame in _graph_tables("old", "token-old").items():
        frame.to_parquet(output / f"{table}.parquet")

    async def seed_delta(_config, context):
        tables = _graph_tables("delta", "token-new")
        if case == "new_and_duplicate":
            extra = tables["entities"].iloc[0].to_dict()
            extra.update(id="delta-monitor", title="MONITOR", human_readable_id=2)
            tables["entities"] = pd.concat([tables["entities"], pd.DataFrame([extra])], ignore_index=True)
            tables["communities"].at[0, "entity_ids"] = ["delta-billing", "delta-billing", "delta-runtime", "delta-monitor"]
        elif case == "unknown_member":
            tables["communities"].at[0, "entity_ids"] = ["delta-billing", "fabricated-member"]
        for table, frame in tables.items():
            await context.output_table_provider.write_dataframe(table, frame)
        return WorkflowFunctionOutput(result=None)

    async def alter_state(_config, context):
        if case == "missing_mapping":
            del context.state["incremental_update_entity_id_mapping"]
        elif case == "invalid_mapping":
            context.state["incremental_update_entity_id_mapping"]["delta-billing"] = "fabricated-target"
        return WorkflowFunctionOutput(result=None)

    async def summarize_without_model(**kwargs):
        entities = kwargs["extracted_entities"].copy()
        relationships = kwargs["extracted_relationships"].copy()
        for frame in (entities, relationships):
            frame["description"] = frame["description"].map(lambda values: " ".join(values))
        return entities, relationships

    monkeypatch.setitem(PipelineFactory.workflows, "offline_seed_delta", seed_delta)
    monkeypatch.setitem(PipelineFactory.workflows, "offline_alter_state", alter_state)
    merge_module = importlib.import_module("graphrag.index.workflows.update_entities_relationships")
    monkeypatch.setattr(merge_module, "create_completion", lambda *_args, **_kwargs: object())
    monkeypatch.setattr(merge_module, "get_summarized_entities_relationships", summarize_without_model)
    manifest = update / "base-manifest.json"
    manifest.write_text(json.dumps({"revision_id": "base", "status": "completed"}), encoding="utf-8")
    request = IndexRequest(revision_id="next", method="standard", settings=settings,
                           documents=[{"id": "token-new", "title": "memory", "text": "delta billing uses runtime"}],
                           base_revision_id="base", base_manifest_path=str(manifest),
                           base_manifest_sha256=hashlib.sha256(manifest.read_bytes()).hexdigest(), max_retries=0)
    result = asyncio.run(Indexer().incremental_index(request))
    if case in {"unknown_member", "missing_mapping", "invalid_mapping"}:
        assert result.status == "failed"
        assert result.failure_code == "workflow_agent_memory_update_communities_ValueError"
        assert not (output / "normalized").exists()
        return
    assert result.status == "completed", result.failure_code
    raw_communities = pd.read_parquet(output / "communities.parquet")
    assert all(len(members) == len(set(members)) for members in raw_communities["entity_ids"])
    correlations = {
        token: {"canonical_kind": "memory", "canonical_id": memory_id, "canonical_fingerprint": f"sha256:{memory_id}"}
        for token, memory_id in [("token-old", "memory-old"), ("token-new", "memory-new")]
    }
    normalized = normalize_graphrag_artifacts(output, correlations)
    communities = [json.loads(row) for row in (normalized / "communities.jsonl").read_text().splitlines()]
    assert len(communities) == 2
    expected_members = {"old-billing", "old-runtime"}
    assert set(next(row for row in communities if row["id"] == "old-community")["entity_ids"]) == expected_members
    if case == "new_and_duplicate":
        expected_members.add("delta-monitor")
    assert set(next(row for row in communities if row["id"] == "delta-community")["entity_ids"]) == expected_members
    reports = [json.loads(row) for row in (normalized / "community_reports.jsonl").read_text().splitlines()]
    assert len(reports) == 2
    assert all({item["canonical_id"] for item in row["evidence"]} == {"memory-old", "memory-new"} for row in reports)
