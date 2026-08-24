---
name: production-beta-operations-evidence
description: Verify or extend Agent Memory P11.3-B same-window production deletion, rights-notice, anomaly-alert, and support-case evidence. Use when changing domain aggregates, sample and target derivation, ready P11.3-A binding, schemas, CLI, runbook, or Privacy/Security/Support handoff.
---

# Production beta operations evidence

## Boundary

P11.3-B requires real production operations during the exact elapsed P11.3-A
window. Repository code only normalizes content-free evidence. Never query live
databases, logs, audit stores, ticket systems, or customer systems; accept
credentials; retain case/receipt/tenant/person IDs, payloads, messages, or
source content; or promote fixtures to production proof.

Strictly reload and hash the ready P11.3-A receipt, then bind its production
inventory/plan/ready-change/passed-release chain and exact window. Require the
four fixed deletion, rights-notice, anomaly-alert, and support-case domains and
nine canonical checks. Each domain binds an immutable private export and
reports bounded due, within-target, late, overdue-open, required-sample,
sampled, and matched counts plus approved and observed integer durations.

Derive reconciliation, sample coverage, and target status independently.
Preserve honest late, overdue, or sample-shortfall evidence as valid-unready.
Reject contradictory green claims, incomplete/duplicate domains or checks,
unsafe identifiers, mismatched totals, invalid empty-domain samples,
cross-window evidence, stale review, unknown fields, file replacement, and
symlinks. Publish create-only mode-`0600` receipts; CLI exits 0/3/2/1. Treat
support as a bound external export until a first-class support-case product is
separately specified and implemented.

## Verification

```sh
go test -race ./internal/saas/platformrollback ./internal/saas/betasloevidence ./internal/saas/betaoperationsevidence ./cmd/agent-memory-beta-operations ./internal/contracts -count=1
make saas-release-script-test
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P11.6 items. P11.3-B remains external
until real same-window exports and samples, approved policies, and accountable
Privacy/Security/Support signatures exist.
