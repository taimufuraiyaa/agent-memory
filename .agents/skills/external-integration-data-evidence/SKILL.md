---
name: external-integration-data-evidence
description: Verify or extend the Agent Memory P0.2-C external-integration data-purpose evidence boundary. Use when changing inventory binding, payment/email/model review rules, schemas, CLI, runbook, or P0.2-C repository support.
---

# External-integration data-purpose evidence

Preserve P0.2-C as a content-free, read-only normalizer for the three explicit
external business integrations. Repository fixtures prove the contract; only
real installed settings, traffic exports, private decisions, and current
Privacy/Security signatures can close P0.2-C.

## Read first

- `.kiro/specs/saas-product-platform/requirements.md` R61
- `.kiro/specs/saas-product-platform/design.md`, “External-integration data-purpose review evidence”
- `.kiro/specs/saas-product-platform/tasks.md` P0.5
- `docs/saas/external-integration-data-policy.md`
- `docs/saas/external-integration-review.md`
- `internal/saas/externalintegrationevidence/evidence.go`
- `internal/saas/platforminventory/inventory.go`

## Preserve the boundary

1. Reload and hash one exact valid self-managed staging or production platform
   inventory. Accept only `self_managed_external` input for that same
   environment, inventory ID, and receipt digest.
2. Require exactly payment, transactional email, and model in canonical order.
   Each enabled state must match the authoritative inventory; reject missing,
   extra, duplicate, or substituted integrations.
3. Bind every integration to opaque configuration and purpose versions plus
   SHA-256 digests for configuration, purpose decision, contract-or-disabled
   state, retention/training settings, traffic export, and exit plan.
4. Disabled integrations require zero approved fields and sampled requests and
   no prohibited observations. Enabled integrations require positive approved-
   field and sampled-request coverage; otherwise they are inconclusive.
5. Customer-content bytes, unapproved fields, unallowlisted destinations,
   provider training, or general logging derive a failed integration outcome.
   Clean positive enabled coverage and clean disabled proof derive passed.
6. Require exactly seven checks in canonical order: inventory binding, purpose,
   contract-or-disabled state, settings, traffic allowlist, minimization, and
   accountable Privacy/Security review. Independently derive settings,
   allowlist, and minimization outcomes from integration observations.
7. Readiness requires all three integrations and all seven checks passed.
   Preserve complete failed or inconclusive reviews as valid-unready; reject
   contradictory readiness, outcomes, checks, chronology, or inventory state.
8. Reject symlinks, unknown fields, trailing JSON, oversized files, and open/
   validate races. Publish create-only mode-`0600` receipts and keep CLI output
   aggregate-only with exits `0` ready, `3` valid-unready, `2` usage, and `1`
   invalid.

Keep provider names, destinations, endpoints, contracts, settings exports,
traffic samples, invoices, messages, prompts, passages, credentials, people,
signatures, and paths in private immutable custody. Only opaque versions,
digests, fixed states, and aggregate counts enter normalized evidence. The
signed external-evidence index remains the final approval boundary.

## Safe change workflow

1. Update R61, design, and P0.5 before changing behavior.
2. Add failing tests for each new invariant before implementation.
3. Keep both JSON schemas closed and update content-exclusion contract tests.
4. Update the example, Make target, runbook, status, and P0.2-C matrix row.
5. Never alter the exact 57-control catalog or mark P0.2-C externally complete
   from local or synthetic fixtures.

## Verification

```sh
go test -race ./internal/saas/platforminventory ./internal/saas/externalintegrationevidence ./cmd/agent-memory-external-integration ./internal/contracts -count=1
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
