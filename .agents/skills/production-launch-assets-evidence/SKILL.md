---
name: production-launch-assets-evidence
description: Verify or extend Agent Memory CP11-A public launch-asset evidence. Use when changing the seven fixed signup/legal/status/support/security assets, owner groups, liveness observations, schemas, CLI, runbook, or accountable approval handoff.
---

# Production launch-assets evidence

## Boundary

CP11-A requires real externally reachable production assets and accountable
approval. Repository code only normalizes content-free evidence. Never make
network requests from the normalizer, accept credentials, retain actual URLs,
hostnames, rendered copy, personal contacts, monitor destinations, logs,
traces, payloads, raw probes, or treat fixtures as launch proof.

Bind the review to the exact ready production inventory, infrastructure plan,
applied change, and passed release. Require exactly these seven assets and
owner groups:

- `external_signup` — `product`
- `terms_of_service`, `privacy_notice`, `content_rights_policy` —
  `product_counsel`
- `status_page` — `operations`
- `support_policy` — `support_operations`
- `security_contact` — `security`

Each asset binds SHA-256 digests for its public URL, rendered copy, monitoring
configuration, route test, and owner decision. Derive liveness only when HTTP
status is 200, the positive bounded probe population is completely successful,
and the latest observation is no more than 900 seconds before the snapshot.
Honest liveness failures are valid-unready only when the live-probe check fails;
green contradictions fail closed.

Require exactly nine checks for manifest, copy, probe coverage, ownership,
monitoring, and Product/Counsel/Support/Security review. Publish a create-only
mode-`0600` receipt. CLI exit codes are 0 ready, 3 valid-unready, 2 usage, and 1
invalid/publication failure.

## Verification

```sh
go test -race ./internal/saas/launchassetevidence ./cmd/agent-memory-launch-assets ./internal/contracts -count=1
make contracts-check
make saas-release-script-test
go test ./... -count=1
go vet ./...
find api/evidence/v1 docs/saas -name '*.json' -print0 | xargs -0 -n1 jq -e . >/dev/null
go test ./internal/saas/evidenceindex -count=1
git diff --check
```

Repository support contributes three P11.8 items. CP11-A remains external until
the real public assets, immutable private copies, installed monitoring and route
tests, owner decisions, and signed Product/Counsel/Support/Security approvals
exist.
