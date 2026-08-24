---
name: staging-migration-cohort-evidence
description: Verify or extend Agent Memory CP9-A representative internal migration-cohort evidence. Use when changing cohort consent, format/size coverage, AMPB2 reconciliation aggregates, staging platform/release binding, schemas, CLI, runbook, or Product/QA evidence handoff.
---

# Staging migration-cohort evidence

Preserve the distinction between repository proof and a real internal cohort.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md`, R58
- `.kiro/specs/saas-product-platform/design.md`, representative
  migration-cohort evidence
- `.kiro/specs/saas-product-platform/tasks.md`, P9.5
- `internal/saas/migrationcohortevidence`
- `docs/saas/staging-migration-cohort.md`
- `api/evidence/v1/staging-migration-cohort-*.schema.json`

## Preserve invariants

- Bind the exact ready staging inventory, reviewed plan, ready applied change,
  and passed Kubernetes release. Never accept local/mock classification.
- Require consent approval before cohort start and no more than 31 days old.
- Keep PDF, EPUB, Markdown, and text as the exact four formats. Require positive
  coverage for each.
- Keep small, medium, and large as the exact three size buckets. Their concrete
  byte thresholds live in the private approved cohort decision; require
  positive coverage for each.
- Reconcile expected items both to selected sources plus memories plus notes and
  to imported plus merged plus skipped plus failed results.
- Ready evidence requires zero failed items, unexplained losses, and duplicate
  publications plus all nine checks passed.
- Preserve complete failed or inconclusive evidence as valid-unready when its
  counts, checks, and top-level readiness agree. Reject contradictions.
- Exclude account, tenant, workspace, source, memory, note, filename, content,
  failure-message, operator, and reviewer identities. Bind private artifacts by
  SHA-256 only.
- Load only bounded regular non-symlink JSON with unknown fields rejected.
  Publish receipts create-only with mode `0600` and aggregate CLI output.
- A fixture proves the collector only. CP9-A stays open until the real consented
  cohort dossier is immutable and Product/QA approve its exact digest through
  the signed external-evidence index.

## Verification

```sh
go test -race ./internal/saas/migrationcohortevidence \
  ./cmd/agent-memory-migration-cohort ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./...
go vet ./...
git diff --check
```

Also parse every JSON file and confirm the external catalog and matrix still
contain exactly the same 57 controls.
