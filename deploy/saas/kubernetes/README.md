# Hosted Kubernetes deployment

These manifests define the self-managed workload boundary. PostgreSQL, object
storage, queue, identity, secret synchronization, ingress, private networking,
backup, and recovery are supplied by the installation's internally operated
platform layer. They are not external cloud-provider dependencies and are not
simulated by these application workload manifests.

The platform contract and minimum production shape are defined in
`docs/saas/self-managed-platform-adr.md`. Component failover, replacement,
export, and restore expectations are defined in
`docs/saas/component-recovery-and-exit.md`.

Before collecting environment evidence, create and validate the content-free
inventory described in `docs/saas/self-managed-platform-inventory.md`. Keep the
real inventory outside Git and the application database.

Before applying platform changes, validate the sanitized IaC plan receipt
described in `docs/saas/self-managed-infrastructure-plan.md`. The receipt binds
private source and raw-plan artifacts by digest without embedding topology or
credentials; it does not replace real apply, drift, or review evidence.

After the real apply and drift check, validate their sanitized binding with
`docs/saas/self-managed-infrastructure-change.md`. Keep the raw apply, installed
resource inventory, and drift artifacts outside Git and the application
database.

After an immutable deployment is healthy, collect the bounded cluster-state
receipt described in `docs/saas/kubernetes-platform-preflight.md`. The collector
checks Secret names only and never retrieves Secret representations or values.

The `staging` and `production` overlays use distinct namespaces and contain no
secret values. Before a release, the platform must create these scoped secrets:

| Secret | Required consumers | Contract |
|---|---|---|
| `agent-memory-api-secrets` | API | PostgreSQL URL, object credentials/endpoint, export encryption key, secret reference, edge-country HMAC secret; production model endpoint/key; OIDC values may override the ConfigMap |
| `agent-memory-worker-secrets` | Worker | PostgreSQL URL, object credentials/endpoints, queue URL, encryption keys, secret reference; production model endpoint/key |
| `agent-memory-reconciler-secrets` | Reconciler | PostgreSQL URL, object credentials/endpoint, and secret reference |
| `agent-memory-migration-secrets` | Migration | PostgreSQL URL only |

Internal secret synchronization and workload-identity bindings must grant each
service only its contract. Never commit `Secret`, `data`, or `stringData`
objects. Replace the example OIDC issuer and audience during environment
provisioning.

Validate both overlays with `scripts/validate-saas-kubernetes.sh`. Deploy an
immutable release with `scripts/saas-kubernetes-release.sh staging`; it runs the
migration to completion before updating workloads and automatically invokes
Kubernetes rollback when a rollout fails.

For a real evidence-bearing release, provide a new output path:

```sh
export AGENT_MEMORY_RELEASE_RECEIPT_PATH=/release-evidence/staging-v1.2.3.json
scripts/saas-kubernetes-release.sh staging
```

The command atomically creates a mode-`0600` receipt conforming to
`api/evidence/v1/kubernetes-release-receipt.schema.json`. Successful receipts
bind the Kubernetes context, namespace, release ID, four image digests,
migration completion, healthy rollout state, and three observed Deployment
revisions. Failed releases record coarse failure and rollback state without
including Secret values, resource YAML, logs, environment variables, tokens,
or application data. The destination must not already exist or be a symlink.
Move the receipt into the immutable external evidence system and reference its
SHA-256 from the applicable signed approval.

For an authorized real staging rollback drill, retain both the passed baseline
and failed rollback-succeeded release receipts, then use the read-only
[staging rollback verification workflow](../../../docs/saas/staging-rollback-verification.md)
to prove that live workload images and ready replicas returned to the exact
baseline. The verifier does not inject a fault or initiate rollback.

After a passed staging release, use the
[staging edge-to-telemetry workflow](../../../docs/saas/staging-edge-telemetry.md)
from the observability network (or an authorized API port-forward) to bind one
fixed content-free readiness challenge to the exact passed release receipt.
The real exported trace and signed operations review remain external evidence.
