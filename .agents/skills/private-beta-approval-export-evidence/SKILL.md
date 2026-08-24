---
name: private-beta-approval-export-evidence
description: Verify or extend the Agent Memory CP10-A signed private-beta accountable approval-export boundary. Use when changing P10.2-B/P10.3-B/CP10-B/CP10-C binding, shared evidence-bundle derivation, five-domain approval export verification, schemas, CLI, or handoff.
---

# Private-beta approval export evidence

Preserve CP10-A as an external accountable decision. Repository fixtures prove
the normalizer, never the real approval.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md` R66
- `.kiro/specs/saas-product-platform/design.md`, “Signed private-beta approval export evidence”
- `.kiro/specs/saas-product-platform/tasks.md` P10.8
- `docs/saas/private-beta-approval-export.md`
- `internal/saas/approvalexportevidence/private_beta.go`
- `internal/saas/readiness/approval.go`

## Invariants

1. Strictly decode and hash exact ready P10.2-B security, P10.3-B alert,
   CP10-B blocker, and CP10-C capacity receipts.
2. Require one staging inventory, plan, ready change, and passed release across
   all four prerequisites.
3. Derive the canonical bundle digest from the four exact receipt digests and
   one private supporting-evidence-manifest digest. Never trust a supplied
   bundle value without recomputing it.
4. Reconcile every regular approval-directory entry against the exact manifest.
   Reject extra/missing files, symlinks, unsafe names, digest changes, and file
   identity races. Hash and strictly decode each approval from the same exact
   bytes under one stable directory/member snapshot; never reload approvals
   after deriving the export digest.
5. Verify exactly legal, operations, privacy, product, and security decisions
   for gate `private_beta` with the independently controlled trust bundle.
6. Require every verified approval to bind the same derived bundle. Reject
   substitution; preserve missing, rejected, or expired decisions as valid-
   unready.
7. Derive the first eight of nine checks. Only the final accountable-domain
   review outcome is supplied. Readiness requires all five current approvals
   and all nine checks passed.
8. Keep receipts and CLI output aggregate-only. Never emit owners, keys,
   signatures, filenames, evidence references, paths, or private contents.
9. Publish create-only mode `0600` with exits `0/3/2/1`. Never alter the exact
   57-control catalog or close CP10-A from repository evidence.

## Verification

```sh
go test -race ./internal/saas/approvalexportevidence ./internal/saas/readiness ./internal/saas/securityclosureevidence ./internal/saas/alertevidence ./internal/saas/blockerevidence ./internal/saas/capacityevidence ./cmd/agent-memory-private-beta-approval ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
find api docs .kiro -name '*.json' -type f -print0 | xargs -0 -n1 jq empty
git diff --check
```

Then reconcile exactly 57 unique catalog IDs, 57 matrix rows, and 57 unchecked
external controls using the signed-evidence-index workflow.
