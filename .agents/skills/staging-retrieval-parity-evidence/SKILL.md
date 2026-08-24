---
name: staging-retrieval-parity-evidence
description: Verify or extend the Agent Memory CP5-A representative staging retrieval-parity evidence boundary. Use when changing approved threshold binding, parity metrics/checks, schemas, normalizer/CLI, runbook, or Product and Engineering approval handoff.
---

# Staging retrieval-parity evidence

## Boundary

CP5-A needs a representative staged parity run and an approved threshold. The
repository normalizer only binds and validates content-free evidence. Never run
retrieval, connect to databases/model providers, copy benchmark content, or
claim that the fixture/local-alpha run proves staging.

Load exact opened bytes for staging inventory, reviewed plan, ready change,
passed release, and the content-free parity input. Bind the private threshold-
decision and immutable parity-report SHA-256 values. The signed external-
evidence index remains the only final `cp5_a` approval boundary.

## Fixed contract

Exactly eight checks are canonical: representative dataset, top-k overlap,
normalized score delta, exact-term winner, feedback preference, decay,
suppression, and citation resolution. Each stores only its fixed ID,
passed/failed/inconclusive, and a private evidence digest.

Independently require observed overlap to meet the approved minimum and observed
score delta not to exceed the approved maximum. Metric-check outcomes must not
contradict observations. Preserve failed/inconclusive checks and metric breaches
as valid-unready. Reject invalid metrics, missing/duplicate/unknown checks,
unsafe versions, threshold approval after evaluation start, pre-release or
over-24-hour runs, stale/future input, contradictory readiness, upstream
mismatch, symlinks, and unknown fields.

Keep corpus/query text, tenant/source/passage/candidate/citation identities,
individual scores/order, explanations, model data, endpoints, credentials,
logs, traces, SQL, and raw output outside the normalized files and CLI output.
Publication is atomic, create-only, non-symlink, and mode `0600`; exit codes are
`0` ready, `3` valid-unready, `2` usage, and `1` invalid/operational.

## Change and verification workflow

1. Read R40, its design section, and P5.6.
2. Write failing tests before changing behavior.
3. Keep benchmark execution outside `internal/saas/parityevidence`.
4. Update both schemas, example, runbook, Make target, matrix, and status.
5. Run:

```sh
go test -race ./internal/saas/parityevidence ./cmd/agent-memory-parity-evidence ./evaluation/parity ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
actionlint
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository work contributes three accepted P5.6 items. Exactly 57 external
controls remain until a representative staging report and current signed
Product/Engineering decision exist.
