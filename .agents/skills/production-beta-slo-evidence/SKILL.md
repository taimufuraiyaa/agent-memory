---
name: production-beta-slo-evidence
description: Verify or extend Agent Memory P11.3-A production beta SLO observation evidence. Use when changing fixed SLO targets, elapsed-window validation, metric coverage, schemas, normalizer/CLI, runbook, or Product/Operations approval handoff.
---

# Production beta SLO evidence

## Boundary

P11.3-A requires a real elapsed production interval. Repository code only
normalizes content-free evidence. Never query Prometheus, dashboards, databases,
logs, traces, or customer systems; accept credentials; retain query expressions,
labels, series, tenant/request IDs, people, payloads, or content; or promote
fixtures and load tests to elapsed-window proof.

Bind the exact production inventory/plan/ready-change and passed production
release to immutable metric samples, reviewed query manifest, approved window
and SLO-definition decisions, and Product/Operations review. Require the six
fixed availability/latency/indexing metric IDs, complete positive sample counts,
six canonical checks, an approved one-to-31-day elapsed window, and fresh
evaluation. Apply fixed integer ppm/microsecond thresholds in the normalizer;
never accept producer-selected thresholds or comparators.

Preserve honest coverage and target failures as valid-unready. Reject green
claims that contradict observations, unsafe IDs, incomplete/duplicate metrics
or checks, out-of-range values, pre-release/short/overlong/stale/future windows,
unknown fields, file replacement, and symlinks. Publish create-only mode-`0600`
receipts; CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/platformrollback ./internal/saas/betasloevidence ./cmd/agent-memory-beta-slo ./internal/contracts -count=1
make saas-release-script-test
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P11.5 items. P11.3-A remains external
until the real elapsed window and Product/Operations signatures exist.
