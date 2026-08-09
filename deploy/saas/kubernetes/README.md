# Hosted Kubernetes deployment

These manifests define the provider-neutral workload boundary. Managed
PostgreSQL, object storage, queue, identity, secret synchronization, ingress,
and private networking are intentionally supplied by the selected cloud
environment rather than simulated here.

The `staging` and `production` overlays use distinct namespaces and contain no
secret values. Before a release, the platform must create these scoped secrets:

| Secret | Required consumers | Contract |
|---|---|---|
| `agent-memory-api-secrets` | API | PostgreSQL URL, object credentials/endpoint, export encryption key, secret reference, edge-country HMAC secret; production model endpoint/key; OIDC values may override the ConfigMap |
| `agent-memory-worker-secrets` | Worker | PostgreSQL URL, object credentials/endpoints, queue URL, encryption keys, secret reference; production model endpoint/key |
| `agent-memory-reconciler-secrets` | Reconciler | PostgreSQL URL, object credentials/endpoint, and secret reference |
| `agent-memory-migration-secrets` | Migration | PostgreSQL URL only |

External secret synchronization and workload-identity bindings must grant each
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
