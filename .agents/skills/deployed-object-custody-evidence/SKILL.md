---
name: deployed-object-custody-evidence
description: Verify or extend the Agent Memory CP4-A deployed staging object-custody review boundary. Use when changing effective object-policy review, service capability evidence, vault/quarantine/audit custody checks, content-free log or trace sampling, schemas, collector, CLI, runbook, or matrix support.
---

# Deployed Object-Custody Evidence

## Purpose

Preserve the boundary between repository tests and a real Security review of
one deployed self-managed staging release. The repository normalizes bounded,
content-free evidence; it does not access effective policies or telemetry and
does not self-certify CP4-A.

## Evidence chain

1. Validate the exact staging platform inventory, infrastructure plan, and
   ready applied-change receipt.
2. Validate the exact passed staging Kubernetes release receipt.
3. In private operator tooling, export effective object policies and workload
   identity configuration; run positive and negative capability probes for API,
   worker, and reconciler; test vault, quarantine, and audit custody; and sample
   logs, traces, and telemetry access.
4. Retain every raw artifact in the immutable private dossier and put only its
   SHA-256 plus passed/failed in the fixed review input.
5. Normalize it:

   ```sh
   make saas-object-custody-check \
     PLATFORM_INVENTORY=/private/staging-inventory.json \
     PLATFORM_PLAN=/private/staging-plan.json \
     PLATFORM_CHANGE=/private/staging-change.json \
     STAGING_RELEASE=/immutable/staging-release.json \
     OBJECT_CUSTODY_REVIEW=/private/object-custody-review.json \
     OBJECT_CUSTODY_RECEIPT=/immutable/object-custody-receipt.json
   ```

6. Bind the unchanged dossier to current signed CP4-A Security approval through
   the external-evidence index.

## Invariants

- Environment is exactly staging; inventory, change, release, and review
  identities and opened-byte digests must match.
- The applied change assesses ready and the Kubernetes release is passed.
- Review begins after both deployments, lasts at most eight hours, input is
  generated after completion, and collection occurs within 24 hours.
- Exactly ten fixed checks cover deployed policy export; API, worker, and
  reconciler capabilities; vault immutability; quarantine promotion/removal;
  audit archive immutability; content-free logs; content-free traces; and
  restricted telemetry access.
- Each check contains only passed/failed and a private evidence SHA-256. Honest
  failures are valid-unready; contradictory readiness is malformed.
- Never add policy bodies, manifests, credentials, endpoints, bucket/object or
  resource names, identities, paths, commands, logs, traces, or content.
- Input is strict bounded regular non-symlink JSON. Output is atomic,
  create-only, mode `0600`, and aggregate-only on stdout.
- Exit codes remain `0` ready, `3` valid-unready, `2` usage, and `1` unsafe,
  malformed, or operational failure.
- Local MinIO/Floci tests, mocks, and examples never close CP4-A.

## Change workflow

1. Read `.kiro/specs/saas-product-platform/requirements.md`, `design.md`, then
   `tasks.md`; update all three before a behavioral contract change.
2. Write a failing test first.
3. Keep Go logic under `internal/saas/objectcustody`, CLI under
   `cmd/agent-memory-object-custody`, schemas under `api/evidence/v1`, and the
   operator contract under `docs/saas/staging-object-custody-review.md`.
4. Preserve the CP4-A external matrix row as open and state the exact deployed
   evidence and approval still missing.

## Verification

```sh
go test -race ./internal/saas/objectcustody ./cmd/agent-memory-object-custody ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
actionlint
git diff --check
```

Also parse every JSON file under `api/` and `docs/saas/`, then reconcile the
canonical external-control catalog to the matrix. A locally ready example
proves the verifier only, not the underlying review.
