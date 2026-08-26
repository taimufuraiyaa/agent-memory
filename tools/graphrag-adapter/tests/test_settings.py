from __future__ import annotations

import json

import pytest

from agent_memory_graphrag.settings import SettingsRequest, generate_settings


def test_settings_are_bounded_schema_valid_and_contain_no_credentials() -> None:
    generated = generate_settings(
        SettingsRequest(
            completion_provider="openai",
            completion_model="index-completion-v1",
            embedding_provider="openai",
            embedding_model="index-embedding-v1",
            chunk_size=1000,
            chunk_overlap=100,
            concurrent_requests=8,
        )
    )
    encoded = json.dumps(generated.settings, sort_keys=True)
    assert "actual-secret" not in encoded
    assert generated.settings["completion_models"]["index_completion"]["api_key"] == "${INDEX_COMPLETION_API_KEY}"
    assert generated.settings["input_storage"]["base_dir"] == "/graph-job/input"
    assert generated.settings["output_storage"]["base_dir"] == "/graph-job/output"
    assert generated.prompt_fingerprint.startswith("sha256:")
    assert generated.settings_fingerprint.startswith("sha256:")


@pytest.mark.parametrize(
    ("field", "value"),
    [("chunk_size", 99), ("chunk_size", 5001), ("concurrent_requests", 0), ("concurrent_requests", 65)],
)
def test_settings_reject_out_of_policy_bounds(field: str, value: int) -> None:
    values = {"completion_provider": "openai", "completion_model": "c", "embedding_provider": "openai", "embedding_model": "e", field: value}
    with pytest.raises(ValueError):
        generate_settings(SettingsRequest(**values))


def test_reviewed_prompts_are_bound_into_settings() -> None:
    generated = generate_settings(SettingsRequest(completion_provider="openai", completion_model="c", embedding_provider="openai", embedding_model="e"))
    assert "evidence" in generated.settings["extract_graph"]["prompt"].lower()
    assert generated.settings["community_reports"]["graph_prompt"]
