# Launch Dashboard Contract

| Dashboard | Required signals | Primary owner |
|---|---|---|
| Funnel | signup, attestation, upload, ready, query, memory, export, deletion | product |
| API | availability, rate, p50/p95/p99 latency, errors, saturation | API on-call |
| Jobs | queue depth, oldest age, attempts, dead letters, processing state | ingestion on-call |
| Deletion | immediate revocations, pending subsystems, oldest operation, receipts | privacy on-call |
| Security | auth denials, anomaly findings, containment, audit/archive integrity | security on-call |
| Billing | usage lag, reconciliation delta, quota blocks, webhook age | billing on-call |
| Support | open cases, first response, escalation age, elevated access | support lead |
| Cost | storage, embedding, generation, queue, database, cost per active tenant | FinOps owner |

Dashboards consume content-free metrics and safe identifiers only. Every panel
used by a release gate has a stable `source_ref`, named owner, threshold, and
observation window. Feature flags support normal, reduced, read-only, and
uploads-paused workload modes; changing mode is audited and never deletes data.

