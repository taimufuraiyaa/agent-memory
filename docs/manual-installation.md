# Manual Installation Guide

This guide covers how to install **agent-memory** and its dependencies in environments that cannot reach the internet, such as air-gapped servers, corporate networks with outbound proxy restrictions, or CI pipelines running behind a firewall.

---

## Prerequisites

| Component | Minimum Version | Notes |
|-----------|----------------|-------|
| Go        | 1.22            | Build from source |
| ONNX Runtime | 1.16 – 1.19  | Required for local MiniLM embeddings |
| `libonnxruntime.{so,dylib,dll}` | same as above | Shared library, loaded at runtime |

---

## 1 · Build agent-memory from Source

```bash
# Clone the repository on a machine that has internet access
git clone https://github.com/your-org/agent-memory
cd agent-memory

# Download all Go module dependencies into a vendor directory
go mod vendor

# Archive the entire project including vendor/
tar czf agent-memory-offline.tar.gz .
```

Transfer `agent-memory-offline.tar.gz` to the air-gapped machine, then:

```bash
tar xzf agent-memory-offline.tar.gz
cd agent-memory

# Build using vendored dependencies only (no network access needed)
go build -mod=vendor -o agent-memory ./cmd/agent-memory
```

---

## 2 · Download ONNX Runtime

ONNX Runtime provides the inference engine for the MiniLM sentence-embedding model.

### macOS (Apple Silicon / arm64)

```bash
VERSION=1.19.2
curl -LO https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-osx-arm64-${VERSION}.tgz
tar xzf onnxruntime-osx-arm64-${VERSION}.tgz
# Copy the shared library to a system library path or set DYLD_LIBRARY_PATH
cp onnxruntime-osx-arm64-${VERSION}/lib/libonnxruntime.dylib /usr/local/lib/
```

### macOS (Intel / x86_64)

```bash
VERSION=1.19.2
curl -LO https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-osx-x86_64-${VERSION}.tgz
tar xzf onnxruntime-osx-x86_64-${VERSION}.tgz
cp onnxruntime-osx-x86_64-${VERSION}/lib/libonnxruntime.dylib /usr/local/lib/
```

### Linux (x86_64)

```bash
VERSION=1.19.2
curl -LO https://github.com/microsoft/onnxruntime/releases/download/v${VERSION}/onnxruntime-linux-x64-${VERSION}.tgz
tar xzf onnxruntime-linux-x64-${VERSION}.tgz
cp onnxruntime-linux-x64-${VERSION}/lib/libonnxruntime.so.${VERSION} /usr/local/lib/
ldconfig   # refresh the dynamic linker cache
```

### Windows (x86_64)

Download from the [ONNX Runtime releases page](https://github.com/microsoft/onnxruntime/releases) and place `onnxruntime.dll` alongside the `agent-memory.exe` binary or in a directory on `%PATH%`.

> **Tip (offline transfer):** Download these archives on an internet-connected machine, verify their SHA-256 checksums from the GitHub release page, then transfer via USB, internal artifact store, or S3-compatible object storage.

---

## 3 · Download the MiniLM Model Files

agent-memory uses the **all-MiniLM-L6-v2** sentence-transformer model converted to ONNX format.

The default model directory is `~/.agent-memory/models/` (overridable via `--model-dir`).

### Files Required

```
<model-dir>/
  model.onnx           # ONNX graph weights
  tokenizer.json       # Tokenizer vocabulary
  tokenizer_config.json
  special_tokens_map.json
  config.json          # Model architecture metadata
```

### Downloading on an Internet-Connected Machine

```bash
pip install huggingface_hub

python3 - <<'EOF'
from huggingface_hub import snapshot_download
snapshot_download(
    repo_id="sentence-transformers/all-MiniLM-L6-v2",
    allow_patterns=["*.onnx", "*.json"],
    local_dir="./minilm-l6-v2",
)
EOF

# Transfer the directory to the air-gapped machine
tar czf minilm-l6-v2.tar.gz minilm-l6-v2/
```

On the target machine:

```bash
mkdir -p ~/.agent-memory/models
tar xzf minilm-l6-v2.tar.gz -C ~/.agent-memory/models/ --strip-components=1
```

Alternatively, download the ONNX file directly:

```bash
MODEL_URL="https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model.onnx"
curl -L "$MODEL_URL" -o ~/.agent-memory/models/model.onnx
```

> **Checksum verification:** Always verify the SHA-256 hash of model files against the values published on the Hugging Face model card before deploying to production.

---

## 4 · Verify the Installation

```bash
# Check that the binary finds the shared library and loads the model
./agent-memory serve --model-dir ~/.agent-memory/models --no-open &

# Health check – embedding_provider should not be "unknown"
curl -s http://localhost:3211/health | python3 -m json.tool
```

Expected output:

```json
{
  "status": "ok",
  "embedding_provider": "onnx-minilm-l6-v2",
  "embedding_model_version": "minilm-l6-v2-fp32",
  "onnx_runtime_available": true,
  "db_size_mb": 0.0,
  "memory_count": 0,
  "last_lifecycle_run": ""
}
```

If `onnx_runtime_available` is `false`, the shared library was not found. Ensure the library is in a path that the dynamic linker searches:

```bash
# Linux
export LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH

# macOS
export DYLD_LIBRARY_PATH=/usr/local/lib:$DYLD_LIBRARY_PATH
```

---

## 5 · Corporate Proxy Configuration

If your environment has an HTTP/HTTPS proxy but it is **not** fully air-gapped:

```bash
# Set proxy environment variables before running go commands
export HTTPS_PROXY=http://proxy.corp.example.com:3128
export GOPROXY=https://goproxy.io,direct   # or your internal GOPROXY mirror
export GONOSUMCHECK="*"                     # if your proxy terminates TLS

go mod download
```

For Hugging Face downloads:

```bash
HF_HUB_DISABLE_PROGRESS_BARS=1 \
  HTTPS_PROXY=http://proxy.corp.example.com:3128 \
  python3 -c "from huggingface_hub import snapshot_download; snapshot_download('sentence-transformers/all-MiniLM-L6-v2')"
```

---

## 6 · Internal Artifact Mirror (Recommended for Teams)

For repeatable deployments, host model files and ONNX Runtime releases in an internal artifact repository (e.g. JFrog Artifactory, Nexus, or Gitea LFS):

```bash
# Upload once from a machine with internet access
artifactory-cli rt u minilm-l6-v2.tar.gz    ml-models/minilm-l6-v2.tar.gz
artifactory-cli rt u onnxruntime-linux-x64-1.19.2.tgz  ml-deps/

# Download in CI/CD or on target machines
curl -u $ARTIFACTORY_USER:$ARTIFACTORY_TOKEN \
  https://artifacts.corp.example.com/ml-models/minilm-l6-v2.tar.gz \
  -o minilm-l6-v2.tar.gz
```

---

## 7 · Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| `libonnxruntime.so: cannot open shared object` | Library not on `LD_LIBRARY_PATH` | Add library directory to `LD_LIBRARY_PATH` or run `ldconfig` |
| `embedding_provider: unknown` in `/health` | Model files missing or wrong path | Check `--model-dir`; ensure `model.onnx` exists |
| `model.onnx: no such file` | Incomplete model download | Re-download model files and verify checksums |
| `go: updates to go.mod needed` | Network required for `go mod tidy` | Use `go build -mod=vendor` with the vendored directory |
| Slow first request (~1–3 s) | ONNX Runtime JIT warm-up | Expected; subsequent requests are fast |

---

## References

- [ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases)
- [Hugging Face all-MiniLM-L6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2)
- [Go module proxy protocol](https://go.dev/ref/mod#module-proxy)
