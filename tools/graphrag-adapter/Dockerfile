# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
ARG PYTHON_IMAGE=python:3.13.14-slim@sha256:9662417aace5ae7b8e2609cce472b72a8958e134ba372808abe9cc1a0c0125e6
ARG UV_IMAGE=ghcr.io/astral-sh/uv:0.12.1@sha256:cf4eedcaa81655197f625739489effcbe71b61ceb1506f332c3facae5deceded

FROM ${UV_IMAGE} AS uv

FROM ${PYTHON_IMAGE} AS build
COPY --from=uv /uv /usr/local/bin/uv
ENV UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never UV_NO_INDEX=1
WORKDIR /build
COPY uv.lock ./
COPY wheelhouse ./wheelhouse
RUN cd wheelhouse \
 && sha256sum --check adapter-wheel.sha256 \
 && cd /build \
 && uv venv --python /usr/local/bin/python3 /opt/adapter/.venv \
 && uv pip install --python /opt/adapter/.venv/bin/python --no-index --find-links /build/wheelhouse --require-hashes --requirement wheelhouse/requirements.txt \
 && uv pip install --python /opt/adapter/.venv/bin/python --no-index --find-links /build/wheelhouse --no-deps agent-memory-graphrag-adapter==0.1.0 \
 && find /opt/adapter/.venv -type d -name __pycache__ -prune -exec rm -rf '{}' + \
 && find /opt/adapter/.venv -type f -name '*.pyc' -delete

FROM ${PYTHON_IMAGE} AS runtime
ENV PATH=/opt/adapter/.venv/bin:$PATH \
    AGENT_MEMORY_GRAPHRAG_LOCK_FILE=/opt/adapter/uv.lock \
    LITELLM_LOCAL_MODEL_COST_MAP=true \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    HOME=/nonexistent
RUN groupadd --system --gid 65532 adapter \
 && useradd --system --uid 65532 --gid 65532 --home-dir /nonexistent --shell /usr/sbin/nologin adapter \
 && rm -rf /usr/local/lib/python*/site-packages/pip* /usr/local/bin/pip* /usr/bin/apt* /usr/bin/dpkg* /var/lib/apt/lists/*
COPY --from=build --chown=65532:65532 /opt/adapter/.venv /opt/adapter/.venv
COPY --from=build --chown=65532:65532 /build/uv.lock /opt/adapter/uv.lock
COPY --chown=65532:65532 wheelhouse/agent-memory-graph-worker /usr/local/bin/agent-memory-graph-worker
WORKDIR /graph-job
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/agent-memory-graph-worker"]
