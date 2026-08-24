---
name: production-public-beta-gate-evidence
description: Verify or extend Agent Memory CP11-B shared-window public-beta gate evidence. Use when changing ready billing/SLO/operations/integrity receipt binding, abuse reconciliation, observed-cost ceilings, schemas, CLI, runbook, or domain-owner handoff.
---

# Production public-beta gate evidence

## Boundary

CP11-B requires real production evidence over one exact elapsed window.
Repository code only normalizes content-free artifacts. Never query production
databases, payment processors, monitoring, audit, security, support, or cost
systems; accept credentials; retain tenant/account/finding/attempt/invoice/
event/operator/reviewer IDs, URLs, queries, contacts, signatures, logs, traces,
payloads, or raw exports; or promote fixtures to production proof.

Strictly reload and hash the ready P11.2-A billing, P11.3-A SLO, P11.3-B
operations, and P11.3-C integrity receipts. Require the same production
inventory, plan, ready change, passed release, and exact window. In particular,
the billing period must equal the SLO window; operations and integrity must
preserve their prerequisite hash links.

Require positive signup-attempt and active-tenant coverage. Reconcile abuse
findings exactly across closed, open nonblocking, open launch-blocking, and
unclassified states. Ready requires zero launch-blocking and zero unclassified
findings. Derive actual per-active-tenant micro-USD with ceiling division, then
require both actual total and derived per-tenant cost within approved ceilings.

Preserve honest blockers or cost overruns as valid-unready only when their
matching checks fail. Reject green contradictions, empty coverage, count or
money overflow, stale/future timelines, unknown fields, symlinks, and any
platform/release/window/receipt substitution. Publish create-only mode-`0600`
receipts. CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/billingreconciliation ./internal/saas/betasloevidence ./internal/saas/betaoperationsevidence ./internal/saas/betaintegrityevidence ./internal/saas/publicbetagateevidence ./cmd/agent-memory-public-beta-gate ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./internal/saas/evidenceindex ./cmd/agent-memory-external-evidence ./cmd/agent-memory-release-approval -count=1
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P11.9 items. CP11-B remains external
until real same-window prerequisite receipts, abuse and cost exports, approved
targets, and signed accountable domain-owner review exist.
