from __future__ import annotations

import json
from pathlib import Path

import pandas as pd

from agent_memory_graphrag.normalization import normalize_graphrag_artifacts


def test_normalization_maps_parquet_references_to_owned_evidence_jsonl(tmp_path: Path) -> None:
    raw = tmp_path / "raw"
    raw.mkdir()
    pd.DataFrame([{"id": "d1", "title": "Book A"}]).to_parquet(raw / "documents.parquet")
    pd.DataFrame([{"id": "tu1", "document_ids": ["token-a"]}]).to_parquet(raw / "text_units.parquet")
    pd.DataFrame([
        {"id": "e1", "title": "Retry Handler", "type": "service", "description": "Retries checkout", "text_unit_ids": ["tu1"]},
        {"id": "e2", "title": "Checkout", "type": "service", "description": "Checkout service", "text_unit_ids": ["tu1"]},
    ]).to_parquet(raw / "entities.parquet")
    pd.DataFrame([{"id": "r1", "source": "e1", "target": "e2", "description": "depends on", "text_unit_ids": ["tu1"]}]).to_parquet(raw / "relationships.parquet")
    pd.DataFrame([{"id": "c1", "parent": None, "entity_ids": ["e1", "e2"]}]).to_parquet(raw / "communities.parquet")
    pd.DataFrame([{"id": "cr1", "community": "c1", "title": "Payments", "summary": "Retry and checkout", "rank": 0.8}]).to_parquet(raw / "community_reports.parquet")

    output = normalize_graphrag_artifacts(raw, {"token-a": {"canonical_kind": "source_text", "canonical_id": "passage-a", "canonical_fingerprint": "sha256:passage-a"}})

    entity = json.loads((output / "entities.jsonl").read_text(encoding="utf-8").splitlines()[0])
    relationship = json.loads((output / "relationships.jsonl").read_text(encoding="utf-8").splitlines()[0])
    report = json.loads((output / "community_reports.jsonl").read_text(encoding="utf-8").splitlines()[0])
    assert entity["evidence"][0]["canonical_id"] == "passage-a"
    assert relationship["source_id"] == "e1" and relationship["target_id"] == "e2"
    assert report["community_id"] == "c1" and report["evidence"][0]["canonical_id"] == "passage-a"
    assert not list(output.glob("*.parquet"))


def test_normalization_fails_closed_when_entity_evidence_is_unresolved(tmp_path: Path) -> None:
    pd.DataFrame([{"id": "e1", "title": "Private", "type": "concept", "text_unit_ids": ["missing"]}]).to_parquet(tmp_path / "entities.parquet")
    pd.DataFrame([], columns=["id", "source", "target", "text_unit_ids"]).to_parquet(tmp_path / "relationships.parquet")
    try:
        normalize_graphrag_artifacts(tmp_path, {})
    except ValueError as error:
        assert "evidence" in str(error)
    else:
        raise AssertionError("evidence-free entity was normalized")
