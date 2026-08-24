---
name: production-billing-reconciliation-evidence
description: Verify or extend Agent Memory P11.2-A production processor, invoice, settlement, and usage-ledger reconciliation evidence. Use when changing production release binding, variance derivation, sample coverage, schemas, normalizer/CLI, runbook, or Finance/Engineering approval handoff.
---

# Production billing reconciliation evidence

## Boundary

P11.2-A requires a real closed production period. Repository code only
normalizes content-free evidence. Never query processors or databases, accept
credentials, retain billing rows, or promote sandbox webhook tests to proof.

Bind the exact payment-enabled production inventory/plan/ready-change chain and
passed production release to processor invoice/settlement exports, invoice and
usage ledgers, independent usage recomputation, webhook report, and approved
variance decision. Require eight canonical outcomes, positive fully matched
tenant/invoice/settlement/usage samples, USD integer micro-values, and positive
approved ceilings. Independently derive all three absolute variances.

Preserve honest variance and coverage failures as valid-unready. Reject known
breaches not failed, unsafe IDs, unknown/duplicate checks, invalid/overflowed
counts, pre-release/overlong/stale/future periods, upstream mismatch, unknown
fields, and symlinks. Exclude all operational billing IDs, payment instruments,
pricing/tax terms, people, endpoints, payloads, credentials, logs, traces, SQL,
and content. Publish create-only mode-`0600` receipts; CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/platformrollback ./internal/saas/billingreconciliation ./cmd/agent-memory-billing-reconciliation ./internal/contracts -count=1
make saas-release-script-test
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P11.4 items. P11.2-A remains external
until the real production period and Finance/Engineering signatures exist.
