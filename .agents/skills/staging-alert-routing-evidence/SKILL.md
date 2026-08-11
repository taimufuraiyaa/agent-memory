---
name: staging-alert-routing-evidence
description: Verify or extend the Agent Memory P10.3-B installed SLO and cost alert-routing evidence boundary. Use when changing fixed rules, routes, owner rosters, synthetic drill timelines, targets, schemas, normalizer/CLI, runbook, or Operations/Finance approval handoff.
---

# Staging alert-routing evidence

## Boundary

P10.3-B is a real installed-route control. Repository code only normalizes
content-free evidence. Never fire alerts, accept routing credentials, inspect
private routes, invent target decisions, or promote rule configuration and
placeholder receivers to routed-delivery proof.

Bind the exact ready staging inventory/plan/change chain, passed release,
deployed rule and route exports, owner roster, private synthetic report, and
approved target decision. The external-evidence index is the final signature
boundary.

## Contract

Require exactly seven canonical API error-budget, API latency, worker, queue,
object-storage, model-gateway, and cost-spike tests. Enforce the reviewed page or
ticket severity, opaque owner-slot versions, and causal trigger, delivery,
escalation, acknowledgement, and resolution timestamps. Derive integer-second
durations and compare delivery and acknowledgement against positive targets of
at most four hours.

Preserve honest failures and inconclusive results as valid-unready. Reject known
breaches labeled passed or inconclusive, contradictory readiness, missing or
duplicate tests, wrong severity, unsafe IDs, pre-release/overlong/stale bundles,
upstream mismatch, unknown fields, and symlinks.

Exclude route URLs/names, receiver names, people, schedules, phone/email/chat/
ticket IDs, credentials, logs, traces, and content. Publication is atomic,
create-only, mode `0600`; CLI exits `0` ready, `3` valid-unready, `2` usage, and
`1` invalid or operational failure.

## Verification

```sh
go test -race ./internal/saas/alertevidence ./cmd/agent-memory-alert-evidence ./internal/contracts -count=1
make contracts-check
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
git diff --check
```

Repository support contributes three P10.5 items. P10.3-B remains external
until real installed routes, synthetic drills, private evidence, and
Operations/Finance signatures exist.
