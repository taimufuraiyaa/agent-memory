# Operational Readiness and Game-Day Runbook

## Ownership contract

Every failure class in `readiness.RequiredFailureClasses` must have a named
on-call owner, resolution target, and escalation policy in
`saas_failure_ownership`. A release review fails if any class is unowned. Owner
names are team/on-call aliases rather than personal data.

## Incident flow

1. Declare severity from customer impact and isolation risk; suspected
   cross-tenant exposure is severity one until disproven.
2. Preserve request, trace, audit, job, deployment, and provider references.
   Never copy source content into an incident ticket or chat.
3. Contain with the narrowest audited action: rate limit, revoke credential,
   quarantine upload, disable source, pause workload, or suspend tenant.
4. Assign incident commander, operations lead, communications lead, and the
   domain owner. Publish status updates on the declared cadence.
5. Recover from authority, reconcile projections/objects/events, verify tenant
   isolation, and document safe evidence before removing containment.

## Required game days

| Scenario | Exercise | Pass condition |
|---|---|---|
| Database failover | Stop the primary during writes | committed state survives; RPO ≤5m and RTO ≤60m |
| Queue backlog | Pause consumers and inject duplicates | business state survives; lag alert fires; replay is idempotent |
| Model provider outage | Force timeouts/circuit open | cited evidence returns without fabricated synthesis |
| Credential leak | Replay a scoped credential | alert, revoke, and containment are audited within target |
| Cross-tenant attempt | Substitute IDs and object keys | no existence signal or content; security finding is explainable |
| Incomplete deletion | Fail one subsystem confirmation | access stays revoked; operation remains visible and retries |

`readiness.RunDrill` records only a safe summary and SHA-256 evidence digest.
A failed check is evidence of a failed drill; it cannot be marked passed by
editing the report. Database restore, source/account deletion, billing webhook,
notice, and migration rollback use their domain-specific runbooks and receipts.

`make saas-local-alpha-gate` rehearses the model, credential, isolation, and
deletion application paths with synthetic fixtures and content-free receipts.
It is a wiring check only: staging must repeat the scenarios with the selected
providers, real alert routes, accountable responders, measured targets, and
immutable external evidence.

## Alert ownership

Pages require an owner for API availability/latency, database saturation,
queue depth and oldest job, object errors, model failure/cost, deletion age,
audit archive lag/integrity, billing reconciliation, signup abuse, and support
response. Synthetic alerts must be routed to a test channel before production.
