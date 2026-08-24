---
name: production-ga-drill-evidence
description: Verify or extend Agent Memory P12.2-B repeated production GA drill evidence. Use when changing restore/deletion/credential/notice repetition, exact GA-window binding, replay prevention, schemas, CLI, runbook, or accountable review.
---

# Repeated production GA drill evidence

## Boundary

P12.2-B requires real repeated production operations. Repository code only
normalizes a content-free manifest and digests. Never accept production query or
control-plane authority, credentials, operator/reviewer/customer identifiers,
logs, traces, payloads, or raw reports; never promote examples to evidence.

Reload and hash the ready P12.2-A receipt. Bind review, production platform and
release IDs/digests to it, and use its exact 28–93 day window. The closed
scenario set is restore, deletion, credential, and notice.

Require at least two drills per scenario, completed on distinct UTC dates.
Drill IDs and evidence digests are globally unique across the manifest. Each
drill starts and completes inside the GA window and has a positive duration no
longer than 24 hours. Reject replay, missing repetition, same-date repetition,
unknown scenarios, substitution, unsafe files, unknown fields, and green
contradictions.

Preserve complete failed or inconclusive drill sets as valid-unready when the
outcome check and readiness agree. Publish create-only mode-`0600` receipts;
the CLI exits 0/3/2/1 and reports aggregates only.

## Verification

```sh
go test -race ./internal/saas/gascorecardevidence ./internal/saas/gadrillevidence ./cmd/agent-memory-ga-drills ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./internal/saas/evidenceindex ./cmd/agent-memory-external-evidence ./cmd/agent-memory-release-approval -count=1
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P12.4 items. P12.2-B remains external
until real repeated production drills, immutable manifests/reports, approved
policy, and signed Operations/Security/Privacy review exist.
