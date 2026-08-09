---
name: saas-minio-least-privilege
description: Verify or restore service-specific MinIO capabilities for the hosted source quarantine, immutable vault, and export buckets. Use when changing object-store credentials, policies, constructors, source promotion, export storage, reconciliation, or the SaaS Compose topology.
---

# SaaS MinIO Least Privilege

## Invariants

- Application services never receive the MinIO root credential and never create buckets or IAM users.
- `object-init` owns bucket, user, and policy provisioning.
- API may put `agent-memory-quarantine/quarantine/*` and get `agent-memory-exports/exports/*` only.
- Worker may get/delete quarantine objects and get/put/delete vault and export objects.
- Reconciler may list `agent-memory-vault` under `vault/*` but may not read object content.
- Vault keys follow `vault/<tenant>/<source>/<version>.aesgcm`.
- `PutVault` rejects an existing key before writing so retries cannot overwrite an immutable version.

Policy sources live under `deploy/saas/minio/policies/`. Service credentials and the provisioning job live in `deploy/saas/compose.yaml`.

## Verification workflow

1. Validate the Compose file:

   `docker compose -f deploy/saas/compose.yaml config --quiet`

2. Recreate provisioning and affected services:

   `docker compose -f deploy/saas/compose.yaml up -d --force-recreate object-init api worker reconciler`

3. Run positive and negative capability checks:

   `make saas-object-policy-test`

   This must prove API quarantine upload, worker quarantine read, worker vault/export writes, reconciler vault listing, and API export reads. It must also prove API vault reads, reconciler content reads, and API export writes are denied.

4. Run the immutable object integration test:

   `AGENT_MEMORY_TEST_OBJECT_ENDPOINT=http://127.0.0.1:59000 AGENT_MEMORY_TEST_OBJECT_ACCESS_KEY=agent-memory-worker AGENT_MEMORY_TEST_OBJECT_SECRET_KEY=worker-local-development-only go test -run TestVaultPromotionCannotOverwriteImmutableVersionObject -count=1 ./internal/saas/source`

5. Run a real upload through API → quarantine → worker → encrypted vault → extraction and confirm the source reaches `indexing` or `ready` with parser, normalization, encryption, and publication provenance.

6. Finish with `make saas-integration-test`, targeted race tests, vet, and `git diff --check`.

## Failure diagnosis

- Service fails during object-store construction: confirm constructors do not call `BucketExists` or `MakeBucket`; those are provisioning responsibilities.
- API upload is denied: confirm the object key begins with `quarantine/` and API policy includes `s3:PutObject` for that prefix.
- Worker cannot promote: confirm quarantine `GetObject/DeleteObject` and vault `GetObject/PutObject/DeleteObject` actions.
- Reconciler cannot list: confirm `s3:ListBucket` targets the bucket ARN, not an object ARN, and its prefix condition includes `vault/*`.
- Overwrite test fails: keep IAM write permission for new objects, but enforce immutable version identity in `PutVault` with `StatObject` plus `ErrVaultObjectExists`.

Never broaden a policy merely to make service startup succeed. Identify the exact operation and keep startup free of administrative bucket probes.
