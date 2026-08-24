---
name: staging-capacity-economics-evidence
description: Verify or extend Agent Memory CP10-C installed private-beta capacity and worst-case economics evidence. Use when changing beta-cap headroom, cost derivation, CP5-C binding, schemas, CLI/runbook, or Operations/Finance/Product approval handoff.
---

# Staging capacity and economics evidence

## Boundary

Normalize only content-free CP10-C evidence. Never query infrastructure,
telemetry, launch/billing databases, or providers; never retain topology,
customer data, pricing details, or samples; never treat the local $1,000
planning preference as an operating ceiling or approval.

Bind exact staging inventory/plan/ready-change/passed-release, a strict ready
CP5-C receipt, and installed-policy, entitlement, capacity, economics, and
decision digests. Final proof is the signed external `cp10_c` dossier.

## Contract

Require eight approval/load/measurement/tenant-headroom/request-headroom/quota/
economics/cost outcomes. Independently compare supported versus planned tenant
concurrency and retrieval requests per minute. Derive total monthly worst-case
micro-US dollars as fixed cost plus account cap times variable per-tenant cost,
with overflow rejection, then compare with the approved positive ceiling.

Preserve honest shortfalls as valid-unready. Reject outcome/readiness
contradictions, unready or misbound CP5-C receipts, missing/duplicate checks,
unsafe IDs, unknown fields, stale/overlong timelines, bad digests, symlinks, and
derived-cost mismatch. Publish atomically, create-only, mode `0600`; CLI exits
`0` ready, `3` valid-unready, `2` usage, and `1` invalid/operational.

## Verification

```sh
go test -race ./internal/saas/retrievalload ./internal/saas/capacityevidence ./cmd/agent-memory-capacity-evidence ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P10.4 items; real installed evidence and
Operations/Finance/Product signatures remain external.
