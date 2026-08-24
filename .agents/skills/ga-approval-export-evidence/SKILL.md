---
name: ga-approval-export-evidence
description: Verify or extend Agent Memory P12.2-C signed GA approval-export evidence. Use when changing scorecard/drill bundle binding, five GA controls, Ed25519 trust/export verification, schemas, CLI, runbook, or named-owner handoff.
---

# Signed GA approval export evidence

## Boundary

P12.2-C requires an authoritative immutable export and independently controlled
trust bundle. Repository code only verifies content-free digests and aggregate
outcomes. Never copy private keys, signatures, owners, evidence references,
filenames, customer content, or raw approval artifacts into receipts or logs.

Reload and hash ready P12.2-A scorecard and P12.2-B repeated-drill receipts.
Require their platform, release, scorecard, and receipt-digest chain to match.
Derive the GA bundle digest as SHA-256 of the scorecard digest, newline, drill
digest, newline. Every approval must review this exact digest.

Require exactly product, security, privacy, legal, and operations under gate
`ga`. Verify current Ed25519 signatures against an independently managed trust
bundle scoped to gate and control. Reconcile every regular non-symlink JSON file
against the exact manifest and deterministic export digest. Reject extra,
missing, changed, unsafe, unauthorized, invalidly signed, or substituted files.
Hash and strictly decode each approval from the same exact opened bytes under
one stable directory/member snapshot; never derive the digest and then reload a
later export generation for signature verification.

Preserve genuinely missing/rejected/expired decisions as valid-unready only
when checks and readiness agree. Publish create-only mode-`0600` receipts. CLI
exits 0/3/2/1 and reports aggregates only.

## Verification

```sh
go test -race ./internal/saas/gascorecardevidence ./internal/saas/gadrillevidence ./internal/saas/approvalexportevidence ./cmd/agent-memory-ga-approval-export ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./internal/saas/evidenceindex ./cmd/agent-memory-external-evidence ./cmd/agent-memory-release-approval -count=1
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P12.5 items. P12.2-C remains external
until the authoritative export, independent trust bundle, five current signed
decisions, and named-owner GA review exist.
