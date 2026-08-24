---
name: staging-operational-game-day-evidence
description: Verify or extend Agent Memory P10.3-A release-bound staging operational game-day evidence across platform, integration, credential, isolation, and deletion scenarios.
---

# Staging operational game-day evidence

Use this skill when changing R68/P10.9, scenario/check coverage, subject
reconciliation, derived response timings, schemas, CLI, runbook, or P10.3-A
matrix support.

## Preserve these invariants

- Bind exact ready staging inventory, reviewed plan, ready applied change, and
  passed Kubernetes release bytes. Never accept local/mock classification.
- Require exactly seven scenarios: database failover, queue backlog, all eight
  self-managed components, all three external integration states, credential
  leak, cross-tenant attempt, and incomplete source/account deletion.
- Enabled integrations require exercises; disabled integrations require
  disabled-state continuity. Never silently mark either not applicable.
- Derive detection, alert, acknowledgement, containment, recovery, and elapsed
  seconds from causal timestamps. Compare elapsed time with a positive approved
  target and reject caller-supplied aggregate contradictions.
- Require seven exact outcomes per drill and eight exact bundle outcomes.
  Preserve honest failures or target breaches as valid-unready.
- Never emit credentials, endpoints, topology/resource/provider names, tenant
  identity, people, contacts, commands, routes, logs, traces, alerts, tickets,
  or customer data.
- Publish create-only mode `0600`, aggregate-only CLI output, exits `0/3/2/1`.
  Local fixtures never close P10.3-A or alter the exact 57-control catalog.
- Hash and decode the same opened bounded regular file, with identity and size
  checks before and after reading; reject validate-then-open/read replacement.

## Verification

Run focused package/CLI/platform/contract race tests, `make contracts-check`,
the full Go test and vet suites, Kubernetes and release-script gates, all JSON
parsing, `git diff --check`, and the exact 57-control reconciliation defined by
`verify-external-evidence-index`.
