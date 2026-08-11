---
name: self-managed-platform-evidence
description: Validate or extend the Agent Memory self-managed inventory, infrastructure-plan, and Kubernetes preflight evidence workflow. Use when changing P0.2/P1.4 platform contracts, schemas, validators, operator commands, or evidence boundaries.
---

# Self-Managed Platform Evidence

## Purpose

Preserve the provider-neutral evidence chain for internally operated Agent
Memory staging and production environments. The repository validates bounded,
content-free artifacts; it never self-certifies a real installation, apply,
failure domain, review, or approval.

## Evidence chain

1. Validate the installation inventory:

   ```sh
   make saas-platform-inventory-check \
     PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json
   ```

2. Validate the sanitized infrastructure plan receipt against that inventory:

   ```sh
   make saas-platform-plan-check \
     PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
     PLATFORM_PLAN=/absolute/path/to/sanitized-plan.json
   ```

3. After a real apply and drift check, validate the sanitized change receipt:

   ```sh
   make saas-platform-change-check \
     PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
     PLATFORM_PLAN=/absolute/path/to/sanitized-plan.json \
     PLATFORM_CHANGE=/absolute/path/to/sanitized-change.json
   ```

4. For production, bind the private firewall export and independent external
   reachability scan to the exact ready change chain:

   ```sh
   make saas-platform-exposure-check \
     PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
     PLATFORM_PLAN=/absolute/path/to/sanitized-plan.json \
     PLATFORM_CHANGE=/absolute/path/to/sanitized-change.json \
     PLATFORM_EXPOSURE=/absolute/path/to/sanitized-exposure.json
   ```

5. After an immutable workload release, collect live Kubernetes
   state into a new private receipt:

   ```sh
   make saas-platform-preflight \
     PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
     PLATFORM_ENVIRONMENT=staging \
     PLATFORM_PREFLIGHT_RECEIPT=/absolute/new/path/preflight.json
   ```

6. Retain unchanged receipts beside the private IaC source bundle, raw plan,
   apply output, drift output, reachability evidence, and authorized approvals
   in the external evidence store. Bind private artifacts by SHA-256; do not
   copy them into sanitized receipts or Git.

## Invariants

- Core infrastructure is self-managed. Do not introduce AWS, Azure, GCP, or an
  equivalent external cloud as a required deployment dependency.
- Only `staging` and `production` are valid environments. Inventory and target
  environment must match.
- Inventory requires Kubernetes, identity, PostgreSQL, object storage, queue,
  secrets, observability, and backup plus explicit payment/email/model states.
- Production inventory and plan capabilities span at least two declared failure
  domains. A label is a claim, not proof of physical independence.
- Infrastructure plans bind the inventory, source revision, source-bundle
  receipt SHA-256, source revision, source-bundle SHA-256, and raw-plan SHA-256
  and contain exactly the 21 capability IDs in `internal/saas/platformplan`.
- Infrastructure change receipts bind exact inventory and plan receipt digests,
  raw apply output, resource inventory, and drift artifacts. Only successful
  apply, no rollback, collected inventory, clean drift, and plan-consistent
  capability results are ready.
- Production exposure receipts bind exact inventory and ready change digests,
  hash the firewall export and raw external scan, and cover exactly seven fixed
  private authority classes. Reachable or inconclusive results are unready.
- Only edge ingress and OIDC may declare public ingress. PostgreSQL, buckets,
  queue, secrets, telemetry, backups, Kubernetes, application/data networks,
  and workload identities remain private.
- Replacement and deletion actions are structurally valid but unready. Never
  add a self-asserted approval boolean to bypass this outcome.
- Preflight queries only fixed names, Service type, service-account binding,
  immutable image references, and replica counts. Never retrieve Secret
  representations or values, ConfigMaps, environments, logs, events, pods,
  application responses, raw YAML/JSON, or customer data.
- Preflight receipts bind the exact validated inventory bytes by SHA-256 and
  reload with the complete canonical check order and derived readiness; an
  opaque inventory ID alone is not a sufficient persisted binding.
- Reports expose only bounded aggregate counts/fixed check outcomes. Do not
  print paths, revisions, hashes, topology, endpoints, addresses, credentials,
  owner personal data, customer identifiers, or raw tool output.
- Input files are bounded regular non-symlink files with strict JSON and unknown
  fields rejected. Preflight output is atomic, create-only, and mode `0600`.
- Examples, tests, mocked collectors, and disposable clusters are repository
  support only. Keep P0.2/P1.4 controls open until real externally retained
  artifacts and authorized signatures exist.

## Change workflow

1. Read `.kiro/specs/saas-product-platform/requirements.md`, `design.md`, then
   `tasks.md`; update all three before a non-trivial contract change.
2. Write a failing test before changing validator or CLI behavior.
3. Keep input schemas under `api/evidence/v1/`, examples/runbooks under
   `docs/saas/`, Go validators under `internal/saas/`, and commands under `cmd/`.
4. Update `docs/saas/external-evidence-matrix.md` with repository support and
   the exact evidence still missing. Do not close the external control.
5. Check the canonical commands and content-free output manually.

## Verification

Run focused checks:

```sh
go test ./internal/saas/platforminventory ./cmd/agent-memory-platform-inventory \
  ./internal/saas/platformplan ./cmd/agent-memory-platform-plan \
  ./internal/saas/platformchange ./cmd/agent-memory-platform-change \
  ./internal/saas/platformexposure ./cmd/agent-memory-platform-exposure \
  ./internal/saas/platformpreflight ./cmd/agent-memory-platform-preflight \
  ./internal/contracts -count=1
```

Run release gates:

```sh
make saas-kubernetes-check
make saas-release-script-test
bash -n scripts/validate-saas-kubernetes.sh \
  scripts/saas-kubernetes-release.sh \
  scripts/tests/saas-kubernetes-release_test.sh
actionlint
```

Finish with:

```sh
go test ./... -count=1
go vet ./...
git diff --check
```

If a disposable cluster is used, report its classification as local rehearsal,
capture no customer content, and delete the cluster after verification.
