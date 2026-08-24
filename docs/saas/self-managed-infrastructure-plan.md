# Self-Managed Infrastructure Plan Receipt

Use this receipt before applying infrastructure changes to a real staging or
production installation. It is provider-neutral and works with internally
selected tools such as OpenTofu, Terraform, Pulumi, Ansible, Helm, Kustomize,
or a controlled custom toolchain. Agent Memory does not require an external
cloud provider.

Start from `self-managed-infrastructure-plan.example.json`. Bind it to the
matching validated platform inventory and replace the example source revision,
inventory-receipt SHA-256, source-bundle SHA-256, and raw-plan SHA-256. The
inventory validator computes the digest from the exact bytes it validates.
Hash the exact private IaC source bundle and raw plan retained for review; do
not copy either artifact into this sanitized receipt.

The receipt declares exactly 21 capabilities:

- edge, application, and data network tiers;
- Kubernetes, OIDC, and PostgreSQL boundaries;
- quarantine, immutable-vault, and export buckets;
- the durable queue;
- API, worker, reconciler, and migration workload identities;
- four matching service-secret contracts;
- telemetry; and
- PostgreSQL and object-storage backup paths.

Every capability references only failure-domain IDs already present in the
inventory. Production requires at least two domains per capability. Only edge
ingress and OIDC may declare public ingress. Do not include commands, file
paths, backend configuration, resource names, hostnames, URLs, IP addresses,
credentials, key identifiers, personal names, customer identifiers, or raw
plan values.

Validate the inventory and receipt together:

```sh
make saas-platform-plan-check \
  PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
  PLATFORM_PLAN=/absolute/path/to/sanitized-infrastructure-plan.json
```

Exit `0` means the receipt is structurally complete and contains only create,
update, or no-change actions. Exit `3` means the receipt is valid but includes
a replacement or deletion and therefore needs a separately reviewed migration
or retirement plan. Exit `2` means CLI arguments are invalid. Exit `1` means an
inventory or receipt contract failed or the report could not be emitted.

The CLI prints only environment, readiness, capability/tool counts, and action
totals. A passing report does not execute a tool, approve a plan, prove a
successful apply, or prove the declared failure domains. Retain the unchanged
receipt with the hashed private source bundle and raw plan, real apply output,
post-apply drift result, platform preflight receipt, independent reachability
review, and authorized P1.4-B approval in the external evidence store.

After the real apply, continue with the
[infrastructure apply and drift receipt](self-managed-infrastructure-change.md)
to bind the raw apply, installed-resource inventory, and drift artifacts back
to this exact plan.
