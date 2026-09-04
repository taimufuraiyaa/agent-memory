from pathlib import Path


DOCKERFILE = (Path(__file__).parents[1] / "Dockerfile").read_text(encoding="utf-8")


def test_runtime_forces_litellm_to_use_its_bundled_cost_map():
    assert "LITELLM_LOCAL_MODEL_COST_MAP=true" in DOCKERFILE


def test_runtime_entrypoint_is_the_graph_worker_supervisor():
    assert 'ENTRYPOINT ["/usr/local/bin/agent-memory-graph-worker"]' in DOCKERFILE
    assert "COPY --chown=65532:65532 wheelhouse/agent-memory-graph-worker /usr/local/bin/agent-memory-graph-worker" in DOCKERFILE
