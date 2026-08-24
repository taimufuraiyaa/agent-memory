---
name: public-beta-approval-export-evidence
description: Verify or extend the Agent Memory CP11-C signed public-beta approval-export evidence boundary. Use when changing approval export completeness, trust/signature verification, CP11-A/CP11-B evidence binding, schemas, CLI, runbook, or Product/Release Authority approval handoff.
---

# Public Beta Approval Export Evidence

Use this workflow for CP11-C changes.

## Invariants

- Keep CP11-C in the 57-control external catalog. Repository fixtures never
  close it.
- Reload ready CP11-A and CP11-B receipts and require their inventory, plan,
  change, and production release bindings to match.
- Treat the trust bundle as independently managed. Never accept a private key,
  application credential, database connection, or network endpoint.
- Reconcile every actual directory entry with the declared manifest. Reject
  symlinks, non-regular entries, unsafe names, extra/missing files, duplicate
  names, changed digests, unknown JSON fields, and unsafe file identity changes.
  Hash and decode the same exact opened bytes under one stable directory/member
  snapshot; do not separately reopen the directory to obtain decisions.
- Verify exactly the six `public_beta` controls with the shared readiness
  verifier. Preserve its signer scope, canonical payload, latest-decision,
  future-clock, rejection, and expiry semantics.
- Require `beta_readiness` to bind the CP11-B receipt digest. Require the other
  five current approvals to bind the CP11-A receipt digest.
- Keep receipts and CLI reports aggregate-only. Never emit owners, key IDs,
  evidence references, keys, signatures, filenames, URLs, or content.
- Publish atomically, create-only, non-symlink, and mode `0600`.
- A directory digest proves what was inspected, not authoritative export
  completeness. Preserve external custody, export authorization/audit, and
  Product/Release Authority approval as closure requirements.

## Files

- Core: `internal/saas/approvalexportevidence/evidence.go`
- Existing verifier: `internal/saas/readiness/approval.go`
- CLI: `cmd/agent-memory-approval-export`
- Schemas: `api/evidence/v1/public-beta-approval-export-*.schema.json`
- Runbook: `docs/saas/public-beta-approval-export.md`
- Matrix: `docs/saas/external-evidence-matrix.md`

## Verification

Run focused race tests first:

```sh
go test -race ./internal/saas/approvalexportevidence \
  ./internal/saas/launchassetevidence \
  ./internal/saas/publicbetagateevidence \
  ./internal/saas/readiness \
  ./cmd/agent-memory-approval-export \
  ./internal/contracts -count=1
```

Then run repository gates:

```sh
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
git diff --check
```

Also parse all JSON with `jq`, confirm the external catalog/matrix still
contains exactly 57 controls, and run `actionlint` when installed.
