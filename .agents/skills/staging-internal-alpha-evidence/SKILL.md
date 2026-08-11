---
name: staging-internal-alpha-evidence
description: Verify or extend Agent Memory P10.1-A release-bound internal-alpha lifecycle evidence across cohort caps, all formats, recurring consent, support, and deletion reconciliation.
---

# Staging internal-alpha evidence

Use this skill when changing R69/P10.10, internal cohort lifecycle/format/check
coverage, support or deletion aggregates, schemas, CLI, runbook, or P10.1-A
matrix support.

## Preserve these invariants

- Bind exact ready staging inventory, reviewed plan, ready applied change,
  passed Kubernetes release, and ready CP3-A human/agent journey bytes. Never
  accept local/mock classification or a failed prerequisite.
- Require one to 100 accounts and positive approved aggregate source-count and
  byte caps. Require exactly PDF, EPUB, Markdown, and text with positive source
  counts/bytes and reconciled indexed, non-sensitive, and deleted counts.
- Require exactly eleven causal stages: invitation, signup, rights consent,
  upload, indexing, search, memory review, export, monthly consent renewal,
  source deletion, and account deletion. Renewal must be at least 28 days after
  consent; the alpha window is 28 to 93 days.
- Require at least one controlled real-route support case, exact case/sample
  reconciliation, approved acknowledgement/resolution targets, and exact
  account/source deletion reconciliation.
- Require nine exact checks. Bind cohort approval, source policy, support
  policy, deletion manifest, trace/audit manifest, and Product/QA/Operations
  review checks to their declared SHA-256 artifacts.
- Preserve honest stage, processing, target, support, deletion, or review
  failures as valid-unready. Reject omissions, unknowns, substitutions,
  impossible time graphs, total mismatch, and readiness contradictions.
- Never emit account, tenant, workspace, source, case, memory, export,
  deletion, request, or trace IDs; content; queries; routes; contacts;
  credentials; logs; screenshots; or raw reports.
- Publish create-only mode `0600`, aggregate-only CLI output, exits `0/3/2/1`.
  Local fixtures never close P10.1-A or alter the exact 57-control catalog.

## Verification

Run focused package/CLI/platform/contract race tests, `make contracts-check`,
the full Go test and vet suites, Kubernetes and release-script gates, all JSON
parsing, `git diff --check`, and the exact 57-control reconciliation defined by
`verify-external-evidence-index`.
