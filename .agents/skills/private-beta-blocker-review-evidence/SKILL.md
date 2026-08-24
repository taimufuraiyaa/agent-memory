---
name: private-beta-blocker-review-evidence
description: Verify or extend Agent Memory CP10-B current incident/finding and launch-blocker review evidence. Use when changing blocker counts, classification outcomes, schemas, normalizer/CLI, runbook, or Incident Commander/Product approval handoff.
---

# Private-beta blocker-review evidence

## Boundary

CP10-B requires current installed exports and accountable review. Repository
code only normalizes content-free evidence. Never query incident, finding,
scanner, ticket, or deployment systems; accept credentials; or treat tests and
empty fixtures as a clean operational register.

Bind the exact ready staging inventory/plan/change chain and passed release to
current finding and incident exports, the classification policy, and private
review decision by SHA-256. Require five canonical export, classification,
Incident Commander, and Product outcomes. Derive total open items and require
complete review coverage plus zero severity-one and unresolved blocker counts.

Preserve honest blockers or incomplete review as valid-unready. Reject unsafe
IDs, unknown/duplicate checks, contradictory readiness/outcomes, invalid counts,
pre-release/stale/future timelines, upstream mismatch, unknown fields, and
symlinks. Exclude operational item IDs, summaries, rules, vulnerabilities,
customers, people, tickets, comments, remediation text, logs, traces, and
content. Publish atomic create-only mode-`0600` receipts; CLI exits 0/3/2/1.

## Verification

```sh
go test -race ./internal/saas/blockerevidence ./cmd/agent-memory-blocker-evidence ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P10.6 items. CP10-B remains external until
the exact installed dossier and Incident Commander/Product signatures exist.
