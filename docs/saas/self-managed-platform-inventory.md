# Self-Managed Platform Inventory

The inventory is a content-free, machine-verifiable summary of one real staging
or production installation. It supports P0.2-A and P1.4-A evidence collection;
it does not prove that the declared topology exists or that its failure domains
are physically independent.

Start from `self-managed-platform-inventory.example.json`. Replace every
placeholder with bounded opaque identifiers and actual component versions. Do
not include hostnames, URLs, IP addresses, credentials, key identifiers,
personal names, customer identifiers, source names, object keys, or application
content.

Production must declare at least two failure domains and place every required
component in at least two declared domains. Only the identity service may
declare public ingress. The inventory always includes payment, email, and model
integration states so a disabled integration is explicit rather than omitted.

Validate it with:

```sh
make saas-platform-inventory-check \
  PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json
```

Exit `0` emits a content-free JSON report containing only the environment,
component count, failure-domain count, and enabled external-integration kinds.
Exit `2` means the CLI configuration is invalid. Exit `1` means the inventory
file or its claims are malformed.

Store the real inventory outside Git and the application database. Add it to
the applicable immutable P0.2-A/P1.4-A dossier with the infrastructure-as-code
plan, independent reachability scan, facility/failure-domain review, drift
report, and signed accountable-owner approval. A passing example or CI run is
repository support only.
