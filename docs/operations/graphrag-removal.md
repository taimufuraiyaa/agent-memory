# GraphRAG Disablement and Removal

GraphRAG is removable because it owns no canonical truth. Removal must preserve every memory, source record, review of canonical data, Basic vector index, and retrieval API.

## Reversible disablement

1. Change workspace routes to Basic and confirm graph fallback traffic reaches zero.
2. Use the authorized `disable` operation for each configuration and record its audit result.
3. Cancel queued/running graph jobs and wait for lease termination. Confirm no pending revision can activate.
4. Set `AGENT_MEMORY_GRAPHRAG_ENABLED=false` for API/worker deployments and remove the GraphRAG overlay in a reviewed deployment change.
5. Verify writes, Basic search, Basic recall, source ingestion, deletion, export, and restore without the adapter or graph worker.

This state is reversible: retain compatible normalized revisions and immutable derived objects only for their approved retention period. Re-enablement begins with readiness checks and an explicit full rebuild if any configuration, prompt, model, projection, adapter, or artifact-schema fingerprint changed.

## Permanent derived-data removal

After the retention/legal hold decision, delete only graph-derived records and objects for the exact tenant/workspace scope: jobs and dead letters, projection bundles, staged artifacts, adapter state, normalized revisions/entities/edges/communities/reports/evidence, graph caches, graph feedback/review data where policy permits, and Graph-specific metrics with tenant-identifying dimensions. Keep deletion receipts and content-safe audit evidence.

Remove the adapter image reference, worker deployment, GraphRAG Kubernetes component/overlay, Graph alert/dashboard rules, and Graph-specific credentials only after all workspaces are disabled and no job lease remains. Remove the exact Python package through a reviewed dependency change; never mutate the canonical database to “clean up” graph associations.

## Acceptance

Run canonical inventory hashes before and after removal and require equality apart from normal concurrent canonical writes. Prove Basic retrieval and writes operate with the Graph worker and adapter absent. Prove a deleted memory/source is absent from search and any retained derived backup according to policy. Record object-prefix and normalized-row counts at zero for removed scopes. A future installation must be able to rebuild solely from authorized canonical data.
