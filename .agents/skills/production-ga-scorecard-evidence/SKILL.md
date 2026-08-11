---
name: production-ga-scorecard-evidence
description: Verify or extend Agent Memory P12.2-A retention-aware production GA scorecard evidence. Use when changing GA metrics, targets, elapsed-window validation, retention coverage, schemas, CLI, runbook, or Product/domain-owner handoff.
---

# Production GA scorecard evidence

## Boundary

P12.2-A requires a real elapsed production window and private immutable source
artifacts. Repository code only normalizes content-free aggregates and digests.
Never grant the collector query/data-plane authority, accept credentials, store
tenant/account/source/request/log/query/person identifiers or raw exports, or
promote examples and tests to production evidence.

Bind the scorecard to the exact installed production inventory, ready plan and
change, and passed production release. The window must be approved before it
starts, begin after the release, last 28–93 days, and be evaluated within 24
hours after it ends.

Require exactly thirteen canonical metrics: API availability; search and write
p95 latency; critical/high exploitable findings; tenant isolation; deletion;
audit integrity; billing reconciliation; restore RPO/RTO; cost per active
tenant; support response; and retention deletion compliance. Every metric has
positive expected and observed samples. Cost uses a positive approved dynamic
target; other targets are specification-owned. Coverage, breaches, retention,
and readiness are independently derived.

Require all seven canonical checks. Coverage, target, and retention check
outcomes must agree with derivation. Preserve matching failures as valid-unready
and reject contradictory green claims, missing/duplicate evidence, unsafe
files, symlinks, stale/future timelines, and platform/release substitution.
Publish create-only mode-`0600` receipts. The CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/gascorecardevidence ./cmd/agent-memory-ga-scorecard ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./internal/saas/evidenceindex ./cmd/agent-memory-external-evidence ./cmd/agent-memory-release-approval -count=1
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P12.3 items. P12.2-A remains external
until the real elapsed window, immutable private scorecard/query artifacts,
approved window and target decisions, and signed accountable reviews exist.
