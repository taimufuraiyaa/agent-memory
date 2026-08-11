---
name: staging-migration-acceptance-evidence
description: Verify or extend the Agent Memory CP9-B migration parity and rollback acceptance evidence boundary. Use when changing prerequisite receipt reloaders, common staging bindings, rollback tabletop outcomes, schemas, CLI, runbook, or external-evidence matrix support.
---

# Staging migration acceptance evidence

Preserve CP9-B as a content-free read-only acceptance normalizer. Repository
fixtures prove the contract; they never close the real external control.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md` R59
- `.kiro/specs/saas-product-platform/design.md`, “Migration parity and rollback acceptance evidence”
- `.kiro/specs/saas-product-platform/tasks.md` P9.6
- `docs/saas/staging-migration-acceptance.md`
- `internal/saas/migrationacceptanceevidence/evidence.go`

## Trust boundary

1. Strictly reload ready CP9-A and CP5-A receipts from exact opened bytes.
2. Require identical staging inventory, reviewed plan, applied change, passed
   release, and representative dataset version across both receipts.
3. Bind the input to both exact receipt SHA-256 digests and their opaque IDs.
4. Begin the tabletop after both prerequisite collection times, cap it at four
   hours, and collect generated evidence within 24 hours.
5. Evaluate exactly eight outcomes in canonical order:
   - original local copy preserved
   - hosted profile disabled
   - credential revocation rehearsed
   - import report reconciled
   - hosted deletion path reviewed
   - explicit local continuity confirmed
   - remigration requires a fresh bundle
   - Product/Engineering/Operations review complete
6. Preserve complete failed or inconclusive table-tops as valid-unready.
   Reject missing, duplicate, stale, substituted, mismatched, symlinked,
   unknown-field, or contradictory evidence.
7. Publish only create-only mode-`0600` receipts. CLI reports are aggregate-only
   and use exit codes `0` ready, `3` valid-unready, `2` usage, and `1` invalid.

Never place participant identities, commands, credentials, endpoints,
account/tenant/workspace/source/item IDs, deletion receipts, report text, logs,
traces, or payloads in normalized input, receipt, or CLI output. Keep the
private artifacts in immutable external custody and reference only SHA-256.

## Safe change workflow

1. Update requirements, design, and tasks before non-trivial behavior changes.
2. Add failing tests for the changed invariant before implementation.
3. Exercise the prerequisite loaders plus CP9-B package and CLI together.
4. Keep schemas closed and content-free; update the contract test when fields
   or fixed enums change.
5. Update the example, Make target, runbook, implementation status, and CP9-B
   matrix support without changing the exact 57-control catalog.
6. Do not mark CP9-B or Checkpoint 9 complete from local fixtures.

## Verification

```sh
go test -race ./internal/saas/parityevidence ./internal/saas/migrationcohortevidence ./internal/saas/migrationacceptanceevidence ./cmd/agent-memory-migration-acceptance ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
find api docs .kiro -name '*.json' -type f -print0 | xargs -0 -n1 jq empty
git diff --check
```

Then run the signed external-evidence narrow gate from
`.agents/skills/verify-external-evidence-index/SKILL.md` and confirm the catalog,
matrix, and open checklist counts still reconcile to exactly 57.
