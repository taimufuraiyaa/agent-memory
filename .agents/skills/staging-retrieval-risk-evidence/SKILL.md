---
name: staging-retrieval-risk-evidence
description: Verify or extend the Agent Memory CP5-B independent blind two-tenant retrieval risk evidence boundary. Use when changing result/count/cache leak metrics, timing tolerance, review domains, schemas, normalizer/CLI, runbook, or independent-security approval handoff.
---

# Staging retrieval-risk evidence

## Boundary

CP5-B is an independent blind-corpus review of retrieval result, public-count,
timing, and cache risks. Repository code only normalizes content-free evidence.
Never run probes, accept staging credentials, embed corpus/findings/samples, or
promote local rehearsal or CP2-A review to CP5-B proof.

Bind exact staging inventory, reviewed plan, ready change, passed release, and
private blind-corpus, timing-report, cache-review, and risk-decision digests.
The external-evidence index is the final `cp5_b` signature boundary.

## Contract

Require exactly two tenants and seven canonical domains: blind-corpus
independence, result isolation, public-count concealment, statistical timing,
cache-key namespace, warm-cache contamination, and risk acceptance. Passed has
zero findings, failed at least one, and inconclusive no known finding.

Independently enforce non-negative bounded result/count/cache leak counts and
integer timing values. Positive leaks and observed timing beyond the approved
tolerance must map to failed domains. Preserve honest failures as valid-unready;
reject contradictions, missing/duplicate/unknown domains, unsafe identifiers,
pre-release or over-fourteen-day reviews, stale/future input, upstream mismatch,
symlinks, and unknown fields.

Exclude reviewer, corpus/query/marker, tenant/account/source/passage/citation/
cache-key identities, timing distributions, findings, attack details,
credentials, endpoints, logs, traces, SQL, and raw output. Publication is
atomic, create-only, mode `0600`; CLI output is aggregate-only with exit codes
`0` ready, `3` valid-unready, `2` usage, and `1` invalid/operational.

## Verification

```sh
go test -race ./internal/saas/retrievalrisk ./cmd/agent-memory-retrieval-risk ./evaluation/isolation ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P5.7 items. CP5-B remains external until
the independent staged review and current signature exist.
