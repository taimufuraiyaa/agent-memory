from pathlib import Path


DOCKERFILE = (Path(__file__).parents[1] / "Dockerfile").read_text(encoding="utf-8")


def test_runtime_forces_litellm_to_use_its_bundled_cost_map():
    assert "LITELLM_LOCAL_MODEL_COST_MAP=true" in DOCKERFILE


def test_runtime_entrypoint_is_the_pinned_python_adapter():
    assert 'ENTRYPOINT ["/opt/adapter/.venv/bin/agent-memory-graphrag"]' in DOCKERFILE
    assert "COPY --chown=65532:65532 wheelhouse/" not in DOCKERFILE
