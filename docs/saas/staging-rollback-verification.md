# Staging Rollback Verification

Use this workflow after an authorized rollback drill against the real
self-managed staging cluster. The verification command is read-only: it does
not deploy an image, inject a fault, initiate rollback, or approve a maintenance
window.

## Required inputs

Retain two unchanged receipts from `scripts/saas-kubernetes-release.sh staging`:

1. a passed baseline receipt for the last known-good release; and
2. a later failed release receipt that records migration complete, rollout
   failed, and rollback attempted and succeeded.

The failed attempt must use at least one different API, worker, or reconciler
image. Both receipts must identify the same staging namespace and Kubernetes
context. The validator computes their SHA-256 values from the exact bounded
files; copying an opaque release ID is not sufficient.

Selecting and deploying a deliberately unhealthy but non-destructive workload
image requires the staging change authority, a maintenance window, an agreed
abort path, and operators able to recover manually. Do not conduct the fault
portion against production and do not infer authorization from this runbook.

## Verify live restoration

After the failed release command finishes its automatic rollback, run:

```sh
make saas-platform-rollback-verify \
  ROLLBACK_BASELINE=/absolute/path/to/passed-staging-release.json \
  ROLLBACK_FAILED_ATTEMPT=/absolute/path/to/failed-staging-release.json \
  ROLLBACK_RECEIPT=/absolute/new/path/staging-rollback-verification.json
```

The command queries only:

- the current Kubernetes context; and
- the fixed API, worker, and reconciler Deployment image, revision, desired
  replicas, and ready replicas in `agent-memory-staging`.

It never reads Secrets, ConfigMaps, pods, logs, events, environment variables,
tokens, raw resources, or application payloads. Images are compared in memory
with the passed baseline and are not copied into the new receipt.

Each Deployment receives `restored`, `image_mismatch`, `not_ready`, or
`unavailable`. Exit `0` requires all three live images to match the baseline
and every desired replica to be ready. Exit `3` writes valid but unready
evidence. Exit `2` means arguments are invalid. Exit `1` means an input,
collection, binding, publication, or report operation failed.

The receipt is atomically published at a new mode-`0600` path and binds both
input receipt hashes. Store all three receipts, the authorized fault plan,
change record, alert/audit evidence, and accountable review in the immutable
external evidence system. A mocked collector or ready local receipt does not
close P1.2, CP1-B, or Checkpoint 1. For CP1-B, reload and combine the resulting
receipt with the managed-secret and human operator exercises using the
[staging operational-safety drill workflow](staging-operational-safety-drills.md).
