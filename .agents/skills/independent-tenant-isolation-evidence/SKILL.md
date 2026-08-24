---
name: independent-tenant-isolation-evidence
description: Verify or extend the Agent Memory CP2-A independent staging tenant-isolation review boundary. Use when changing API/RLS/identifier/cache/error/timing review evidence, domain outcomes, schemas, normalizer, CLI, runbook, or external matrix support.
---

# Independent Tenant-Isolation Evidence

## Purpose

Preserve the boundary between repository adversarial tests and an independent
security assessment of one deployed self-managed staging release. The product
normalizes content-free evidence; it never runs the review or self-certifies
CP2-A.

## Evidence chain

1. Validate the exact staging platform inventory, infrastructure plan, and
   ready applied-change receipt.
2. Validate the exact passed staging Kubernetes release receipt.
3. An independent reviewer uses private tooling to assess control APIs, forced
   RLS, identifier substitution, cache namespaces, concealment/errors, and
   timing inference, including remediation and retests.
4. Retain all raw artifacts in an immutable private dossier and put only each
   domain's outcome, bounded finding count, and evidence SHA-256 in the input.
5. Normalize it:

   ```sh
   make saas-isolation-review-check \
     PLATFORM_INVENTORY=/private/staging-inventory.json \
     PLATFORM_PLAN=/private/staging-plan.json \
     PLATFORM_CHANGE=/private/staging-change.json \
     STAGING_RELEASE=/immutable/staging-release.json \
     ISOLATION_REVIEW=/private/tenant-isolation-review.json \
     ISOLATION_REVIEW_RECEIPT=/immutable/tenant-isolation-receipt.json
   ```

6. Bind the unchanged dossier to current CP2-A approval signed by the
   independent-security owner through the external-evidence index.

## Invariants

- Environment is exactly staging; inventory, change, release, and review
  identities and opened-byte digests must match.
- Applied change assesses ready and the Kubernetes release is passed.
- Review starts after both deployments, lasts at most fourteen days, input is
  generated after completion, and normalization occurs within 24 hours.
- Exactly six fixed domains cover control API authorization, forced RLS,
  identifier substitution, cache namespace, concealment/error behavior, and
  timing inference.
- Each domain contains only passed/failed/inconclusive, finding count, and a
  private evidence digest. Passed requires zero findings; failed requires at
  least one; inconclusive requires zero known findings.
- Only six passed domains and zero findings are ready. Honest failure or
  uncertainty is valid-unready; contradictory readiness is malformed.
- Never include reviewer identity, tenant or resource IDs, corpus, queries,
  identifiers, cache keys, timing samples/thresholds, SQL, schemas, topology,
  endpoints, credentials, logs, traces, content, finding text, or raw output.
- Input is strict bounded regular non-symlink JSON. Output is atomic,
  create-only, mode `0600`, and aggregate-only on stdout.
- Exit codes remain `0` ready, `3` valid-unready, `2` usage, and `1` unsafe,
  malformed, or operational failure.
- Local tests, local-alpha receipts, mocks, and disposable deployments never
  close CP2-A.

## Change workflow

1. Read `.kiro/specs/saas-product-platform/requirements.md`, `design.md`, then
   `tasks.md`; update all three before changing behavior.
2. Write a failing test first.
3. Keep logic under `internal/saas/isolationreview`, CLI under
   `cmd/agent-memory-isolation-review`, schemas under `api/evidence/v1`, and
   the runbook under `docs/saas/staging-tenant-isolation-review.md`.
4. Preserve CP2-A as open and state the exact deployed evidence and signature
   still missing.

## Verification

```sh
go test -race ./internal/saas/isolationreview ./cmd/agent-memory-isolation-review ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
actionlint
git diff --check
```

Parse every JSON file under `api/` and `docs/saas/`, then prove the canonical
57-control catalog still exactly matches the matrix. A locally ready fixture
proves the normalizer only, not the independent review.
