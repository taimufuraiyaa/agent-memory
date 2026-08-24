---
name: staging-retrieval-load-evidence
description: Verify or extend the Agent Memory CP5-C deployed staging retrieval load, latency, model-route, and cost evidence boundary. Use when changing load metrics, cost targets, schemas, normalizer/CLI, runbook, or Product/Operations approval handoff.
---

# Staging retrieval load evidence

## Boundary

CP5-C is a real installed-site load and model-cost control. Repository code only
normalizes content-free evidence. Never run load, accept deployment/provider/
observability credentials, retain samples or content, invent a model-cost
ceiling, or promote the generation-disabled local rehearsal to CP5-C proof.

Bind the exact staging inventory, reviewed plan, ready change, passed release,
and private workload-manifest, load-report, cost-report, and target-decision
digests. The external-evidence index is the final `cp5_c` signature boundary.

## Contract

Require opaque workload/site/route/target/run versions, positive corpus/request/
concurrency counts, bounded errors and model calls, ordered integer p50/p95/p99
microseconds, and integer micro-US dollars per 1,000 requests. The approved cost
ceiling is positive; the repository search target is p95 strictly below 800,000
microseconds.

Require eight canonical corpus/site/route/concurrency/distribution/p95/cost-
attribution/cost-target outcomes. Independently require zero errors and positive
model calls. Preserve honest failures as valid-unready; reject contradictions,
missing/duplicate/unknown checks, unsafe IDs, pre-release/overlong/stale runs,
upstream mismatch, unknown fields, and symlinks.

Exclude site/provider/region/endpoint/model names, tenant/account/source/passage
IDs, corpus/query/prompt/output, pricing terms, credentials, samples, logs,
traces, SQL, and raw output. Publication is atomic, create-only, mode `0600`;
CLI exits `0` ready, `3` valid-unready, `2` usage, and `1` invalid/operational.

## Verification

```sh
go test -race ./internal/saas/retrievalload ./cmd/agent-memory-retrieval-load ./evaluation/isolation ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P5.8 items. CP5-C remains external until
the real staged run, immutable reports, approved target, and signatures exist.
