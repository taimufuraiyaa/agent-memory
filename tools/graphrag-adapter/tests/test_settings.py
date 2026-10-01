from __future__ import annotations

import json
import stat
from pathlib import Path

import pytest
from graphrag.config.models.graph_rag_config import GraphRagConfig

from agent_memory_graphrag.settings import GeneratedSettings, SettingsRequest, generate_settings


def _generate_settings(tmp_path: Path, **values: object) -> GeneratedSettings:
    return generate_settings(SettingsRequest(job_root=str(tmp_path), **values))


def test_settings_are_bounded_schema_valid_and_contain_no_credentials(tmp_path: Path) -> None:
    generated = _generate_settings(
        tmp_path,
        completion_provider="openai",
        completion_model="index-completion-v1",
        embedding_provider="openai",
        embedding_model="index-embedding-v1",
        chunk_size=1000,
        chunk_overlap=100,
        concurrent_requests=8,
    )
    encoded = json.dumps(generated.settings, sort_keys=True)
    assert "actual-secret" not in encoded
    assert generated.settings["completion_models"]["index_completion"]["api_key"] == "${INDEX_COMPLETION_API_KEY}"
    assert generated.settings["input_storage"]["base_dir"] == str(tmp_path / "input")
    assert generated.settings["output_storage"]["base_dir"] == str(tmp_path / "output")
    assert generated.prompt_fingerprint.startswith("sha256:")
    assert generated.settings_fingerprint.startswith("sha256:")


@pytest.mark.parametrize(
    ("field", "value"),
    [("chunk_size", 99), ("chunk_size", 5001), ("concurrent_requests", 0), ("concurrent_requests", 65)],
)
def test_settings_reject_out_of_policy_bounds(tmp_path: Path, field: str, value: int) -> None:
    values = {"completion_provider": "openai", "completion_model": "c", "embedding_provider": "openai", "embedding_model": "e", field: value}
    with pytest.raises(ValueError):
        _generate_settings(tmp_path, **values)


def test_reviewed_prompts_are_private_job_files_resolved_by_pinned_graphrag(tmp_path: Path) -> None:
    generated = _generate_settings(
        tmp_path,
        completion_provider="openai",
        completion_model="c",
        embedding_provider="openai",
        embedding_model="e",
    )
    config = GraphRagConfig.model_validate(generated.settings)
    prompts = {
        "extract_graph": (
            generated.settings["extract_graph"]["prompt"],
            config.extract_graph.resolved_prompts().extraction_prompt,
        ),
        "summarize_descriptions": (
            generated.settings["summarize_descriptions"]["prompt"],
            config.summarize_descriptions.resolved_prompts().summarize_prompt,
        ),
        "community_graph": (
            generated.settings["community_reports"]["graph_prompt"],
            config.community_reports.resolved_prompts().graph_prompt,
        ),
        "community_text": (
            generated.settings["community_reports"]["text_prompt"],
            config.community_reports.resolved_prompts().text_prompt,
        ),
    }
    for configured_path, resolved_prompt in prompts.values():
        path = Path(configured_path)
        assert path.parent == tmp_path
        assert path.is_file()
        assert stat.S_IMODE(path.stat().st_mode) == 0o600
        assert path.read_text(encoding="utf-8") == resolved_prompt
    extraction_prompt = prompts["extract_graph"][1]
    assert "evidence" in extraction_prompt.lower()
    assert "-Goal-" in extraction_prompt
    assert '("entity"<|>' in extraction_prompt
    assert "untrusted evidence" in extraction_prompt.lower()
    assert '"date_range"' in prompts["community_text"][1]


def test_settings_refuse_to_write_prompts_into_a_shared_job_root(tmp_path: Path) -> None:
    tmp_path.chmod(0o755)
    with pytest.raises(ValueError, match="private directory"):
        generate_settings(SettingsRequest(
            completion_provider="openai", completion_model="c", embedding_provider="openai", embedding_model="e",
            job_root=str(tmp_path),
        ))
    assert list(tmp_path.iterdir()) == []


def test_local_ollama_routes_use_only_loopback_and_no_cloud_key_placeholders(tmp_path: Path) -> None:
    generated = _generate_settings(
        tmp_path,
        completion_provider="ollama_chat",
        completion_model="qwen3:8b",
        embedding_provider="ollama",
        embedding_model="qwen3-embedding:0.6b",
    )
    completion = generated.settings["completion_models"]["index_completion"]
    embedding = generated.settings["embedding_models"]["index_embedding"]
    for model in (completion, embedding):
        assert model["api_base"] == "http://127.0.0.1:11434"
        assert model["api_key"] == "ollama"
    assert completion["model_provider"] == "ollama_chat"
    assert completion["model"] == "qwen3:8b"
    assert embedding["model_provider"] == "ollama"
    assert embedding["model"] == "qwen3-embedding:0.6b"
    encoded = json.dumps(generated.settings, sort_keys=True)
    assert "INDEX_COMPLETION_API_KEY" not in encoded
    assert "INDEX_EMBEDDING_API_KEY" not in encoded


def test_qwen3_embedding_config_matches_its_observed_1024_dimensions(tmp_path: Path) -> None:
    generated = _generate_settings(
        tmp_path,
        completion_provider="ollama_chat",
        completion_model="qwen3:8b",
        embedding_provider="ollama",
        embedding_model="qwen3-embedding:0.6b",
    )
    assert generated.settings["vector_store"]["vector_size"] == 1024


def test_dev_container_local_ollama_routes_use_fixed_host_gateway(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    monkeypatch.setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", "true")
    generated = _generate_settings(
        tmp_path,
        completion_provider="ollama_chat",
        completion_model="qwen3:8b",
        embedding_provider="ollama",
        embedding_model="qwen3-embedding:0.6b",
    )
    completion = generated.settings["completion_models"]["index_completion"]
    embedding = generated.settings["embedding_models"]["index_embedding"]
    assert completion["api_base"] == "http://host.docker.internal:11434"
    assert embedding["api_base"] == "http://host.docker.internal:11434"


@pytest.mark.parametrize("value", ["http://remote.example:11434", "false", "TRUE"])
def test_non_boolean_dev_ollama_setting_cannot_override_endpoint(monkeypatch: pytest.MonkeyPatch, tmp_path: Path, value: str) -> None:
    monkeypatch.setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", value)
    generated = _generate_settings(
        tmp_path,
        completion_provider="ollama_chat",
        completion_model="qwen3:8b",
        embedding_provider="ollama",
        embedding_model="qwen3-embedding:0.6b",
    )
    assert generated.settings["completion_models"]["index_completion"]["api_base"] == "http://127.0.0.1:11434"
    assert generated.settings["embedding_models"]["index_embedding"]["api_base"] == "http://127.0.0.1:11434"


def test_cloud_routes_keep_environment_key_placeholders(tmp_path: Path) -> None:
    generated = _generate_settings(
        tmp_path,
        completion_provider="openai",
        completion_model="index-completion-v1",
        embedding_provider="openai",
        embedding_model="index-embedding-v1",
    )
    assert generated.settings["completion_models"]["index_completion"]["api_key"] == "${INDEX_COMPLETION_API_KEY}"
    assert generated.settings["embedding_models"]["index_embedding"]["api_key"] == "${INDEX_EMBEDDING_API_KEY}"
