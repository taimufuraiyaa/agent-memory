#!/usr/bin/env python3
"""Generate deterministic benchmark seed memories and labeled test cases."""

from __future__ import annotations

import argparse
import hashlib
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List


ROOT = Path(__file__).resolve().parent
DEFAULT_OUTPUT_DIR = ROOT / "testdata"
DEFAULT_CATALOG = DEFAULT_OUTPUT_DIR / "cluster_catalog.json"

SEED_TEMPLATES = [
    "{title} notes explain {kw0} for the {kw1} workflow.",
    "When {kw0} fails, inspect {kw2} before retrying the {kw1} path in {title}.",
    "{title} configuration stores {kw3} alongside {kw0} settings.",
    "The {title} rollout checklist validates {kw1} and {kw2} before release.",
    "{title} troubleshooting guidance links {kw0} with {kw3} regressions.",
    "{title} architecture keeps {kw2} close to {kw1} ownership boundaries.",
    "Use the {title} reports to review {kw0} trends and {kw3} anomalies.",
    "{title} testing guidance covers {kw1}, {kw2}, and {kw3} edge cases.",
]

QUESTION_BLUEPRINTS = [
    {"id": "q01", "body": "how {title_lower} handles {kw0} during the {kw1} workflow", "gold": [0], "keyword_indices": [0, 1]},
    {"id": "q02", "body": "what to check when {kw0} fails before the {kw1} path continues", "gold": [1], "keyword_indices": [0, 2]},
    {"id": "q03", "body": "where {title_lower} configuration stores {kw3} and related {kw0} settings", "gold": [2], "keyword_indices": [0, 3]},
    {"id": "q04", "body": "the rollout checklist for {kw1} and {kw2} in {title_lower}", "gold": [3], "keyword_indices": [1, 2]},
    {"id": "q05", "body": "how to debug {kw0} and {kw3} regressions in {title_lower}", "gold": [4], "keyword_indices": [0, 3]},
    {"id": "q06", "body": "the architecture link between {kw2} and {kw1} in {title_lower}", "gold": [5], "keyword_indices": [1, 2]},
    {"id": "q07", "body": "how to review {kw0} trends and {kw3} anomalies for {title_lower}", "gold": [6], "keyword_indices": [0, 3]},
    {"id": "q08", "body": "what testing guidance covers {kw1}, {kw2}, and {kw3} for {title_lower}", "gold": [7], "keyword_indices": [1, 2, 3]},
    {"id": "q09", "body": "the core summary of {title_lower} responsibilities and release checks", "gold": [0, 3], "keyword_indices": [0, 1, 2]},
    {"id": "q10", "body": "which notes connect {kw0} with adjacent {adj_kw} behavior in {title_lower}", "gold": [1, 4], "keyword_indices": [0, 3]},
]

VARIANT_PREFIXES = [
    "",
    "Quick check: ",
    "For agent-memory, ",
    "During debugging, ",
    "Please clarify ",
]

VARIANT_SUFFIXES = [
    "",
    " in this repo",
    " for the current workspace",
    " before release",
]

STYLE_WRAPPERS = [
    "{prefix}explain {body}{suffix}.",
    "{prefix}where can I find guidance on {body}{suffix}?",
]


@dataclass(frozen=True)
class Cluster:
    id: str
    title: str
    keywords: List[str]
    adjacent_clusters: List[str]


def stable_id(*parts: str) -> str:
    joined = ":".join(parts)
    return hashlib.sha256(joined.encode("utf-8")).hexdigest()[:16]


def load_clusters(path: Path) -> List[Cluster]:
    raw = json.loads(path.read_text())
    return [
        Cluster(
            id=item["id"],
            title=item["title"],
            keywords=item["keywords"],
            adjacent_clusters=item["adjacent_clusters"],
        )
        for item in raw
    ]


def render_seed_text(cluster: Cluster, index: int) -> str:
    kw0, kw1, kw2, kw3 = cluster.keywords
    return SEED_TEMPLATES[index].format(
        title=cluster.title,
        kw0=kw0,
        kw1=kw1,
        kw2=kw2,
        kw3=kw3,
    )


def build_seed_records(clusters: List[Cluster]) -> tuple[list[dict], dict[str, list[str]]]:
    records: list[dict] = []
    stable_ids_by_cluster: dict[str, list[str]] = {}
    for cluster in clusters:
        ids: list[str] = []
        for index in range(len(SEED_TEMPLATES)):
            sid = stable_id(cluster.id, "seed", str(index))
            ids.append(sid)
            records.append(
                {
                    "stable_id": sid,
                    "cluster_id": cluster.id,
                    "cluster_title": cluster.title,
                    "seed_index": index,
                    "content": render_seed_text(cluster, index),
                    "keywords": cluster.keywords,
                }
            )
        stable_ids_by_cluster[cluster.id] = ids
    return records, stable_ids_by_cluster


def required_keywords(cluster: Cluster, keyword_indices: List[int], adjacent_keyword: str) -> List[str]:
    values = [cluster.keywords[index] for index in keyword_indices]
    if adjacent_keyword:
        values.append(adjacent_keyword)
    deduped: list[str] = []
    for value in values:
        if value not in deduped:
            deduped.append(value)
    return deduped


def build_prompt(body: str, variant_index: int) -> str:
    prefix_count = len(VARIANT_PREFIXES)
    suffix_count = len(VARIANT_SUFFIXES)
    style_count = len(STYLE_WRAPPERS)

    prefix = VARIANT_PREFIXES[variant_index // (suffix_count * style_count)]
    suffix = VARIANT_SUFFIXES[(variant_index // style_count) % suffix_count]
    wrapper = STYLE_WRAPPERS[variant_index % style_count]
    return wrapper.format(prefix=prefix, body=body, suffix=suffix)


def build_test_cases(
    clusters: List[Cluster],
    seed_ids_by_cluster: Dict[str, List[str]],
    variants_per_question: int,
) -> list[dict]:
    clusters_by_id = {cluster.id: cluster for cluster in clusters}
    cases: list[dict] = []

    for cluster in clusters:
        adjacent = [clusters_by_id[cluster_id] for cluster_id in cluster.adjacent_clusters]
        adjacent_keyword = adjacent[0].keywords[0] if adjacent else ""
        for question_index, blueprint in enumerate(QUESTION_BLUEPRINTS):
            body = blueprint["body"].format(
                title_lower=cluster.title.lower(),
                kw0=cluster.keywords[0],
                kw1=cluster.keywords[1],
                kw2=cluster.keywords[2],
                kw3=cluster.keywords[3],
                adj_kw=adjacent_keyword,
            )
            gold_ids = [seed_ids_by_cluster[cluster.id][seed_index] for seed_index in blueprint["gold"]]
            partial_ids: list[str] = []
            relevance_grades = {gold_id: 3 for gold_id in gold_ids}
            for adjacent_index, adjacent_cluster in enumerate(adjacent):
                partial_seed_id = seed_ids_by_cluster[adjacent_cluster.id][adjacent_index]
                partial_ids.append(partial_seed_id)
                relevance_grades[partial_seed_id] = 1
            keywords = required_keywords(cluster, blueprint["keyword_indices"], adjacent_keyword if question_index == 9 else "")
            for variant_index in range(variants_per_question):
                case_id = stable_id(cluster.id, blueprint["id"], str(variant_index))
                cases.append(
                    {
                        "stable_case_id": case_id,
                        "cluster_id": cluster.id,
                        "cluster_title": cluster.title,
                        "question_id": blueprint["id"],
                        "question_index": question_index,
                        "variant_index": variant_index,
                        "prompt": build_prompt(body, variant_index),
                        "gold_ids": gold_ids,
                        "partial_ids": partial_ids,
                        "required_keywords": keywords,
                        "relevance_grades": relevance_grades,
                    }
                )
    return cases


def write_jsonl(path: Path, rows: List[dict]) -> None:
    with path.open("w", encoding="utf-8") as handle:
        for row in rows:
            handle.write(json.dumps(row, sort_keys=True))
            handle.write("\n")


def write_manifest(path: Path, clusters: List[Cluster], seeds: List[dict], cases: List[dict], variants_per_question: int) -> None:
    manifest = {
        "cluster_count": len(clusters),
        "seed_count": len(seeds),
        "seed_count_per_cluster": len(SEED_TEMPLATES),
        "question_count_per_cluster": len(QUESTION_BLUEPRINTS),
        "variants_per_question": variants_per_question,
        "test_case_count": len(cases),
        "scaled_target_formula": f"{len(clusters)} x {len(QUESTION_BLUEPRINTS)} x {variants_per_question}",
    }
    path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--catalog",
        type=Path,
        default=DEFAULT_CATALOG,
        help="Path to the cluster catalog JSON file.",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=DEFAULT_OUTPUT_DIR,
        help="Directory to receive generated benchmark artifacts.",
    )
    parser.add_argument(
        "--variants-per-question",
        type=int,
        default=40,
        help="Deterministic variant count per canonical question.",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    max_variants = len(VARIANT_PREFIXES) * len(VARIANT_SUFFIXES) * len(STYLE_WRAPPERS)
    if args.variants_per_question <= 0:
        raise SystemExit("--variants-per-question must be positive")
    if args.variants_per_question > max_variants:
        raise SystemExit(
            f"--variants-per-question exceeds deterministic variant capacity ({max_variants})"
        )

    output_dir = args.output_dir
    output_dir.mkdir(parents=True, exist_ok=True)

    clusters = load_clusters(args.catalog)
    seeds, seed_ids_by_cluster = build_seed_records(clusters)
    cases = build_test_cases(clusters, seed_ids_by_cluster, args.variants_per_question)

    write_jsonl(output_dir / "seed_memories.jsonl", seeds)
    write_jsonl(output_dir / "testcases.jsonl", cases)
    write_manifest(output_dir / "benchmark_manifest.json", clusters, seeds, cases, args.variants_per_question)

    print(
        json.dumps(
            {
                "cluster_count": len(clusters),
                "seed_count": len(seeds),
                "test_case_count": len(cases),
                "output_dir": str(output_dir),
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
