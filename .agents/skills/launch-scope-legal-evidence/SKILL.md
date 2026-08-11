---
name: launch-scope-legal-evidence
description: Verify or extend the Agent Memory P0.1 launch-scope and legal-position evidence boundary. Use when changing external-business scope counts, legal-position review, risk reconciliation, schemas, CLI, runbook, or P0.1-A/P0.1-B repository support.
---

# Launch-scope and legal-position evidence

Preserve P0.1 as a content-free read-only normalizer. Repository fixtures prove
the contract; only authoritative private artifacts and current external
signatures can close P0.1-A or P0.1-B.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md` R60
- `.kiro/specs/saas-product-platform/design.md`, “Launch scope and legal-position evidence”
- `.kiro/specs/saas-product-platform/tasks.md` P0.4
- `docs/saas/launch-scope-evidence.md`
- `internal/saas/launchscopeevidence/evidence.go`

## Preserve the boundary

1. Accept only strict bounded regular-file `external_business` input for the
   `external` environment; reject symlinks, unknown fields, trailing JSON, and
   validate/open races.
2. Bind opaque scope, jurisdiction-policy, legal-review, and risk-register
   versions to exact decision-register, launch-decision, jurisdiction-memo,
   policy-manifest, legal-review, and risk-register SHA-256 digests.
3. Normalize only positive launch-country, support-language, and notice-
   jurisdiction counts plus a positive numeric minimum age. Never copy country
   lists, jurisdiction names, languages, legal text, or policy copy.
4. Require exactly six legal positions in canonical order:
   - rights attestation
   - source retention
   - rights notice
   - deletion
   - backup
   - audit retention
5. Require exactly eight checks in canonical order: scope approval,
   jurisdiction-memo completeness, minimum age, support coverage, notice
   routing, legal-position review, risk reconciliation, and accountable
   Product/Counsel/Privacy review.
6. Independently derive the legal-review check from the six position outcomes
   and the risk check from blocking/unowned counts. All checks and positions
   passed plus zero blocking and unowned risks are required for readiness.
7. Preserve complete failed or inconclusive evidence as valid-unready. Reject
   missing, duplicate, stale, malformed, substituted, or contradictory input.
8. Publish create-only mode-`0600` receipts. CLI output remains aggregate-only
   with exits `0` ready, `3` valid-unready, `2` usage, and `1` invalid.

Keep legal analysis, risk descriptions, people, organizations, signatures,
keys, evidence references, and paths in the private immutable dossier. The
signed external-evidence index remains the final approval boundary.

## Safe change workflow

1. Update R60, design, and P0.4 before changing behavior.
2. Add a failing test for each new invariant before implementation.
3. Keep both JSON schemas closed and update content-exclusion contract tests.
4. Update the example, Make target, runbook, status, and both P0.1 matrix rows.
5. Never change the exact 57-control catalog or mark P0.1-A/P0.1-B complete
   from a local fixture.

## Verification

```sh
go test -race ./internal/saas/launchscopeevidence ./cmd/agent-memory-launch-scope ./internal/contracts -count=1
make contracts-check
make saas-kubernetes-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
find api docs .kiro -name '*.json' -type f -print0 | xargs -0 -n1 jq empty
git diff --check
```

Run the narrow signed-index gates from
`.agents/skills/verify-external-evidence-index/SKILL.md`, then reconcile 57
catalog IDs, 57 matrix rows, and 57 unchecked external acceptance items.
