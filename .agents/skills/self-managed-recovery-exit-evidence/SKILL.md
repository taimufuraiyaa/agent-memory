---
name: self-managed-recovery-exit-evidence
description: Verify or extend the Agent Memory P0.2-B component recovery and integration exit evidence boundary. Use when changing inventory subject binding, exercise reconciliation, recovery targets, schemas, CLI, runbook, or P0.2-B repository support.
---

# Self-managed recovery and exit evidence

Preserve P0.2-B as a content-free, read-only normalizer. Repository fixtures
prove the contract; only installation-specific procedures, real-environment
exercises, immutable custody, and current Operations approval can close P0.2-B.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md` R65
- `.kiro/specs/saas-product-platform/design.md`, “Component recovery and integration exit evidence”
- `.kiro/specs/saas-product-platform/tasks.md` P0.7
- `docs/saas/component-recovery-and-exit.md`
- `internal/saas/recoveryexitevidence/evidence.go`
- `internal/saas/platforminventory/inventory.go`

## Preserve the boundary

1. Reload and hash one exact staging or production self-managed inventory.
2. Require its eight core components and payment, email, and model integrations;
   preserve exact integration enabled states and reject omissions or additions.
3. Require replacement, failover, export, and restore for every subject. A
   disabled integration proves disabled-state continuity and exit behavior; it
   does not receive a not-applicable exception.
4. Bind each operation to procedure and exercise digests. Require positive
   attempts and target seconds, exact passed/failed/inconclusive reconciliation,
   and a bounded nonnegative maximum observed duration.
5. Derive failure when an attempt failed or duration breached target; derive
   inconclusive for remaining incomplete attempts; otherwise derive passed.
   Aggregate the four operations into each subject outcome.
6. Require exactly eight checks in canonical order. Independently derive
   inventory, subject completeness, four operation classes, and target checks.
   Only the accountable Operations review is a supplied decision.
7. Readiness requires all 11 subjects, 44 operations, and eight checks passed.
   Preserve honest failed/inconclusive evidence as valid-unready.
8. Reject symlinks, unknown fields, trailing JSON, oversized files, invalid
   chronology, and open/validate races. Publish create-only mode-`0600` receipts
   with aggregate-only CLI output and exits `0/3/2/1`.

Never store commands, endpoints, credentials, topology, provider/customer data,
people, signatures, paths, or raw exercise output in normalized evidence. Never
alter the exact 57-control catalog or mark P0.2-B complete from local fixtures.

## Verification

```sh
go test -race ./internal/saas/platforminventory ./internal/saas/recoveryexitevidence ./cmd/agent-memory-recovery-exit ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
find api docs .kiro -name '*.json' -type f -print0 | xargs -0 -n1 jq empty
git diff --check
```

Then run the narrow signed-index gates from
`.agents/skills/verify-external-evidence-index/SKILL.md` and reconcile exactly
57 catalog IDs, 57 matrix rows, and 57 unchecked external controls.
