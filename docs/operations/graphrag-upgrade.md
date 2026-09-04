# GraphRAG dependency upgrade and rollback

Agent Memory consumes Microsoft GraphRAG as an exact, replaceable PyPI dependency behind the isolated indexing adapter. It is never fetched or upgraded at runtime, and it is never part of the online retrieval process.

An upgrade must change the exact `graphrag==X.Y.Z` pin, regenerate and review `uv.lock` and the offline wheelhouse, and update the reviewed policy version. Automated dependency-update pull requests are not accepted. The graph-index owner, security, privacy, and operations reviewers must approve the candidate.

Run `GRAPHRAG_UPGRADE_POLICY_ONLY=1 make graphrag-upgrade-certify` while developing. This checks the exact pin, lock, offline wheel hashes, adapter tests, graph contracts, malicious-artifact validation, full/update worker paths, and deterministic shadow evaluation. It does not grant release approval.

Release certification additionally requires a digest-pinned, signed candidate image and a signed `agent-memory-graphrag-upgrade-report/v1`. The report records SBOM, license and vulnerability review; schema/golden/determinism results; full and incremental canaries; normalized artifact diff; shadow quality/latency/cost; deployment canary; and proof that rollback restored both the prior image digest and active derived revision.

Any adapter, GraphRAG, artifact schema, prompt fingerprint, or model-route change creates a new graph configuration and full rebuild. Keep the prior image digest and prior active revision until the canary and observation window are approved. On failure, disable graph routing, restore the prior digest, atomically reactivate the prior revision, verify Basic retrieval, and retain the failed candidate only for bounded incident evidence.
