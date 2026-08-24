# Production Private-Authority Exposure Receipt

Use this receipt only after a real self-managed production infrastructure
change has a ready apply-and-drift receipt. It binds the exact inventory and
change receipts to two private artifacts: an export of the effective firewall
or network-policy state and the raw output of a reachability scan performed
from outside the protected administrative boundary.

Start from the production examples. Replace every placeholder, then recompute
each downstream receipt digest after changing an upstream file. Retain the raw
firewall export and scanner output in the private external evidence store and
record only their SHA-256 values. Do not put addresses, ports, scan origins,
commands, paths, accounts, resource names, topology, credentials, personal
data, customer data, or raw output in the sanitized receipt.

Record exactly one outcome for each fixed authority:

- PostgreSQL;
- object storage;
- durable queue;
- secrets;
- observability;
- backup; and
- Kubernetes control.

`blocked` means the external measurement could not reach the authority under
the approved scan method. `reachable` means exposure was detected.
`inconclusive` means the measurement could not establish either condition.
Reachable and inconclusive receipts are retained as valid evidence but are not
ready. There is deliberately no reviewer, exception, independence, ownership,
or approval flag in this receipt.

Validate the complete production chain:

```sh
make saas-platform-exposure-check \
  PLATFORM_INVENTORY=/absolute/path/to/production-inventory.json \
  PLATFORM_PLAN=/absolute/path/to/production-plan.json \
  PLATFORM_CHANGE=/absolute/path/to/production-change.json \
  PLATFORM_EXPOSURE=/absolute/path/to/production-exposure.json
```

Exit `0` requires all seven results to be blocked. Exit `3` means the receipt
is structurally valid but has a reachable or inconclusive result. Exit `2`
means CLI arguments are invalid. Exit `1` means the upstream chain, receipt
contract, binding, or aggregate report failed.

The report contains only environment and aggregate target counts. A passing
example proves validator behavior only. P1.4-C remains open until the hashes
bind real production artifacts, the scan was actually performed independently,
its method and results were reviewed, and the resulting dossier has an
authorized external signature.
