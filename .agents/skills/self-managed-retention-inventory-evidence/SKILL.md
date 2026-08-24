---
name: self-managed-retention-inventory-evidence
description: Validate or extend the Agent Memory CP4-B installed retention-policy inventory boundary. Use when changing retention data classes, purposes, TTLs, policy migrations, privacy disclosure, the retention inventory collector/CLI/schema/runbook, or evidence-matrix support.
---
# Self-managed retention inventory evidence

## Boundary

This workflow proves repository support for collecting the policies installed
on a self-managed platform. It never closes CP4-B from local evidence. CP4-B
still requires a real staging or production receipt, immutable private database
proof, and current signed Privacy approval.

The collector may read only `saas_retention_policies`. Never accept PostgreSQL
credentials as flags or emit connection strings, endpoints, database identity,
tenant/customer identity, paths, SQL, row contents, logs, or customer content.

## Inspect before editing

Read, in order:

1. `.kiro/specs/saas-product-platform/requirements.md` R33.
2. The retention-inventory section of `design.md`.
3. `tasks.md` P7.1, P7.5, and P7.6.
4. `internal/saas/retention/registry.go` and latest retention migration.
5. `internal/saas/retentioninventory/` and its CLI.
6. The JSON schema, operator runbook, external-evidence matrix, and privacy API/UI.

Run `agent-memory search` first and immediately score the result as required by
the repository rules.

## Invariants

- The active inventory is exactly the twelve canonical `retention.DataClasses`:
  no missing, duplicate, or unknown class.
- Purpose, version, owner, trigger, deletion method, hold behavior, migration
  plan, and customer impact are trimmed, non-empty, and bounded.
- TTL is non-negative whole seconds; effective time is non-zero and not future.
- Listing and receipt policy order are deterministic by `data_class`.
- The canonical policy JSON digest covers the exact normalized policy array.
- Collection loads and validates inventory → plan → applied-change bytes and
  requires a ready, drift-clean change before reading PostgreSQL.
- The collector is read-only and never applies migrations.
- Receipt publication is atomic, create-only, non-symlink, and mode `0600`.
- CLI output contains only readiness, publication state, and policy count.
- Local/disposable evidence is implementation proof only and leaves CP4-B open.

## TDD sequence

1. Add failing pure coverage tests for every new invariant.
2. Add a failing migration contract test for schema and rollback behavior.
3. Add/update the purpose field in the database, registry, privacy DTO/query,
   OpenAPI, and dashboard disclosure as one source-of-truth change.
4. Add failing collector tests for canonical order, platform binding, causal
   time, content-free output, create-only publication, and mode `0600`.
5. Update the strict JSON schema and contract tests. Search schema property
   names for forbidden secret/content-bearing fields.
6. Update CLI, Make target, runbook, matrix support, and spec checkboxes only
   after tests pass.

## Disposable PostgreSQL proof

Use a fresh `pgvector/pgvector:pg17` container on an unused loopback port. Do
not reuse the persistent local product database. Set only
`AGENT_MEMORY_TEST_POSTGRES_URL`, run the integration test below, and stop the
container afterward:

```sh
go test -count=1 -run TestIntegrationInstalledPoliciesBuildSchemaBoundReceipt \
  -v ./internal/saas/retentioninventory
```

Confirm the migration ledger count, exactly twelve active policies with
non-empty trimmed purpose, and zero incomplete purpose/trigger/TTL rows. A
future-dated platform-change fixture must be rejected; generate a correctly
rebound fresh chain for an active CLI proof rather than weakening causal time.

## Gates

Run at minimum:

```sh
go test ./internal/saas/retention ./internal/saas/retentioninventory \
  ./cmd/agent-memory-retention-inventory ./internal/saas/postgres \
  ./internal/saas/privacy ./internal/saas/dashboard ./internal/contracts
go test ./...
go vet ./...
make contracts-check
```

Then run the repository schema validators, external-evidence catalog verifier,
and any full local-alpha gate required by the surrounding change. Reconcile the
task numerator/denominator independently from the unchanged external-control
catalog; repository support must not reduce the external open-control count.
