# Kubernetes Platform Preflight

Use this command after validating the self-managed platform inventory and after
deploying an immutable release. It produces a content-free P1.4 supporting
receipt from the current Kubernetes context.

```sh
make saas-platform-preflight \
  PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
  PLATFORM_ENVIRONMENT=staging \
  PLATFORM_PREFLIGHT_RECEIPT=/absolute/new/path/staging-preflight.json
```

The collector targets only `agent-memory-staging` or
`agent-memory-production`. The inventory environment must match. It verifies:

- namespace existence;
- the API, worker, reconciler, and migration service accounts;
- existence by name of the four service-specific secret contracts;
- default-deny, edge-ingress, internal-service/DNS, and observability policies;
- a ClusterIP-only API Service;
- API, worker, and reconciler service-account bindings;
- digest-pinned workload images; and
- desired replicas equal ready replicas.

The persisted receipt includes both the opaque inventory ID and the SHA-256 of
the exact validated inventory bytes. Strict reload requires all eight checks in
canonical order and recomputes readiness, preventing a same-ID replacement
inventory or edited check set from being silently paired with an old receipt.

The collector never requests Secret representations or values, ConfigMap
values, environment variables, logs, events, pods, application responses, or
raw resource JSON/YAML. Kubernetes stderr and raw query output are not copied
into the receipt or CLI report.

The receipt is atomically created with mode `0600` and never overwrites an
existing path or symlink. Exit `0` means all fixed checks passed. Exit `3` means
collection completed and a failed receipt was written. Exit `2` is invalid CLI
configuration. Exit `1` is malformed inventory, collector failure, or receipt
publication failure.

Move the unchanged receipt and its exact validated inventory into the immutable external
evidence dossier. Add the independent network reachability scan, infrastructure
drift report, exact installation inventory, and accountable approvals. A local
or mocked receipt proves collector behavior only and does not close P1.4.
