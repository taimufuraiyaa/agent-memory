---
name: safe-platform-launch-state-evidence
description: Verify or extend the Agent Memory CP1-C fail-closed staging launch-state evidence boundary. Use when changing launch-policy defaults, signup admission migrations, the read-only launch-state collector, receipt schema, CLI, runbook, matrix support, or Product approval handoff.
---

# Safe-platform launch-state evidence

## Boundary

CP1-C has two parts that must not be conflated:

1. repository-owned technical proof that one exact staging platform remains in
   a fail-closed pre-customer state; and
2. externally retained Product approval that no customer feature launched.

Repository tests, examples, local profiles, and disposable databases prove only
the implementation. Never close CP1-C without a real staging receipt, immutable
private exposure/policy evidence, and an authorized `cp1_c` dossier signature.

## Invariants

- Fresh `internal_alpha` installs seed `signup_enabled=false` and
  `invitation_required=true`.
- Additive migration `0028_launch_policy_safe_default` closes already-migrated
  internal-alpha policy state. Its down migration must never reopen signup.
- Evidence collection is read-only and never changes launch policy.
- Load the exact staging platform inventory, infrastructure plan, ready applied
  change, and passed Kubernetes release before observing PostgreSQL.
- Query only singleton `saas_launch_policy` fields: `phase`, `signup_enabled`,
  `invitation_required`, `policy_version`, and `updated_at`.
- Never query accounts, tenants, invitations, signup attempts/reservations,
  customer content, counts, countries, quotas, feature flags, actors, or reasons.
- Ready requires `internal_alpha`, signup disabled, invitations required, a
  bounded policy version, and a policy time no later than collection.
- Valid private-beta/public-beta/GA or enabled-signup observations are unready,
  not parser failures. Malformed, future, misbound, or unready upstream evidence
  fails closed.
- Receipt classification is `staging_external`; publication is atomic,
  create-only, non-symlink, and mode `0600`.
- CLI PostgreSQL authority comes only from `AGENT_MEMORY_POSTGRES_URL`. Output
  contains readiness, phase, and receipt-written state only. Exit codes are
  `0` ready, `3` valid-unready, `2` usage, and `1` unsafe/operational failure.

## Change workflow

1. Read `.kiro/specs/saas-product-platform/requirements.md`, `design.md`, then
   `tasks.md`, especially R37 and P1.22. Update them before non-trivial changes.
2. Inspect migrations 0024, 0025, and 0028 plus `internal/saas/launch/service.go`.
3. Write failing tests first for migration defaults, policy assessment,
   upstream byte bindings, unsafe timestamps/versions, publication, CLI output,
   and schema closure.
4. Keep the collector in `internal/saas/launchstate` and the CLI in
   `cmd/agent-memory-launch-state`. Do not add mutation or arbitrary-query
   options.
5. Update `api/evidence/v1/staging-safe-platform-launch-state.schema.json`,
   the example, runbook, Make target, external matrix, and implementation status.
6. Preserve the existing external-evidence index and Ed25519 approval system;
   do not invent a second signature mechanism.

## Verification

Run at minimum:

```sh
go test -race ./internal/saas/launchstate ./cmd/agent-memory-launch-state ./internal/saas/postgres ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

If `AGENT_MEMORY_TEST_POSTGRES_URL` is configured, confirm the migration test
runs rather than skips. A passing disposable PostgreSQL check still does not
constitute staging evidence.

After validation, reconcile tasks mechanically: P1.22 contributes three
repository-owned acceptance items, while the 57-control external catalog must
remain exactly synchronized with the matrix and all 57 remain open until their
real dossiers are collected and signed.
