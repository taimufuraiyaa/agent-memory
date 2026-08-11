---
name: self-managed-backup-expiry-evidence
description: Validate or extend the Agent Memory P7.4-A production aged-backup expiry boundary. Use when changing backup retention duration, retention receipt loading, aged-backup drill timelines/checks, schemas, collector/CLI, runbook, or external matrix support.
---
# Self-managed backup expiry evidence

## Truth boundary

This workflow proves the collector implementation, not elapsed production
retention. Never close P7.4-A from a fixture, altered clock, local/Floci run,
disposable database, or ordinary restore test. Closure requires a real
self-managed production backup aged through the complete installed interval,
immutable private storage/catalog/restore/key evidence, and signed Privacy plus
Operations approval.

The collector is passive. It must never accept backup credentials or list,
restore, delete, or otherwise mutate backups.

## Read first

1. `.kiro/specs/saas-product-platform/requirements.md`, R34.
2. The aged-backup evidence section of `design.md`.
3. `tasks.md`, P7.4 and P7.7.
4. `internal/saas/retentioninventory` and
   `internal/saas/backupexpiry`.
5. Both backup-expiry schemas, the example, runbook, and external matrix.
6. The PostgreSQL restore and retention-inventory evidence skills.

Run `agent-memory search` first and immediately score its result.

## Invariants

- Load the exact production inventory → plan → ready applied-change chain.
- Strictly load the installed retention receipt, recompute the exact opened-byte
  digest and canonical policy-array digest, and revalidate all twelve policies.
- Require the retention receipt to bind the same inventory and change.
- Use only the installed `backups` policy version and duration. Drill values
  must match and cannot shorten the interval.
- The selected backup predates deletion and privately proves it contained the
  later-deleted test record.
- Derive the deadline as deletion completion plus installed duration. Never
  accept a caller-entered deadline.
- Verification starts no earlier than that deadline, lasts at most six hours,
  and is generated/collected within 24 hours.
- Require exactly seven checks: manifest verified, deleted record present,
  deletion receipt verified, expiry schedule verified, backup absent after the
  deadline, restore unavailable, and cryptographic material expired.
- Preserve a failed post-deadline check as valid-unready. Reject contradictory
  readiness, broken causality, future/stale evidence, or broken bindings.
- Receipt and CLI output remain content-free. Never emit tenant/account/
  workspace/record identity, object or backup paths, manifests, encryption
  keys, credentials, endpoints, provider/database names, SQL, rows, customer
  content, logs, or raw output.
- Publish atomically, create-only, non-symlink, mode `0600`.

## TDD order

1. Add failing retention-receipt loader tests for exact bytes, canonical policy
   digest, unknown fields, tampering, and order.
2. Add failing pure collector tests for deadline derivation, ready/unready
   checks, early verification, stale/future time, policy mismatch, broken
   binding, duplicate/missing checks, and content-free output.
3. Add CLI tests for aggregate reports and exit `0/3/2/1`.
4. Add strict input/receipt schemas and contract tests closing seven check IDs.
5. Exercise `Collect` through real inventory/plan/change files and a published,
   reloaded retention receipt.
6. Update example, runbook, Make target, matrix, status, and specs only after
   focused tests pass.

## Verification

```sh
go test -count=1 \
  ./internal/saas/retentioninventory \
  ./internal/saas/backupexpiry \
  ./cmd/agent-memory-backup-expiry \
  ./internal/contracts
make -n saas-backup-expiry-check \
  PLATFORM_INVENTORY=/private/inventory.json \
  PLATFORM_PLAN=/private/plan.json \
  PLATFORM_CHANGE=/private/change.json \
  RETENTION_INVENTORY_RECEIPT=/private/retention.json \
  BACKUP_EXPIRY_DRILL=/private/drill.json \
  BACKUP_EXPIRY_RECEIPT=/immutable/receipt.json
go test ./...
go vet ./...
git diff --check
```

Also parse every API JSON document, lint workflows, and run the exact 57-control
catalog/matrix reconciliation. Check only P7.7 repository acceptance. Leave the
original P7.4-A checkbox and external catalog entry open until real elapsed
production evidence and authorized signatures exist.
