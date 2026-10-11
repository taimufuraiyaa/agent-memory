# Graph processing after memory inserts

Three independent observations determine whether a new memory participates in a graph:

- `internal/engine/write_pipeline.go` calls `inferRelationships` after an accepted, nonduplicate insert. This is best effort. Session metadata enables temporal/outcome links; supplied entities enable overlap links against up to 500 recent non-cold memories. Plain content or locator keywords alone do not enable these branches. Check actual incident `relations` rows in both directions; a successful write does not guarantee a new edge.
- `internal/storage/sqlite/graph_change.go` records a `graph_change_journal` entry in the canonical write transaction for each enabled graph configuration. This hook records scheduling intent and never invokes the GraphRAG adapter. Missing supplied entities/session metadata does not prevent GraphRAG from extracting entities from projected memory text.
- A usable GraphRAG index requires a successfully activated revision. Configuration readiness, `enabled=1`, and pending journal rows do not establish successful indexing. Compare the active revision, job/revision states, indexed watermark, and revision-bound graph artifacts.

## Confirm worker execution before retrying

Inspect the installed CLI revision and the corresponding source. Search production call sites for the graph coordinator, `ClaimGraphJobs`, and `NewLocalGraphRunner`; declarations and tests do not prove runtime integration. Inspect the daemon's scheduler to establish whether it processes graph jobs or only lifecycle maintenance. Check the actual daemon/container state separately.

At the unmodified verified revision `028f41447ab2a7a5b80356311e10d73cd93d91dc` (Agent Memory 0.7), the native daemon only schedules lifecycle maintenance. The graph primitives have no production callers. Graph operations enqueue jobs, but a configuration change or daemon restart alone cannot complete them. This is a historical build-specific finding; a modified build can share that base revision while including the new worker. Check build identity and actual production call sites, then follow [native worker operations](native-worker-operations.md) for the current implementation. Earlier development-container worker instructions apply only when that runtime exists and is running.

Check `AGENT_MEMORY_GRAPH_ADAPTER` for the configured absolute adapter executable; `internal/config/config.go` maps that variable into `GraphConfig.Executable`. Probe adapter readiness with a minimal child environment, and inspect local model inventory separately. Neither establishes full indexing success.

## Inspect project metadata

Use `agent-memory` on PATH for supported status/readiness operations, with the explicitly authorized workspace. Resolve a SQLite database from the project registry (`workspaces.json`) and query read-only or use a consistent diagnostic snapshot. Preserve WAL data when applicable; do not use SQLite `immutable=1` on a live database to work around an open error. Do not change graph configuration, retry a job, or index a real corpus merely to answer a status question.

The following queries use a bound `:workspace` parameter and expose metadata only. Confirm the installed schema before executing them.

```sql
SELECT id, enabled, active_revision_id, updated_at
FROM graph_configurations WHERE workspace = :workspace;

SELECT id, state, attempt, updated_at
FROM graph_jobs WHERE workspace = :workspace
ORDER BY updated_at DESC LIMIT 5;

SELECT change_kind, COUNT(*) AS total,
       SUM(CASE WHEN processed_revision_id = '' THEN 1 ELSE 0 END) AS pending,
       MAX(occurred_at) AS latest_change
FROM graph_change_journal WHERE workspace = :workspace
GROUP BY change_kind;

SELECT COUNT(*) AS entities FROM graph_entities WHERE workspace = :workspace;
SELECT COUNT(*) AS edges FROM graph_edges WHERE workspace = :workspace;

SELECT r.type, json_extract(r.metadata_json, '$.subtype') AS subtype,
       COUNT(*) AS total, MAX(r.created_at) AS latest
FROM relations r JOIN memories m ON m.id = r.source_id
WHERE m.workspace = :workspace GROUP BY r.type, subtype;
```

For the latest insert, match its memory ID to `graph_change_journal.subject_id`. Existing `supersedes` edges alone do not prove entity or session inference. Report a failed job and a missing active revision separately from a working insert journal. Timestamp runtime findings because counts and job states change.
