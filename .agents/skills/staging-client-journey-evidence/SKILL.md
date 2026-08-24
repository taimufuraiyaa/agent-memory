---
name: staging-client-journey-evidence
description: Verify or extend the Agent Memory CP3-A human-web and scoped-agent staging journey bundle without accepting credentials, customer content, or local evidence as staging proof. Use when changing the journey schemas, validator, CLI, Make target, runbook, CP3-A matrix support, or evidence file boundaries.
---

# Staging client journey evidence

Keep repository validation separate from the external claim that real staging
clients completed their journeys.

## Read before changing

1. `.kiro/specs/saas-product-platform/requirements.md`, R30.
2. `.kiro/specs/saas-product-platform/design.md`, staging client-journey
   evidence bundle.
3. `.kiro/specs/saas-product-platform/tasks.md`, P3.6 and Checkpoint 3.
4. `docs/saas/staging-client-journeys.md` and
   `docs/saas/external-evidence-matrix.md`, CP3-A.
5. `api/evidence/v1/staging-client-journey.schema.json` and
   `staging-client-journey-bundle.schema.json`.
6. `internal/saas/stagingjourney` and
   `cmd/agent-memory-staging-journey`.

## Preserve invariants

- Bind exactly one `human_web` and one `scoped_agent` input to the exact ID and
  SHA-256 of one passed staging Kubernetes release receipt.
- Accept only `staging_external`, strict bounded JSON from regular non-symlink
  files. Revalidate file identity before, during, and after the opened-byte
  hash is computed.
- Require exactly five checks per client: authentication, audited memory write,
  audited memory search, audited ready export, and client cleanup.
- Require canonical unique UUID-v4 request IDs and unique lowercase
  32-character hexadecimal trace IDs across both clients.
- Keep each journey after release completion, at most 30 minutes long, not in
  the future, and no more than 24 hours old at collection.
- Preserve valid-but-unready receipts when a fixed check fails. Readiness must
  exactly equal the check outcomes.
- Never accept or emit tokens, cookies, authorization headers, tenant/account/
  workspace/memory/export/credential IDs, content, queries, results, URLs,
  browser storage, screenshots, raw audit/telemetry bodies, or logs.
- Publish the combined receipt atomically, create-only, non-symlink, and mode
  `0600`. CLI output contains aggregate counts only.
- Preserve exit codes: `0` ready, `3` valid but unready, `2` invalid command
  use, and `1` malformed/unsafe or operational failure.
- Never check CP3-A or Checkpoint 3 from fixtures, local Compose, tests, or the
  collector alone. Real self-managed staging inputs, matching private
  trace/audit exports, immutable retention, and signed Product/QA approval are
  still required.

## Verify

Run the focused gates:

```sh
go test ./internal/saas/stagingjourney \
  ./cmd/agent-memory-staging-journey \
  ./internal/contracts -count=1
jq empty \
  api/evidence/v1/staging-client-journey.schema.json \
  api/evidence/v1/staging-client-journey-bundle.schema.json
make -n saas-staging-journey-collect \
  STAGING_RELEASE=/tmp/release.json \
  STAGING_HUMAN_JOURNEY=/tmp/human.json \
  STAGING_AGENT_JOURNEY=/tmp/agent.json \
  STAGING_JOURNEY_RECEIPT=/tmp/bundle.json
```

Then run full Go tests, vet, actionlint, and `git diff --check`. Treat a mocked
ready run as collector evidence only. A real run must retain both private raw
journeys and their matching trace/audit evidence outside the application
database before the signed external-evidence workflow can evaluate CP3-A.
