# GraphRAG Incident Runbook

## First response

Preserve Agent Memory writes and Basic retrieval first. Identify affected topology, tenants/workspaces, active and previous revision IDs, configuration version, current job, adapter image digest, and earliest alert time without copying customer content into the incident record.

If graph results are unsafe, stale, cross-tenant, ungrounded, or broadly unavailable, set affected workspaces to Basic/disabled. For fleet-wide risk, set `AGENT_MEMORY_GRAPHRAG_ENABLED=false` through the normal deployment rollback. Do not delete canonical memories, rotate database state manually, or activate a pending revision to restore service.

## Classification and containment

| Signal | Containment | Evidence to preserve |
|---|---|---|
| Provider or model outage | Leave writes and Basic live; stop/retry graph jobs after recovery | Job IDs, bounded reason, model route, cost counters |
| Queue saturation or poison job | Cancel the job, quarantine its projection/artifacts, enforce workspace limits | Queue age, attempts, manifest digests, dead-letter transition |
| Worker crash | Confirm lease expiry and idempotent reclaim; verify no activation occurred | Lease owner/time, revision state, audit events |
| Corrupt or malicious artifact | Reject before import/activation; quarantine immutable objects | Signature, manifest/hash validation reason, adapter digest |
| Object or database outage | Stop graph processing; keep Basic; recover dependencies before replay | Dependency health, pending/active IDs, RPO/RTO timestamps |
| Credential revocation | Disable the workload identity and reject new claims | Identity ID, revocation time, post-revocation denial evidence |
| Suspected tenant leak | Disable Graph fleet-wide, invoke security/privacy response, preserve audit custody | Request/trace IDs, tenant scopes, authorization decisions |
| Deletion during indexing | Cancel affected job, purge derived evidence, rebuild from remaining canonical data | Deletion receipt, artifact prefixes, post-rebuild absence proof |
| Bad active revision | Roll back to the previous compatible revision or Basic | Expected/current revision IDs, rollback audit, Basic checks |

## Recovery procedure

1. Confirm Basic search/recall and canonical writes succeed before touching Graph.
2. Cancel or allow leases to expire; never run two owners for one job lease.
3. Validate the previous revision and its configuration fingerprint. Roll back atomically if safe; otherwise disable Graph.
4. Repair the dependency. For corrupt derived state, purge only the exact tenant/workspace graph scope and immutable derived prefixes, then perform a full canonical-only rebuild.
5. Verify artifact signature/schema/hash/evidence policies and tenant authorization before activation.
6. Exercise direct, Day-1/Day-10 relational, global, contradiction, ambiguity, deletion, and provider-failure journeys. Confirm Basic remained within its SLO.
7. Re-enable only explicit routes first and observe. Auto routes require a new accountable decision if the incident invalidated their evidence window.

## Exit criteria

Close only when the root cause and affected scope are known; no partial revision is active; deletion and tenant-isolation checks pass; Basic and writes stayed available or their separate incident is resolved; freshness, latency, cost, queue, and fallback metrics are healthy; and security/privacy/operations owners accept the recovery evidence. Link the incident to any superseded production or upgrade approval report.
