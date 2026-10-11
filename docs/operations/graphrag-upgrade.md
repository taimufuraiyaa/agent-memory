# GraphRAG dependency upgrade and rollback

Agent Memory consumes Microsoft GraphRAG as an exact, replaceable PyPI dependency behind the isolated indexing adapter. It is never fetched or upgraded at runtime, and it is never part of the online retrieval process.

An upgrade must change the exact `graphrag==X.Y.Z` pin, regenerate and review `uv.lock` and the offline wheelhouse, and update the reviewed policy version. Automated dependency-update pull requests are not accepted. The graph-index owner, security, privacy, and operations reviewers must approve the candidate.

Run `make graphrag-adapter-supply-chain` while developing. This checks the exact pin, lock, offline wheel hashes, adapter tests, and artifact supply-chain metadata.

Local validation additionally runs the deterministic GraphRAG evaluation and the adapter container test. No remote deployment or service release gate is part of the local product.

Any adapter, GraphRAG, artifact schema, prompt fingerprint, or model-route change creates a new graph configuration and full rebuild. On failure, disable graph routing, reactivate the prior local revision, and verify Basic retrieval.
