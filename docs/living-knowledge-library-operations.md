# Living Knowledge Library operations

The `AGENT_MEMORY_LIBRARY_ENABLED` flag defaults to enabled after the migration,
recovery, deletion, authorization, and evaluation gates pass. Set it to `false`
for an immediate route-level rollback; existing memories and library records remain
unchanged.

Upgrade and recovery procedure:

1. Back up the workspace SQLite database and source vault.
2. Open the workspace to apply additive, idempotent migrations.
3. Run interrupted-job recovery; `running` imports become `queued`.
4. Re-run the queued import with the same source fingerprint. Import identity is
   idempotent, so already published editions are reused.
5. Rebuild retrieval indexes with `RebuildLibraryIndexes`; durable citations,
   memories, annotations, and graph records are not overwritten.
6. Verify memory counts and a sample of historical citations before enabling traffic.

Retention enforcement keeps retained sources queryable. On-demand, session-only,
and deleted assets are removed from retrieval. Historical citation and memory lineage
remain auditable; a fully deleted source cannot support new grounded answers or new
quote verification.
