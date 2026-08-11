---
name: self-managed-postgres-restore-evidence
description: Validate or extend the Agent Memory CP3-B self-managed PostgreSQL restore evidence boundary. Use when changing restore drill schemas, inventory/plan/change binding, RPO/RTO calculation, reconciliation or tombstone checks, collector publication, the operator runbook, or the saas-postgres-restore-check target.
---

# Self-managed PostgreSQL restore evidence

Use this workflow to preserve the distinction between repository proof and real
self-managed recovery evidence.

## Read first

Read, in order:

1. `.kiro/specs/saas-product-platform/requirements.md` R31.
2. `.kiro/specs/saas-product-platform/design.md`, “Self-managed PostgreSQL restore evidence”.
3. `.kiro/specs/saas-product-platform/tasks.md` P3.7 and Checkpoint 3.
4. `docs/saas/self-managed-postgres-restore-drill.md`.
5. `internal/saas/postgresrestore/restore.go` and its tests.

## Invariants

- Load and validate the exact inventory → plan → applied-change chain. The
  platform-change assessment must be ready.
- Accept only `self_managed_external` drill inputs from staging or production.
- Derive RPO from impairment start minus recovery point. Derive RTO from
  service readiness minus impairment start. Do not trust caller-entered
  duration values.
- Keep targets fixed at RPO ≤ 300 seconds and RTO ≤ 3,600 seconds unless the
  authoritative requirements change first.
- Require exactly the ten named backup, restore, schema, tenant, row, outbox,
  audit, tombstone, deleted-data, and restore-target-disposal checks.
- Preserve failed checks and target breaches as valid-but-unready. Reject
  contradictory readiness as malformed.
- Reject symlinks, non-regular or oversized files, unknown JSON, broken receipt
  binding, impossible/future/stale timestamps, missing evidence hashes, and
  duplicate or unknown checks.
- Keep the receipt content-free. Never admit credentials, endpoints, database
  names, tenant IDs, object or backup paths, SQL, row contents, logs, or raw
  command output. Bind private artifacts only by SHA-256.
- Publish atomically, create-only, and mode `0600`. Keep CLI output aggregate.
- A fixture or disposable database can prove implementation behavior only. Do
  not close CP3-B without a real self-managed backup/PITR drill, immutable
  private dossier, and signed Operations approval.

## Change workflow

1. Update requirements, design, and tasks before implementation.
2. Add failing package or CLI tests for the changed behavior.
3. Implement the smallest contract change.
4. Update both restore JSON schemas, the contract test, Make target, runbook,
   example, and external-evidence matrix when their boundary changes.
5. Run focused tests and `git diff --check`.
6. When Docker is available, use a uniquely named, labelled, disposable
   `pgvector/pgvector:pg17` container with tmpfs storage and a loopback random
   port. Refuse to reuse an existing container.
7. Apply all migrations through an existing PostgreSQL integration test,
   exercise `TestRestoreReplaysLaterTombstonesBeforeServing`, create a custom
   dump, restore it into a second database, and compare migration, public-table,
   and tombstone counts.
8. Remove only the exact disposable container. Never touch the persistent
   `agent_memory` database or unrelated containers.
9. Run full Go tests, vet, actionlint, schema parsing, external-evidence gates,
   and diff checks before checking P3.7 acceptance boxes.
10. Recount task completion while leaving CP3-B and all external controls open.

## Focused commands

```sh
go test ./internal/saas/postgresrestore ./cmd/agent-memory-postgres-restore ./internal/contracts -count=1
jq empty api/evidence/v1/self-managed-postgres-restore-drill.schema.json api/evidence/v1/self-managed-postgres-restore-receipt.schema.json
git diff --check
```

Run the collector through:

```sh
make saas-postgres-restore-check \
  PLATFORM_INVENTORY=/private/inventory.json \
  PLATFORM_PLAN=/private/plan.json \
  PLATFORM_CHANGE=/private/change.json \
  POSTGRES_RESTORE_DRILL=/private/drill.json \
  POSTGRES_RESTORE_RECEIPT=/immutable/receipt.json
```

Exit `0` is ready, `3` is valid-unready, `2` is usage, and `1` is unsafe,
malformed, or operational failure.
