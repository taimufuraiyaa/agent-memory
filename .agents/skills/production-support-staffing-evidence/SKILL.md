---
name: production-support-staffing-evidence
description: Verify or extend Agent Memory P11.1-A production feedback and incident-channel staffing evidence. Use when changing channel inventory, coverage roster, response targets, escalation drills, schemas, normalizer/CLI, runbook, or Support/Operations approval handoff.
---

# Production support staffing evidence

## Boundary

P11.1-A requires real published channels and active staffing. Repository code
only normalizes content-free evidence. Never query helpdesk, pager, messaging,
identity, scheduling, or incident providers; accept credentials; retain people,
destinations, rotations, case IDs, messages, payloads, logs, or content; or
promote local fixtures to proof.

Bind the exact production inventory/plan/ready-change and passed production
release to the channel inventory, coverage roster, response policy, approved
targets, and private escalation report. Require independent primary and backup
coverage, two fixed feedback/incident drills, and six canonical checks. Derive
delivery and acknowledgement duration and compare them with approved targets.

Preserve honest coverage and target failures as valid-unready. Reject green
claims that contradict observations, unsafe IDs, incomplete/duplicate checks or
drills, noncausal/stale/future timelines, upstream mismatch, unknown fields, and
symlinks. Publish create-only mode-`0600` receipts; CLI exits 0/3/2/1.
Hash and decode the same opened bounded regular file, with identity and size
checks before and after reading; never return to a path-only read after Lstat.

## Verification

```sh
go test -race ./internal/saas/platformrollback ./internal/saas/supportevidence ./cmd/agent-memory-support-evidence ./internal/contracts -count=1
make saas-release-script-test
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P11.1.1 items. P11.1-A remains external
until real channels, active coverage, drills, policy, and Support/Operations
signatures exist.
