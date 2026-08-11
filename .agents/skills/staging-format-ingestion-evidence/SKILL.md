---
name: staging-format-ingestion-evidence
description: Verify or extend the Agent Memory CP4-C release-bound PDF, EPUB, Markdown, and text staging-ingestion evidence boundary. Use when changing the four-format input/receipt schemas, source/version/job/projection bindings, staging format collector or CLI, Make target, operator runbook, or Checkpoint 4 evidence support.
---

# Staging four-format ingestion evidence

Use this workflow to keep staging evidence content-free and prevent local
fixtures from self-certifying CP4-C.

## Read first

1. `.kiro/specs/saas-product-platform/requirements.md` R32.
2. `.kiro/specs/saas-product-platform/design.md`, “Staging four-format ingestion evidence”.
3. `.kiro/specs/saas-product-platform/tasks.md` P4.7 and Checkpoint 4.
4. `docs/saas/staging-format-ingestion.md`.
5. `internal/saas/stagingformat/format.go` and its tests.

## Invariants

- Bind one input to the exact ID and opened-byte SHA-256 of a passed staging
  Kubernetes release receipt.
- Require exactly PDF/application-pdf, EPUB/application-epub+zip,
  Markdown/text-markdown, and text/text-plain format/media pairs.
- Use canonical UUID-v4 source and ingestion-job IDs and unique lowercase
  32-character trace IDs. Do not reuse any record UUID across source/job roles.
- Require positive source versions, hashed source-version and terminal-job
  receipts, and both full-text and vector projection summaries with bounded
  versions, counts, and receipt hashes.
- Require exactly seven checks: upload acceptance, source-version publication,
  job success, full-text readiness, vector readiness, source readiness, and
  deletion of the temporary staging source.
- Bind version/job/projection check hashes to their corresponding summaries and
  reject downstream success after a failed prerequisite.
- Preserve failed checks or zero document counts as valid-but-unready. Reject
  contradictory run or bundle readiness.
- Every run starts after the release, lasts at most six hours, and all runs fit
  in a 24-hour bundle. Generate after all runs and collect within 24 hours.
- Reject symlinks, non-regular or oversized files, unknown JSON, duplicate or
  missing formats/checks/identifiers, media mismatch, invalid clocks, local or
  mock classification, and release mismatch.
- Never admit source bytes, extracted text, filenames, titles, checksums,
  tenant/account/workspace IDs, object keys, paths, URLs, credentials, headers,
  queries, results, logs, or raw records.
- Publish atomically, create-only, and mode `0600`; CLI output is aggregate.
- Local Compose/Floci and mocked inputs prove implementation only. Do not close
  CP4-C without real staging origin, immutable private source/version/job/
  projection evidence, and signed QA/Operations approval.

## Change workflow

1. Update R32, design, and P4.7 before behavior changes.
2. Add a failing package or CLI test.
3. Implement the smallest behavior change and run focused tests.
4. Update both JSON schemas, contract test, canonical example, Make target,
   runbook, external-evidence matrix, and this skill when their boundary moves.
5. Run `make saas-local-alpha-gate` for a current isolated four-format
   lifecycle. Confirm its manifest says `local_development`, lifecycle says
   `formats=4`, four source deletion receipts exist, and cleanup passes.
6. Run full Go tests, vet, actionlint, JSON parsing, external-evidence verifier,
   and diff checks before checking P4.7 acceptance items.
7. Recount readiness while leaving CP4-C and the 57 external controls open.

## Focused commands

```sh
go test ./internal/saas/stagingformat ./cmd/agent-memory-staging-format ./internal/contracts -count=1
jq empty api/evidence/v1/staging-format-ingestion.schema.json api/evidence/v1/staging-format-ingestion-receipt.schema.json docs/saas/staging-format-ingestion.example.json
git diff --check
```

For real private inputs:

```sh
make saas-staging-format-collect \
  STAGING_RELEASE=/private/release.json \
  STAGING_FORMAT_INPUT=/private/input.json \
  STAGING_FORMAT_RECEIPT=/immutable/receipt.json
```

Exit `0` is ready, `3` is valid-unready, `2` is usage, and `1` is unsafe,
malformed, or operational failure.
