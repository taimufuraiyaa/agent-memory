#!/usr/bin/env python3
"""Isolated native CLI/HTTP worker smoke, with a synthetic offline adapter.

Uses the real installed ONNX embedding provider. Real GraphRAG/model proof is
the separate TestNativeGraphRealLocalAdapter integration test.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from urllib.request import Request, urlopen


ADAPTER = r'''import hashlib,json,sys
from datetime import datetime,timezone
from pathlib import Path
command=sys.argv[1]
if command=="readiness":
 print(json.dumps({"contract_version":"graph-adapter/v1","state":"ready"})); raise SystemExit(0)
envelope=json.loads(Path(sys.argv[3]).read_text())
payload=envelope["request"]; root=Path(envelope["job_root"])/"output"/"normalized"
root.mkdir(parents=True,mode=0o700)
evidence=[{"canonical_kind":v["canonical_kind"],"canonical_id":v["canonical_id"],"canonical_fingerprint":v["canonical_fingerprint"]} for v in payload["correlations"].values()]
files={
 "entities.jsonl":[{"id":"pricing","name":"Pricing","type":"decision","evidence":evidence},{"id":"runtime","name":"Runtime","type":"service","evidence":evidence}],
 "relationships.jsonl":[{"id":"pricing-runtime","source_id":"pricing","target_id":"runtime","kind":"related","description":"Pricing is connected to runtime cost","evidence":evidence}],
 "communities.jsonl":[{"id":"pricing-community","entity_ids":["pricing","runtime"]}],
 "community_reports.jsonl":[{"id":"pricing-report","community_id":"pricing-community","title":"Pricing and runtime","summary":"Pricing is connected to runtime cost.","evidence":evidence}],
}
outputs=[]
for name,rows in files.items():
 data=b"".join(json.dumps(row,separators=(",",":")).encode()+b"\n" for row in rows)
 path=root/name; path.write_bytes(data); path.chmod(0o600)
 outputs.append({"name":name,"kind":name.removesuffix(".jsonl"),"required":True,"bytes":len(data),"rows":len(rows),"schema_fingerprint":"sha256:synthetic-v1","content_hash":"sha256:"+hashlib.sha256(data).hexdigest()})
manifest={"contract_version":"graph-adapter/v1","artifact_schema_version":"graph-artifact/v1","scope":payload["scope"],"configuration_id":payload["configuration_id"],"job_id":payload["job_id"],"revision_id":payload["revision_id"],"adapter_name":"agent-memory-graphrag-adapter","adapter_version":"0.1.0","graphrag_version":"3.1.2","python_version":"3.13.5","environment_fingerprint":"sha256:synthetic-env","configuration_fingerprint":"sha256:synthetic-config","input_manifest_hash":payload["input_manifest_hash"],"prompt_fingerprint":payload["prompt_fingerprint"],"index_method":"standard","mode":"full","outputs":outputs,"models":[payload["completion_model"],payload["embedding_model"]],"status":"completed","completed_at":datetime.now(timezone.utc).isoformat().replace("+00:00","Z"),"attestation":{"producer_identity":payload["producer_identity"],"build_digest":payload["build_digest"],"signature":payload["attestation_signature"]}}
(root/"artifact-manifest.json").write_text(json.dumps(manifest)); (root/"artifact-manifest.json").chmod(0o600)
print(json.dumps({"contract_version":"graph-adapter/v1","state":"completed"}))
'''


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default=shutil.which("agent-memory"))
    parser.add_argument("--model-dir", default=str(Path.home() / ".agent-memory" / "models" / "all-MiniLM-L6-v2"))
    args = parser.parse_args()
    if not args.binary:
        raise SystemExit("agent-memory is unavailable on PATH")
    binary = Path(args.binary).resolve(strict=True)
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix="agent-memory-native-daemon-smoke-") as temporary:
        root = Path(temporary)
        project = root / "project"
        project.mkdir()
        executable_dir = root / "bin"
        executable_dir.mkdir()
        shutil.copy2(binary, executable_dir / "agent-memory")
        adapter = root / "synthetic-adapter"
        adapter.write_text(f"#!{sys.executable}\n" + ADAPTER)
        adapter.chmod(0o700)
        environment = {key: value for key, value in os.environ.items()
                       if not key.startswith("AGENT_MEMORY_HARNESS_") and key != "MEMORY_API_URL"}
        environment.update({
            "PATH": str(executable_dir) + os.pathsep + environment.get("PATH", ""),
            "AGENT_MEMORY_DATA_DIR": str(root),
            "AGENT_MEMORY_GRAPH_ENABLED": "true",
            "AGENT_MEMORY_GRAPH_ADAPTER": str(adapter),
            "AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER": "ollama_chat",
            "AGENT_MEMORY_GRAPH_COMPLETION_MODEL": "synthetic-completion",
            "AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER": "ollama",
            "AGENT_MEMORY_GRAPH_EMBEDDING_MODEL": "synthetic-embedding",
        })
        # CLI persisted settings override process variables. Select a private
        # file instead of loading or modifying the real user's saved settings.
        settings = root / "isolated.env"
        settings.write_text("".join(f"{key}={json.dumps(value)}\n"
                                    for key, value in environment.items()
                                    if key.startswith("AGENT_MEMORY_GRAPH_")))
        settings.chmod(0o600)
        environment["AGENT_MEMORY_ENV_FILE"] = str(settings)

        def command(*values: str) -> dict:
            result = subprocess.run(["agent-memory", *values], cwd=project,
                                    env=environment, capture_output=True, text=True, timeout=30)
            if result.returncode:
                raise RuntimeError(f"CLI {values[0]} failed: {result.stderr.strip()}")
            return json.loads(result.stdout)["data"]

        command("init", "--base-dir", str(root), "--project-name", "agent-memory", "--no-rule")
        database = root / "agent-memory.db"
        command("graph-index", "enable", "--workspace", "agent-memory", "--db", str(database))
        pid_file = root / "serve.pid"
        log = (root / "daemon.log").open("w+")
        daemon = subprocess.Popen(["agent-memory", "serve", "--no-open", "--addr", "127.0.0.1:0",
                                   "--db", str(root / ".serve-placeholder.db"), "--pid-file", str(pid_file),
                                   "--model-dir", args.model_dir], cwd=project, env=environment,
                                  stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 30
            while not pid_file.exists():
                if daemon.poll() is not None or time.monotonic() > deadline:
                    log.seek(0)
                    raise RuntimeError("isolated daemon failed to start: " + log.read())
                time.sleep(0.1)
            url = json.loads(pid_file.read_text())["url"]

            def api(path: str, data: dict | None = None) -> dict:
                request = Request(url + path, data=None if data is None else json.dumps(data).encode(),
                                  headers={"Content-Type": "application/json"})
                with urlopen(request, timeout=30) as response:
                    return json.load(response)["data"]

            status_path = "/api/v1/graph-index/status?workspace=agent-memory&configuration_id=default"

            def wait_fresh(previous: str = "") -> dict:
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    status = api(status_path)
                    if status["fresh"] and status["active_revision_id"] != previous:
                        return status
                    time.sleep(0.2)
                raise RuntimeError("isolated worker did not activate a fresh revision: " + json.dumps(status))

            api("/api/v1/memories/write", {"workspace": "agent-memory", "type": "semantic",
                                         "content": "Pricing depends on runtime operating cost and benchmark throughput."})
            first = wait_fresh()
            write = api("/api/v1/memories/write", {"workspace": "agent-memory", "type": "semantic",
                                                  "content": "Latest pricing correction changes runtime operating cost and measured throughput."})
            stale = api(status_path)
            assert stale["pending_changes"] == 1 and not stale["fresh"], stale
            query = "How does pricing depend on runtime operating cost?"
            discovery = api("/api/v1/memories/search", {"workspace": "agent-memory", "query": query,
                              "top_k": 8, "graph_mode": "local_graph", "graph_allow_stale": True})
            assert discovery["graph_route"]["selected_mode"] == "local_graph", discovery["graph_route"]
            assert len(discovery["results"]) == 2, discovery["result_bands"]
            api("/api/v1/graph-index/operations", {"scope": {"workspace_id": "agent-memory"},
                 "configuration_id": "default", "action": "update", "idempotency_key": "smoke-update",
                 "expected_revision": first["active_revision_id"]})
            final = wait_fresh(first["active_revision_id"])
            http_result = api("/api/v1/memories/search", {"workspace": "agent-memory", "query": query,
                             "top_k": 8, "graph_mode": "local_graph", "graph_required": True})
            cli_result = command("search", "--workspace", "agent-memory", "--api", url, "--query", query,
                                 "--graph-mode", "local_graph", "--graph-required", "--top-k", "8")
            assert {item["id"] for item in http_result["results"]} == {item["id"] for item in cli_result["results"]}
            assert http_result["graph_context"]["local"]["paths"]
            print(json.dumps({"binary_sha256": binary_hash, "native_daemon": "passed",
                              "automatic_insert_index": "passed", "fresh_unindexed_hit": "passed",
                              "operator_update": "passed", "cli_http_parity": "passed",
                              "active_revision": final["active_revision_id"], "pending": final["pending_changes"]}))
        finally:
            daemon.send_signal(signal.SIGTERM)
            daemon.wait(timeout=20)
            log.close()


if __name__ == "__main__":
    main()
