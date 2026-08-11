---
name: staging-operational-safety-evidence
description: Verify or extend the Agent Memory CP1-B staging rollback, managed-secret rotation, and human operator-access evidence bundle. Use when changing rollback receipt loading, operational drill check sets, timelines, schemas, normalizer/CLI, runbook, or Security and Operations approval handoff.
---

# Staging operational-safety evidence

## Boundary

CP1-B requires real deployed demonstrations of rollback, managed-secret
rotation, and human operator access. Repository code only normalizes the
content-free evidence and binds exact artifacts. It must never rotate secrets,
grant access, retrieve audit rows, or claim that local rehearsals prove staging.

The existing external-evidence index signs the final `cp1_b` dossier. Do not add
a second signature system or check CP1-B complete from fixtures.

## Required chain

Load and bind exact opened bytes in this order:

1. staging platform inventory;
2. reviewed infrastructure plan;
3. ready applied-change receipt;
4. passed baseline Kubernetes release;
5. later failed, rollback-succeeded release attempt;
6. live rollback-verification receipt; and
7. content-free managed-secret and human operator drill input.

Rollback receipt loading must reject symlinks, unknown fields, malformed or
duplicate deployments, invalid digests, and readiness that disagrees with the
canonical API, worker, and reconciler restoration outcomes. A failed release's
rollback boolean alone is never proof of live restoration.

## Fixed drill checks

Managed-secret rotation has exactly seven checks, in canonical order:

- `managed_replacement_created`
- `workload_rollout_completed`
- `old_value_rejected`
- `new_value_accepted`
- `service_recovered`
- `immutable_audit_retained`
- `customer_content_absent`

Human operator access has exactly seven checks:

- `human_identity_verified`
- `independent_approval_verified`
- `least_privilege_scope_verified`
- `access_expiry_enforced`
- `access_revoked`
- `immutable_audit_retained`
- `customer_content_absent`

Each check stores only `passed`, `failed`, or `inconclusive` and one private
evidence SHA-256. Preserve failed and inconclusive results as valid-unready.
Reject missing, duplicate, unknown, contradictory, stale, future, unsafe, or
misbound evidence.

Both drills begin after the passed baseline, last no more than four hours, and
complete before the input is generated. Generation follows live rollback
verification and collection occurs within 24 hours.

## Content exclusions

Never put people, account/tenant/source IDs, secret names/versions/values,
credentials, tickets, commands, endpoints, Kubernetes context, topology, audit
rows, logs, traces, SQL, customer content, or raw output in drill input,
normalized receipts, or CLI output. Keep originals only in the immutable
private dossier.

Publication remains atomic, create-only, non-symlink, and mode `0600`. CLI
output contains readiness and aggregate drill/check counts only. Exit codes are
`0` ready, `3` valid-unready, `2` usage, and `1` malformed/unsafe/operational.

## Change workflow

1. Read R38, its design section, and P1.23 in the SaaS platform spec.
2. Read `saas-kubernetes-release-gate` before changing release or rollback
   semantics.
3. Write failing tests first for receipt reload, exact byte binding, fixed
   checks, readiness consistency, timelines, unsafe files, and CLI aggregation.
4. Keep provider/human actions outside `internal/saas/operationalsafety`; the
   package is a read-only file normalizer.
5. Update both schemas, example, runbook, Make target, matrix, and status when
   the contract changes.

## Verification

```sh
go test -race ./internal/saas/platformrollback ./cmd/agent-memory-platform-rollback ./internal/saas/operationalsafety ./cmd/agent-memory-operational-safety ./internal/contracts -count=1
make saas-kubernetes-check
make saas-release-script-test
bash -n scripts/validate-saas-kubernetes.sh scripts/saas-kubernetes-release.sh scripts/tests/saas-kubernetes-release_test.sh
make contracts-check
go test ./... -count=1
go vet ./...
actionlint
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

After verification, P1.23 contributes three repository-owned accepted items.
Exactly 57 external controls remain until real staging artifacts and current
accountable signatures exist.
