---
name: staging-identity-safety-evidence
description: Verify or extend the Agent Memory CP2-B identity-provider outage and credential-revocation staging evidence boundary. Use when changing drill checks, timing/RTO derivation, approved targets, schemas, normalizer/CLI, runbook, or Security and Operations approval handoff.
---

# Staging identity-safety evidence

## Boundary

CP2-B requires real managed staging drills with alert, containment, RTO, audit,
and accountable-response evidence. Repository code only normalizes content-free
evidence and binds exact artifacts. Never access an identity provider, alerting
system, audit store, credential, or customer data from this package, and never
promote local-alpha rehearsal to staging proof.

Load exact opened bytes for the staging inventory, reviewed plan, ready applied
change, passed Kubernetes release, and content-free drill input. The external-
evidence index signs the final `cp2_b` dossier; do not add another signature
system or close CP2-B from fixtures.

## Fixed checks and timing

`identity_provider_outage` has exactly seven checks: real alert delivery,
cached-key continuity, fail-closed unknown/invalid trust, containment, service
recovery, immutable audit, and customer-content absence.

`credential_revocation` has exactly eight checks: abuse detection, real alert,
independent approval, production-path revocation, post-revoke denial,
containment, immutable audit, and customer-content absence.

Each check contains only its fixed ID, passed/failed/inconclusive, and a private
evidence SHA-256. Preserve honest failures and inconclusive outcomes as
valid-unready. Derive all durations from impairment, detection, alert,
containment, and recovery timestamps. Each approved RTO target is 1..86,400
seconds and binds a private approval digest. A target breach is valid-unready.

Reject missing/duplicate/unknown checks, time reversal, over-four-hour drills,
stale/future input, asserted derived durations, contradictory readiness, unsafe
identifiers, upstream mismatch, symlinks, and unknown fields. Publication is
atomic, create-only, and mode `0600`; CLI output is aggregate-only with exit
codes `0` ready, `3` valid-unready, `2` usage, and `1` invalid/operational.

## Change and verification workflow

1. Read R39, its design section, and P2.6.
2. Write failing tests for the changed invariant before implementation.
3. Keep provider and response actions outside `internal/saas/identitysafety`.
4. Update both schemas, example, runbook, Make target, matrix, and status.
5. Run:

```sh
go test -race ./internal/saas/identitysafety ./cmd/agent-memory-identity-safety ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
actionlint
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository work contributes three accepted P2.6 items. Exactly 57 external
controls remain until real staging artifacts and current signatures exist.
