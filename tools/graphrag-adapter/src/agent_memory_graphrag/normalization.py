from __future__ import annotations

import json
import math
from pathlib import Path
from typing import Any

import pandas as pd


def normalize_graphrag_artifacts(root: Path, correlations: dict[str, dict[str, str]]) -> Path:
    """Convert pinned GraphRAG parquet output into Agent Memory-owned JSONL.

    The result contains revision-local candidates only. Stable reconciliation,
    trust, authorization, and activation remain Go-owned responsibilities.
    """
    root = root.resolve(strict=True)
    output = root / "normalized"
    output.mkdir(mode=0o700, exist_ok=False)

    text_units = _frame(root / "text_units.parquet", required=False)
    evidence_by_text_unit: dict[str, list[dict[str, str]]] = {}
    for row in _records(text_units):
        evidence_by_text_unit[str(row.get("id", ""))] = _evidence_for_tokens(
            _ids(row.get("document_ids")), correlations
        )

    entities_frame = _frame(root / "entities.parquet", required=True)
    entity_evidence: dict[str, list[dict[str, str]]] = {}
    entities: list[dict[str, Any]] = []
    for row in _records(entities_frame):
        external_id = _required(row, "id")
        evidence = _merge_evidence(
            *(evidence_by_text_unit.get(value, []) for value in _ids(row.get("text_unit_ids")))
        )
        if not evidence:
            raise ValueError("entity evidence is unresolved")
        entity_evidence[external_id] = evidence
        entities.append({
            "id": external_id,
            "name": _required_any(row, "title", "name"),
            "type": _required_any(row, "type", "entity_type"),
            "evidence": evidence,
        })
    _write_jsonl(output / "entities.jsonl", entities)

    relationships_frame = _frame(root / "relationships.parquet", required=True)
    relationships: list[dict[str, Any]] = []
    for row in _records(relationships_frame):
        source = _required_any(row, "source", "source_id")
        target = _required_any(row, "target", "target_id")
        evidence = _merge_evidence(
            *(evidence_by_text_unit.get(value, []) for value in _ids(row.get("text_unit_ids"))),
            entity_evidence.get(source, []), entity_evidence.get(target, []),
        )
        if not evidence:
            raise ValueError("relationship evidence is unresolved")
        relationships.append({
            "id": _required(row, "id"), "source_id": source, "target_id": target,
            "kind": _optional_any(row, "type", "kind", "description") or "related",
            "evidence": evidence,
        })
    _write_jsonl(output / "relationships.jsonl", relationships)

    communities_frame = _frame(root / "communities.parquet", required=False)
    community_entities: dict[str, list[str]] = {}
    communities: list[dict[str, Any]] = []
    for row in _records(communities_frame):
        community_id = _required_any(row, "id", "community")
        entity_ids = sorted(set(_ids(row.get("entity_ids"))))
        if not entity_ids or any(value not in entity_evidence for value in entity_ids):
            raise ValueError("community entity reference is unresolved")
        parent = _optional_any(row, "parent", "parent_id")
        community_entities[community_id] = entity_ids
        communities.append({"id": community_id, "parent_id": parent, "entity_ids": entity_ids})
    if communities:
        _write_jsonl(output / "communities.jsonl", communities)

    reports_frame = _frame(root / "community_reports.parquet", required=False)
    reports: list[dict[str, Any]] = []
    for row in _records(reports_frame):
        community_id = _required_any(row, "community", "community_id")
        evidence = _merge_evidence(*(entity_evidence.get(value, []) for value in community_entities.get(community_id, [])))
        if not evidence:
            raise ValueError("community report evidence is unresolved")
        reports.append({
            "id": _required(row, "id"), "community_id": community_id,
            "title": _required_any(row, "title"),
            "summary": _required_any(row, "summary", "full_content"),
            "evidence": evidence,
        })
    if reports:
        _write_jsonl(output / "community_reports.jsonl", reports)
    return output


def _frame(path: Path, required: bool) -> pd.DataFrame:
    if not path.exists():
        if required:
            raise ValueError("required GraphRAG artifact is absent")
        return pd.DataFrame()
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 2 << 30:
        raise ValueError("GraphRAG artifact is unsafe")
    return pd.read_parquet(path)


def _records(frame: pd.DataFrame) -> list[dict[str, Any]]:
    return frame.to_dict(orient="records") if not frame.empty else []


def _ids(value: Any) -> list[str]:
    if value is None or (isinstance(value, float) and math.isnan(value)):
        return []
    if isinstance(value, str):
        stripped = value.strip()
        if stripped.startswith("["):
            try:
                value = json.loads(stripped)
            except json.JSONDecodeError:
                return [stripped]
        else:
            return [stripped] if stripped else []
    if hasattr(value, "tolist"):
        value = value.tolist()
    if isinstance(value, (list, tuple, set)):
        return [str(item).strip() for item in value if str(item).strip()]
    return [str(value).strip()]


def _required(row: dict[str, Any], name: str) -> str:
    value = _optional(row.get(name))
    if not value:
        raise ValueError("GraphRAG artifact identity is missing")
    return value


def _required_any(row: dict[str, Any], *names: str) -> str:
    value = _optional_any(row, *names)
    if not value:
        raise ValueError("GraphRAG artifact required field is missing")
    return value


def _optional_any(row: dict[str, Any], *names: str) -> str:
    for name in names:
        if value := _optional(row.get(name)):
            return value
    return ""


def _optional(value: Any) -> str:
    if value is None or (isinstance(value, float) and math.isnan(value)):
        return ""
    return str(value).strip()


def _evidence_for_tokens(tokens: list[str], correlations: dict[str, dict[str, str]]) -> list[dict[str, str]]:
    evidence = []
    for token in tokens:
        item = correlations.get(token)
        if not isinstance(item, dict):
            continue
        normalized = {name: str(item.get(name, "")).strip() for name in ("canonical_kind", "canonical_id", "canonical_fingerprint")}
        if all(normalized.values()):
            evidence.append(normalized)
    return _merge_evidence(evidence)


def _merge_evidence(*groups: list[dict[str, str]]) -> list[dict[str, str]]:
    merged: dict[tuple[str, str, str], dict[str, str]] = {}
    for group in groups:
        for item in group:
            key = (item["canonical_kind"], item["canonical_id"], item["canonical_fingerprint"])
            merged[key] = dict(item)
    return [merged[key] for key in sorted(merged)]


def _write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    with path.open("x", encoding="utf-8") as output:
        for row in sorted(rows, key=lambda item: str(item["id"])):
            output.write(json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n")
