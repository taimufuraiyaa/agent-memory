---
name: staging-edge-telemetry-evidence
description: Verify or extend the Agent Memory CP1-A staging edge-to-API-to-telemetry challenge collector without exposing internal query surfaces or falsely closing external evidence.
---

# Staging edge telemetry evidence

Use this skill when changing the staging request challenge, edge correlation
headers, API evidence observation, platform probe, receipt schema, CP1-A matrix
entry, or operator runbook.

## Invariants

- Bind every probe to the exact SHA-256 of a passed staging release receipt.
- Generate a canonical UUID v4 request ID and a 32-character lowercase hex
  trace ID; do not accept user-supplied identifiers in the operator workflow.
- Send only `GET /_edge/health/ready` through the edge.
- Make the edge reject `/internal`, `/internal/*`, `/metrics`, and `/metrics/*`.
- Keep the API observation cache in memory, capped at 1,024 entries and ten
  minutes. Store only request ID, trace ID, service, bounded operation, status,
  outcome, and observation time.
- Expose only `GET /internal/evidence/requests/{request_id}` on the network-
  internal API. Do not create a list, search, arbitrary-path, or trace-query API.
- Require HTTPS for the edge. Permit internal HTTP only for loopback or cluster
  service names. Reject paths, queries, fragments, user information, and
  redirects in base URLs.
- Bound and discard edge response bodies. Strictly decode only the bounded
  content-free observation response.
- Publish receipts create-only, atomically, and mode `0600`. CLI output must be
  aggregate only.
- Never treat mocked, in-process, local Compose, or collector-only evidence as
  CP1-A completion. A real passed staging release, matching external trace,
  immutable dossier, and accountable signed approval remain required.

## Verification

Run:

```sh
go test ./internal/saas/api ./internal/saas/edge ./internal/saas/telemetry \
  ./internal/saas/platformprobe ./cmd/agent-memory-platform-probe \
  ./internal/saas/platformrollback ./internal/contracts -count=1
make -n saas-platform-probe \
  STAGING_RELEASE=/tmp/release.json \
  STAGING_EDGE_URL=https://staging.example \
  STAGING_INTERNAL_URL=http://127.0.0.1:18080 \
  STAGING_PROBE_RECEIPT=/tmp/probe.json
make saas-kubernetes-check
```

Then run the repository-wide Go tests, vet, action lint, and `git diff --check`.
Keep P1.21 implementation acceptance separate from the open CP1-A external
control.
