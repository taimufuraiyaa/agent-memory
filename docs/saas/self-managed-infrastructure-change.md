# Self-Managed Infrastructure Apply and Drift Receipt

Use this sanitized receipt after running a real infrastructure apply. It binds
the exact validated inventory and infrastructure-plan receipts to the raw apply,
installed-resource inventory, and post-apply drift artifacts retained in the
private external evidence store. Agent Memory does not execute the selected IaC
tool or require an external cloud provider.

Start from `self-managed-infrastructure-change.example.json`. Compute the
inventory and plan receipt SHA-256 values from the exact files accepted by their
validators. Hash the exact raw apply output. After a successful apply, export
and hash the installed-resource inventory, record only its bounded resource
count, then run and hash the drift check. Never copy raw artifacts, commands,
paths, resource names, backend settings, topology, endpoints, addresses,
credentials, personal data, or customer data into the sanitized receipt.

Every receipt carries one fixed result for each of the 21 plan capabilities:

- `no_change` maps to `unchanged`;
- successful `create` or `update` maps to `applied`;
- a failed changed capability maps to `failed`; or
- when rollback succeeds, a failed changed capability maps to `rolled_back`.

A successful apply requires rollback `not_required`, a collected resource
inventory, and a completed drift check. A failed apply uses rollback
`not_attempted`, `succeeded`, or `failed` and must not claim a resource inventory
or drift run. Drift detection and drift-check failure are valid evidence states
but remain unready.

Validate the complete chain:

```sh
make saas-platform-change-check \
  PLATFORM_INVENTORY=/absolute/path/to/platform-inventory.json \
  PLATFORM_PLAN=/absolute/path/to/sanitized-infrastructure-plan.json \
  PLATFORM_CHANGE=/absolute/path/to/sanitized-infrastructure-change.json
```

Exit `0` requires successful apply, no rollback, collected inventory, clean
drift, valid timestamp ordering, and 21 plan-consistent capability results.
Exit `3` means the receipt is structurally valid but records a failed apply or
non-clean drift state. Exit `2` means CLI arguments are invalid. Exit `1` means
an input contract or binding failed or the aggregate report could not be
emitted.

The CLI prints only the environment, coarse apply/rollback/inventory/drift
outcomes, and capability/resource counts. A passing example or report does not
prove that an apply occurred or that the private artifacts are genuine. Retain
the unchanged chain with the raw artifacts, platform preflight, independent
reachability review, post-apply reviewer decision, and authorized P1.4-B
signature in the external evidence store.

For production, continue the evidence chain with the
[private-authority exposure receipt](production-private-authority-exposure.md).
