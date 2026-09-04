# GraphRAG Production Gate

## Decision boundary

Passing repository tests is necessary but is not production approval. GraphRAG may affect a default route only when Tasks 1–41 are complete and a current signed `agent-memory-graphrag-production-approval/v1` report passes `make graphrag-production-gate`. The gate is deliberately fail-closed when external evidence or accountable approvals are absent.

For repository-only verification, use `GRAPHRAG_PRODUCTION_POLICY_ONLY=1 make graphrag-production-gate`. That command proves internal controls but explicitly does not approve a release.

## Required evidence

The signed report binds the checked-out 40-character release commit, exact GraphRAG 3.1.2 contract, `graph-artifact/v1`, immutable adapter image digest, current certification results, topology matrices, observation window, kill-switch exercise, canonical-safety proofs, upgrade/rollback report digest, and five distinct accountable approvers.

Certification must cover capacity, chaos, security, privacy, recovery, and accessibility. Matrices must include standalone plus two tenants each for self-managed and hosted. Security and privacy results require no unresolved blocker; a locally passing script is not a substitute for independent review.

The completed observation window is at least seven days and 1,000 requests. It requires 100% grounded graph claims, relational gain of at least 10 percentage points, global gain of at least 15, direct precision regression no more than one point, Basic p95 regression under 2%, Local p95 overhead no more than 75 ms, Global selection p95 no more than 250 ms, and cost within the approved budget.

Approvals are individually evidence-bound and follow the observation window: graph-index owner, security, privacy, operations, and product. Approval reports expire within 30 days, so a stale decision cannot authorize a later deployment.

## Running the release gate

Provide the detached signature and trusted public key, then run:

```sh
GRAPHRAG_PRODUCTION_REPORT=/secure/evidence/graphrag-production.json \
GRAPHRAG_PRODUCTION_REPORT_SIGNATURE=/secure/evidence/graphrag-production.sig \
GRAPHRAG_PRODUCTION_PUBLIC_KEY=/secure/trust/graphrag-production.pub \
make graphrag-production-gate
```

The gate verifies the signature with cosign, rejects missing/unknown JSON fields, binds the Git commit and immutable image digest, enforces freshness and numeric thresholds, requires distinct approvers, and reruns deterministic evaluation, failure/recovery harnesses, and the dependency policy gate.

## Rollout and rollback

The report authorizes only its `approved_route`: `basic`, `explicit_local`, `auto_local`, `explicit_global`, or `auto_global`. Promotion follows that order and cannot skip a stage. Any threshold breach, certification supersession, incident, material configuration/model/prompt/schema change, or expired approval returns affected workspaces to Basic and requires new evidence.

Rollback uses the prior signed image and compatible active revision from the separately signed upgrade report. If normalized state is suspect, disable Graph and rebuild from canonical data. Disabling or removing GraphRAG must leave canonical data and Basic retrieval intact as specified in the removal runbook.
