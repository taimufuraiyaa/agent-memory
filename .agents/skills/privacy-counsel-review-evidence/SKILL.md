---
name: privacy-counsel-review-evidence
description: Verify or extend the Agent Memory CP7-A Privacy/Counsel rendered UI, copy, and customer-rights receipt review evidence boundary.
---

# Privacy and Counsel review evidence

Use this skill when changing CP7-A review coverage, artifact bindings, schemas,
normalizer behavior, CLI output, or the Phase 7 evidence matrix.

## Invariants

- Keep CP7-A in the exact 57-control catalog and externally open. Repository
  fixtures prove normalization only and never replace real signatures.
- Require exactly four surfaces: `privacy_overview`, `source_custody`,
  `source_details`, and `source_deletion`.
- Require exactly five contracts: `rights_attestation`, `privacy_overview`,
  `source_deletion`, `account_deletion`, and `portable_export`.
- Bind rendered/copy/accessibility and schema/compatibility artifacts only by
  SHA-256. Never emit UI text, schemas, routes, paths, signer identity,
  signatures, keys, payloads, or customer data.
- Preserve complete failed or inconclusive review as valid-unready. Reject
  incomplete sets, unknown fields, local classification, unsafe files,
  contradictory checks/readiness, stale generation, and future timelines.
- Publish atomically, create-only, non-symlink, and mode `0600`. CLI output is
  aggregate-only with exit codes `0/3/2/1`.
- Hash and decode the same opened bounded regular file, with identity and size
  checks before and after reading; reject validate-then-open/read replacement.

## Workflow

1. Read R67, the CP7-A design section, and P7.8 in the SaaS product spec.
2. Inspect the dashboard and `api/openapi/saas-v1.yaml` before changing the
   fixed coverage set; update requirements, design, and tasks first.
3. Write or change adversarial tests before implementation.
4. Run:

   `go test ./internal/saas/privacyreviewevidence ./cmd/agent-memory-privacy-review`

5. Run `make contracts-check`, then the full Go test, race, vet, JSON/schema,
   Kubernetes/release, and signed external-index gates used by the repository.
6. Recount acceptance items and confirm all exact 57 external controls remain
   open unless authoritative external dossiers actually exist.
