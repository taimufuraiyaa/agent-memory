---
name: saas-kubernetes-release-gate
description: Validate and operate the provider-neutral Agent Memory Kubernetes release boundary. Use when changing hosted manifests, service identities, runtime secret contracts, image references, migration ordering, staging deployment, rollback behavior, content-free release receipts, or the hosted release workflow.
---

# SaaS Kubernetes Release Gate

## Invariants

- Only `agent-memory-staging` and `agent-memory-production` are valid release
  namespaces.
- API, worker, reconciler, and migration use separate service accounts and
  secrets. Migration receives PostgreSQL only.
- Repository manifests contain no `Secret`, `data`, or `stringData` resources.
  Secret synchronization and workload identity belong to provider-specific
  infrastructure.
- Workload images must be exact `image@sha256:<64 hex>` references. Tags,
  including `latest`, never reach a cluster.
- Default-deny ingress and egress, non-root execution, dropped capabilities,
  read-only root filesystems, resource limits, health probes, and bounded
  writable `/tmp` volumes remain present.
- A backward-compatible migration completes before Deployments are applied.
- A failed API, worker, or reconciler rollout invokes `kubectl rollout undo`
  for all three workloads and leaves feature flags unchanged.
- When `AGENT_MEMORY_RELEASE_RECEIPT_PATH` is set, publish exactly one new
  mode-`0600` JSON receipt conforming to
  `api/evidence/v1/kubernetes-release-receipt.schema.json`. Record only context,
  namespace, release/images, timestamps, coarse outcomes, revisions, and
  rollback booleans. Never retrieve Secret representations, pod logs,
  environments, tokens, or payloads for evidence.
- A failed receipt stays failed after a successful rollback. Receipt validation
  or atomic publication failure fails the release and triggers rollback once
  workloads have been applied.
- Post-rollback verification is read-only. It binds exact passed and failed
  staging release receipt hashes and queries only fixed Deployment images,
  revisions, desired replicas, and ready replicas. It never initiates rollback
  or treats the failed receipt's rollback boolean as proof of restoration.
- Static or mocked tests do not prove a real staging deployment, secret
  rotation, private managed-data ingress, or rollback drill. Keep those
  acceptance items open until immutable external evidence exists.

## Verification workflow

1. Render and enforce both overlay policies:

   `make saas-kubernetes-check`

2. Prove migration-before-rollout and failure-triggered rollback with the
   mocked Kubernetes contract:

   `make saas-release-script-test`

   Prove strict receipt-pair binding and bounded post-rollback collection:

   `go test ./internal/saas/platformrollback ./cmd/agent-memory-platform-rollback ./internal/contracts -count=1`

3. Validate workflow expressions and shell syntax:

   `actionlint`

   `bash -n scripts/validate-saas-kubernetes.sh scripts/saas-kubernetes-release.sh scripts/tests/saas-kubernetes-release_test.sh`

4. Run the hosted configuration and composition tests:

   `go test ./internal/saas/config ./internal/saas/auth ./internal/saas/modelgateway ./cmd/agent-memory-api`

5. Finish with:

   `git diff --check`

## Real release

Supply four immutable references and a release ID, configure `KUBECONFIG` for
the exact target environment, choose a new receipt path, and invoke:

```bash
export AGENT_MEMORY_RELEASE_RECEIPT_PATH=/release-evidence/staging-release.json
scripts/saas-kubernetes-release.sh staging
```

The target namespace must already contain the four service-specific secrets
documented in `deploy/saas/kubernetes/README.md`. Capture the tagged release,
resolved digests, migration Job result, Deployment revision/status, health
checks, and rollback result in the external evidence store. Hash the receipt
after upload and reference that digest from the accountable approval; do not
edit or reuse the local path.

After an authorized failed-rollout drill, validate live restoration with
`make saas-platform-rollback-verify` as documented in
`docs/saas/staging-rollback-verification.md`. Retain the passed baseline,
failed attempt, and verification receipts together. A mocked ready receipt does
not satisfy P1.2 or Checkpoint 1.

The GitHub `hosted release` workflow performs the same staging sequence for a
`v*` tag after publishing and attesting all four images. It requires the
protected `staging` environment and `KUBE_CONFIG_STAGING_B64` secret.
It configures the receipt path and uploads the passed or failed artifact with
`if: always()`; a missing artifact fails the upload step.

## Failure diagnosis

- Mutable-image rejection: resolve the registry digest; do not relax the
  pattern or substitute a tag.
- Missing secret: provision the exact service contract through the selected
  secret manager; do not copy another service's secret.
- Migration failure: stop before Deployments, inspect content-free Job logs and
  database connectivity, then correct the backward-compatible migration.
- Readiness failure: preserve the failed ReplicaSet evidence and confirm the
  automatic undo completed before retrying.
- Receipt failure: preserve the nonzero release result. Do not relax schema,
  overwrite/symlink protections, immutable-image validation, or content
  exclusions; correct the destination or metadata query and issue a new path.
- Network timeout: determine the exact DNS, HTTPS, PostgreSQL, or NATS
  destination and patch the environment-specific egress policy narrowly.
- Production startup rejects configuration: verify HTTPS OIDC/model endpoints,
  managed secret reference, TLS PostgreSQL, and the remote model route. Never
  enable development identity or the local embedding scaffold in production.
