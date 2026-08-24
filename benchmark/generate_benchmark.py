#!/usr/bin/env python3
"""Generate deterministic continuation benchmark fixtures and labeled test cases."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, List


ROOT = Path(__file__).resolve().parent
DEFAULT_OUTPUT_DIR = ROOT / "testdata"
DEFAULT_CATALOG = DEFAULT_OUTPUT_DIR / "cluster_catalog.json"

FIXTURE_BLUEPRINTS = [
    {
        "id": "f01",
        "memory_type": "semantic",
        "template": "Earlier session notes for {title} say {kw0} should stay aligned with {kw1}.",
        "fact_keys": ["kw0", "kw1"],
    },
    {
        "id": "f02",
        "memory_type": "procedural",
        "template": "When continuing {title_lower}, inspect {kw2} before retrying the {kw1} path.",
        "fact_keys": ["kw2", "kw1"],
    },
    {
        "id": "f03",
        "memory_type": "semantic",
        "template": "{title} configuration keeps {kw3} beside the main {kw0} settings.",
        "fact_keys": ["kw3", "kw0"],
    },
    {
        "id": "f04",
        "memory_type": "procedural",
        "template": "The {title_lower} release checklist verifies {kw1} and {kw2} before merge.",
        "fact_keys": ["kw1", "kw2"],
    },
    {
        "id": "f05",
        "memory_type": "outcome",
        "template": "A prior {title_lower} incident showed {kw0} regressed when {kw3} checks were skipped near {adj_kw}.",
        "fact_keys": ["kw0", "kw3", "adj_kw"],
    },
    {
        "id": "f06",
        "memory_type": "semantic",
        "template": "{title} architecture keeps {kw2} close to the {kw1} ownership boundary.",
        "fact_keys": ["kw2", "kw1"],
    },
    {
        "id": "f07",
        "memory_type": "semantic",
        "template": "Earlier work reviewed {file0} to inspect {kw0} trends and {kw3} anomalies for {title_lower}.",
        "fact_keys": ["file0", "kw0", "kw3"],
    },
    {
        "id": "f08",
        "memory_type": "procedural",
        "template": "The previous workflow used `{command0}` while validating {kw1}, {kw2}, and {kw3} for {title_lower}.",
        "fact_keys": ["command0", "kw1", "kw2", "kw3"],
    },
]

QUESTION_BLUEPRINTS = [
    {
        "id": "q01",
        "body": "continue the earlier {title_lower} work and remind me what we already learned about {kw0} in the {kw1} workflow",
        "gold": [0],
        "keyword_indices": [0, 1],
        "fact_groups": [[{"fixture_index": 0, "fact_keys": ["kw0", "kw1"]}]],
    },
    {
        "id": "q02",
        "body": "what did the previous session say to inspect before the {kw1} path continues in {title_lower}",
        "gold": [1],
        "keyword_indices": [1, 2],
        "fact_groups": [[{"fixture_index": 1, "fact_keys": ["kw2", "kw1"]}]],
    },
    {
        "id": "q03",
        "body": "where did we record {kw3} together with {kw0} settings for {title_lower}",
        "gold": [2],
        "keyword_indices": [0, 3],
        "fact_groups": [[{"fixture_index": 2, "fact_keys": ["kw3", "kw0"]}]],
    },
    {
        "id": "q04",
        "body": "summarize the prior release-check guidance for {kw1} and {kw2} in {title_lower}",
        "gold": [3],
        "keyword_indices": [1, 2],
        "fact_groups": [[{"fixture_index": 3, "fact_keys": ["kw1", "kw2"]}]],
    },
    {
        "id": "q05",
        "body": "what root cause did the earlier {title_lower} incident reveal about {kw0}",
        "gold": [4],
        "keyword_indices": [0, 3],
        "fact_groups": [[{"fixture_index": 4, "fact_keys": ["kw0", "kw3"]}]],
    },
    {
        "id": "q06",
        "body": "what architecture note linked {kw2} with {kw1} ownership in {title_lower}",
        "gold": [5],
        "keyword_indices": [1, 2],
        "fact_groups": [[{"fixture_index": 5, "fact_keys": ["kw2", "kw1"]}]],
    },
    {
        "id": "q07",
        "body": "which file did the previous session review for {kw0} trends and {kw3} anomalies in {title_lower}",
        "gold": [6],
        "keyword_indices": [0, 3],
        "fact_groups": [[{"fixture_index": 6, "fact_keys": ["file0", "kw0", "kw3"]}]],
    },
    {
        "id": "q08",
        "body": "what command was used earlier while validating {kw1}, {kw2}, and {kw3} for {title_lower}",
        "gold": [7],
        "keyword_indices": [1, 2, 3],
        "fact_groups": [[{"fixture_index": 7, "fact_keys": ["command0", "kw1", "kw2"]}]],
    },
    {
        "id": "q09",
        "body": "give me the short continuation summary for {title_lower} responsibilities and release checks",
        "gold": [0, 3],
        "keyword_indices": [0, 1, 2],
        "fact_groups": [
            [{"fixture_index": 0, "fact_keys": ["kw0", "kw1"]}],
            [{"fixture_index": 3, "fact_keys": ["kw1", "kw2"]}],
        ],
    },
    {
        "id": "q10",
        "body": "which earlier notes connected {kw0} with adjacent {adj_kw} behavior in {title_lower}",
        "gold": [1, 4],
        "keyword_indices": [0, 3],
        "fact_groups": [
            [{"fixture_index": 1, "fact_keys": ["kw2", "kw1"]}],
            [{"fixture_index": 4, "fact_keys": ["kw0", "adj_kw"]}],
        ],
    },
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
    files: List[str]
    commands: List[str]


def word_count(text: str) -> int:
    return len([part for part in text.split() if part.strip()])


def unique_preserve_order(values: list[str]) -> list[str]:
    out: list[str] = []
    for value in values:
        if value and value not in out:
            out.append(value)
    return out


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
            files=item.get("files", []),
            commands=item.get("commands", []),
        )
        for item in raw
    ]


def cluster_values(cluster: Cluster, adjacent_keyword: str) -> dict[str, str]:
    kw0, kw1, kw2, kw3 = cluster.keywords
    file0 = cluster.files[0] if cluster.files else "unknown"
    file1 = cluster.files[1] if len(cluster.files) > 1 else file0
    command0 = cluster.commands[0] if cluster.commands else "rtk agent-memory recall"
    command1 = cluster.commands[1] if len(cluster.commands) > 1 else command0
    return {
        "title": cluster.title,
        "title_lower": cluster.title.lower(),
        "kw0": kw0,
        "kw1": kw1,
        "kw2": kw2,
        "kw3": kw3,
        "adj_kw": adjacent_keyword,
        "file0": file0,
        "file1": file1,
        "command0": command0,
        "command1": command1,
    }


def render_fixture_content(cluster: Cluster, adjacent_keyword: str, index: int) -> tuple[str, list[str], str]:
    blueprint = FIXTURE_BLUEPRINTS[index]
    values = cluster_values(cluster, adjacent_keyword)
    content = blueprint["template"].format(**values)
    facts = [values[key] for key in blueprint["fact_keys"]]
    return content, facts, blueprint["memory_type"]


def build_acquisition_profile(content: str, facts: list[str], files: list[str], commands: list[str]) -> dict:
    # Benchmark fixtures are generated, so acquisition metadata is an explicit estimate,
    # not a measured session trace. The raw components stay visible for auditability.
    evidence_items = len(unique_preserve_order(facts + files + commands))
    artifact_tokens = word_count(content)
    effort_units = max(1, evidence_items)
    return {
        "measured": False,
        "estimation_source": "generated-fixture-proxy",
        "artifact_tokens": artifact_tokens,
        "evidence_items": evidence_items,
        "file_count": len(files),
        "command_count": len(commands),
        "fact_count": len(facts),
        "effort_units": effort_units,
    }


def build_fixture_records(clusters: List[Cluster]) -> tuple[list[dict], dict[str, list[str]]]:
    records: list[dict] = []
    stable_ids_by_cluster: dict[str, list[str]] = {}
    clusters_by_id = {cluster.id: cluster for cluster in clusters}
    for cluster in clusters:
        adjacent = [clusters_by_id[cluster_id] for cluster_id in cluster.adjacent_clusters]
        adjacent_keyword = adjacent[0].keywords[0] if adjacent else cluster.keywords[0]
        ids: list[str] = []
        for index in range(len(FIXTURE_BLUEPRINTS)):
            sid = stable_id(cluster.id, "fixture", str(index))
            content, facts, memory_type = render_fixture_content(cluster, adjacent_keyword, index)
            ids.append(sid)
            records.append(
                {
                    "stable_id": sid,
                    "cluster_id": cluster.id,
                    "cluster_title": cluster.title,
                    "fixture_index": index,
                    "memory_type": memory_type,
                    "content": content,
                    "keywords": cluster.keywords,
                    "facts": facts,
                    "expected_files": cluster.files,
                    "expected_commands": cluster.commands,
                    "expected_locator_targets": unique_preserve_order(cluster.files + cluster.commands),
                    "acquisition_profile": build_acquisition_profile(content, facts, cluster.files, cluster.commands),
                }
            )
        stable_ids_by_cluster[cluster.id] = ids
    return records, stable_ids_by_cluster


def delexicalize_query(query: str, cluster_keywords: list[str]) -> str:
    """Replace any cluster keyword appearing as a case-insensitive substring in *query*
    with a redaction marker. Longest keywords are processed first to avoid partial
    replacements (e.g. 'alter table' before 'alter')."""
    result = query
    for kw in sorted(cluster_keywords, key=len, reverse=True):
        pattern = re.compile(re.escape(kw), re.IGNORECASE)
        result = pattern.sub("[redacted]", result)
    return result


def assert_no_gold_keywords(query: str, keywords: list[str]) -> None:
    """Property test: raise AssertionError if any keyword appears verbatim
    (case-insensitive substring) in the query."""
    haystack = query.lower()
    for kw in keywords:
        if kw.lower() in haystack:
            raise AssertionError(f"Gold keyword {kw!r} found in de-lexicalized query: {query!r}")


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
    suffix_count = len(VARIANT_SUFFIXES)
    style_count = len(STYLE_WRAPPERS)

    prefix = VARIANT_PREFIXES[variant_index // (suffix_count * style_count)]
    suffix = VARIANT_SUFFIXES[(variant_index // style_count) % suffix_count]
    wrapper = STYLE_WRAPPERS[variant_index % style_count]
    return wrapper.format(prefix=prefix, body=body, suffix=suffix)


def build_test_cases(
    clusters: List[Cluster],
    fixture_ids_by_cluster: Dict[str, List[str]],
    variants_per_question: int,
) -> list[dict]:
    clusters_by_id = {cluster.id: cluster for cluster in clusters}
    cases: list[dict] = []

    for cluster in clusters:
        adjacent = [clusters_by_id[cluster_id] for cluster_id in cluster.adjacent_clusters]
        adjacent_keyword = adjacent[0].keywords[0] if adjacent else ""
        values = cluster_values(cluster, adjacent_keyword)
        for question_index, blueprint in enumerate(QUESTION_BLUEPRINTS):
            body = blueprint["body"].format(**values)
            gold_ids = [fixture_ids_by_cluster[cluster.id][seed_index] for seed_index in blueprint["gold"]]
            partial_ids: list[str] = []
            # Binary relevance grades: 3 = gold (directly relevant), 1 = partial (adjacent-cluster).
            # Calibration note: this coarse {3,1,0} scale is intentional for deterministic fixture-based
            # benchmarking. A finer graded scale (e.g. 0-4) would require human judgment per query.
            relevance_grades = {gold_id: 3 for gold_id in gold_ids}
            for adjacent_index, adjacent_cluster in enumerate(adjacent):
                partial_seed_id = fixture_ids_by_cluster[adjacent_cluster.id][adjacent_index]
                partial_ids.append(partial_seed_id)
                relevance_grades[partial_seed_id] = 1
            keywords = required_keywords(cluster, blueprint["keyword_indices"], adjacent_keyword if question_index == 9 else "")
            fact_groups: list[list[str]] = []
            expected_facts: list[str] = []
            for group in blueprint["fact_groups"]:
                group_values: list[str] = []
                for part in group:
                    for key in part["fact_keys"]:
                        value = values[key]
                        if value not in group_values:
                            group_values.append(value)
                fact_groups.append(group_values)
                for value in group_values:
                    if value not in expected_facts:
                        expected_facts.append(value)
            for variant_index in range(variants_per_question):
                case_id = stable_id(cluster.id, blueprint["id"], str(variant_index))
                raw_prompt = build_prompt(body, variant_index)
                prompt = delexicalize_query(raw_prompt, cluster.keywords)
                # Property test: ensure no gold keyword leaked through
                assert_no_gold_keywords(prompt, cluster.keywords)
                cases.append(
                    {
                        "stable_case_id": case_id,
                        "cluster_id": cluster.id,
                        "cluster_title": cluster.title,
                        "question_id": blueprint["id"],
                        "question_index": question_index,
                        "variant_index": variant_index,
                        "prompt": prompt,
                        "prior_fixture_ids": gold_ids,
                        "expected_facts": expected_facts,
                        "expected_fact_groups": fact_groups,
                        "expected_files": cluster.files,
                        "expected_commands": cluster.commands,
                        "expected_locator_targets": unique_preserve_order(cluster.files + cluster.commands),
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


def write_manifest(path: Path, clusters: List[Cluster], fixtures: List[dict], cases: List[dict], variants_per_question: int) -> None:
    manifest = {
        "cluster_count": len(clusters),
        "seed_count": len(fixtures),
        "fixture_count": len(fixtures),
        "seed_count_per_cluster": len(FIXTURE_BLUEPRINTS),
        "fixture_count_per_cluster": len(FIXTURE_BLUEPRINTS),
        "question_count_per_cluster": len(QUESTION_BLUEPRINTS),
        "variants_per_question": variants_per_question,
        "test_case_count": len(cases),
        "scaled_target_formula": f"{len(clusters)} x {len(QUESTION_BLUEPRINTS)} x {variants_per_question}",
        "cluster_ids": [cluster.id for cluster in clusters],
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
    fixtures, fixture_ids_by_cluster = build_fixture_records(clusters)
    cases = build_test_cases(clusters, fixture_ids_by_cluster, args.variants_per_question)

    write_jsonl(output_dir / "prior_session_fixtures.jsonl", fixtures)
    write_jsonl(output_dir / "seed_memories.jsonl", fixtures)
    write_jsonl(output_dir / "testcases.jsonl", cases)
    write_manifest(output_dir / "benchmark_manifest.json", clusters, fixtures, cases, args.variants_per_question)

    print(
        json.dumps(
            {
                "cluster_count": len(clusters),
                "seed_count": len(fixtures),
                "fixture_count": len(fixtures),
                "test_case_count": len(cases),
                "output_dir": str(output_dir),
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
