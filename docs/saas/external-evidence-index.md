# Signed External Evidence Index

The external-evidence index turns the 57 open P0-P12 controls into one
machine-verifiable collection queue. It verifies integrity and authorization;
it does not claim that a local fixture, Floci, or a repository test satisfies a
staging, provider, legal, business, elapsed-window, or independent-review
control.

## Package layout

Keep the working package outside the repository and application database:

```text
evidence-package/
  index.json
  approver-trust.json
  approvals/
    p1_2_a.json
  artifacts/
    p1_2_a.tar.zst
```

The catalog remains in the repository at
`api/evidence/v1/external-control-catalog.json`. Each `index.json` entry binds a
catalog control to one deterministic dossier under `artifacts/`, a durable
content-free external URI, SHA-256 digest, external classification,
environment, collection time, and optional release or observation window.
Dossiers may contain sensitive reports and therefore stay out of verifier
output, CI logs, PostgreSQL, and Git.

Start from `docs/saas/external-evidence-index.example.json`. Unknown fields,
unknown or duplicate controls, local/mock classifications, absolute or
traversing paths, symlinks, empty files, and mismatched classifications fail
closed.

## Collect one control

1. Find the human ID, normalized `approval_control`, owner group, and exact
   evidence requirement in the canonical catalog.
2. Collect the real artifact from the accountable external system. Create one
   deterministic dossier and publish the unchanged dossier to immutable
   storage. Record a content-free URI that reviewers can resolve.
3. Copy the same dossier into the package below `artifacts/`. Add exactly one
   index entry. Use `external_staging`, `external_production`,
   `external_review`, `external_business`, or `external_observation`; use
   `staging`, `production`, or `external` for its environment.
4. Have an authorized owner review and sign that exact dossier. For example:

```sh
go run ./cmd/agent-memory-release-approval \
  --private-key /owner-control/operations.pem \
  --gate external_evidence \
  --control p1_2_a \
  --decision approved \
  --owner operations-review \
  --key-id operations-2026 \
  --evidence /evidence-package/artifacts/p1_2_a.tar.zst \
  --evidence-ref evidence://staging/releases/v1.2.3/p1_2_a \
  --valid-for 168h \
  > /evidence-package/approvals/p1_2_a.json
```

The separately managed trust bundle must scope that public key to gate
`external_evidence` and control `p1_2_a`. Retain newer rejection decisions in
the exported approval directory; newest signed decision wins.

## Verify the complete index

```sh
make saas-external-evidence-check \
  EVIDENCE_INDEX=/evidence-package/index.json \
  EVIDENCE_ROOT=/evidence-package \
  EVIDENCE_TRUST=/evidence-package/approver-trust.json \
  EVIDENCE_APPROVALS=/evidence-package/approvals
```

Exit `0` means all 57 dossiers exist, hash correctly, and have current matching
authorized approvals. Exit `3` means the inputs are valid but controls are
missing, rejected, or expired. Exit `2` is invalid CLI configuration; exit `1`
is malformed/unsafe evidence or another verification failure.

The JSON report contains only counts and sorted human control IDs. Archive the
report with the release record, but retain the dossiers, index, approval
history, and trust bundle in the immutable external evidence system. Never mark
the matrix rows complete based only on a local ready fixture.
