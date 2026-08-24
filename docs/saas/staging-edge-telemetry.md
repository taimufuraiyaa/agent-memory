# Staging edge-to-telemetry evidence

This workflow collects a content-free receipt for one fixed readiness request
through a real staging edge, API, and API telemetry middleware. It requires an
existing **passed** staging Kubernetes release receipt. It does not deploy a
release, change cluster state, read customer data, or close Checkpoint 1 by
itself.

## Trust boundary

The public edge accepts only the fixed `/_edge/health/ready` challenge path for
this workflow and echoes generated request and trace IDs. It rejects `/internal`
and `/metrics`. The API keeps at most 1,024 exact content-free observations for
ten minutes and exposes them only at the fixed network-internal
`/internal/evidence/requests/{request_id}` route.

Run the collector from the observability network allowed to reach the API, or
use an authorized loopback port-forward to the API service. The edge base URL
must use HTTPS. The internal base URL must use HTTPS, except that HTTP is
accepted for loopback, the `agent-memory-api` service, or a Kubernetes
`.svc`/`.svc.cluster.local` name. Neither URL may contain a path, query,
fragment, or user information. Redirects are never followed and response bodies
are bounded and discarded.

## Collect

Choose a destination that does not exist. Keep the release receipt and generated
probe receipt outside Git and the application database.

```sh
make saas-platform-probe \
  STAGING_RELEASE=/absolute/evidence/staging-release.json \
  STAGING_EDGE_URL=https://memory.staging.internal \
  STAGING_INTERNAL_URL=http://127.0.0.1:18080 \
  STAGING_PROBE_RECEIPT=/absolute/evidence/staging-edge-telemetry.json
```

The command creates a mode-`0600` receipt conforming to
`api/evidence/v1/staging-edge-telemetry-receipt.schema.json`. It binds the exact
release-receipt SHA-256, generated request and trace IDs, a maximum two-minute
window, and three fixed outcomes: edge response, API correlation, and telemetry
observation. CLI output contains aggregate counts only.

Exit code `0` means all checks passed. Exit code `3` means a valid receipt was
written but at least one check failed. Exit code `1` is an operational or input
artifact failure; exit code `2` is invalid command usage.

## Retain and review

Move the receipt into the immutable external evidence store. Separately export
the matching trace from the staging telemetry system and retain it in the same
dossier, then obtain the accountable operations approval using the signed
release-approval workflow. The receipt deliberately contains no URL, response
body, tenant, actor, credential, authorization value, raw log, or raw span.

Unit tests, mocked servers, local Compose, and the repository collector prove
the implementation boundary only. CP1-A remains open until the real staging
release, raw trace export, immutable dossier, and authorized approval exist.
