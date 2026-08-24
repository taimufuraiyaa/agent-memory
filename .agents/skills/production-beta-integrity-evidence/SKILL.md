---
name: production-beta-integrity-evidence
description: Verify or extend Agent Memory P11.3-C same-window production isolation and audit-integrity evidence. Use when changing ready P11.3-A/P11.3-B binding, audit-chain or archive reconciliation, signal classification, anomaly closure, schemas, CLI, runbook, or Security residual-risk handoff.
---

# Production beta integrity evidence

## Boundary

P11.3-C requires real production evidence for the exact P11.3-A/P11.3-B beta
window. Repository code only normalizes content-free exports. Never query live
databases, object stores, logs, traces, dashboards, ticket/security systems, or
external evidence stores; accept credentials; retain tenant/event/finding/
incident/request/trace/source/credential/archive-object/operator/reviewer IDs,
queries, endpoints, messages, signatures, payloads, or raw output; or promote
fixtures to production proof.

Strictly reload and hash both ready prerequisite receipts. Require exact
production platform/release and window equality. Bind seven immutable private
artifacts: database chain report, archive reconciliation, two signal exports,
anomaly report, residual-risk decision, and Security review.

Require a positive audit-event population. Every event must be chain-checked
and expected in archive reconciliation. Verified, missing, and checksum-
mismatched archives reconcile exactly. Explained, unexplained, and unclassified
counts reconcile for both fixed signal classes. Closed and open findings
reconcile to the anomaly population. Derive chain/archive completeness,
classification coverage, unexplained counts, and finding closure independently.

Preserve honest breaks, archive gaps, classification shortfalls, unexplained
signals, and open findings as valid-unready when matching checks fail. Reject
contradictory green claims, empty exports, incomplete or duplicate checks,
overflow, unsafe IDs, stale/future clocks, cross-window or cross-environment
substitution, unknown fields, file replacement, and symlinks. A chain break or
archive mismatch stays unready even when explained. Publish create-only mode-
`0600` receipts; CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/betasloevidence ./internal/saas/betaoperationsevidence ./internal/saas/betaintegrityevidence ./cmd/agent-memory-beta-integrity ./internal/contracts -count=1
make saas-release-script-test
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
go test ./internal/saas/evidenceindex -count=1
git diff --check
```

Repository support contributes three P11.7 items. P11.3-C remains external
until real production exports, complete chain/archive verification, a closed
anomaly dossier, approved policy, and signed Security residual-risk review
exist.
