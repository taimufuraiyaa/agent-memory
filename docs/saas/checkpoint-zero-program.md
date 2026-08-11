# Checkpoint-zero program approval evidence

This boundary turns private architecture, blocker, cost, beta-cap, staffing, and
accountable-review artifacts into one content-free CP0 receipt. It supports the
CP0-A and CP0-B decisions but does not sign or approve either decision.

## Required chain

Use one valid self-managed staging or production inventory, one ready P0.1
launch-scope receipt, and one ready P0.2-C external-integration receipt. The
collector reloads and revalidates both receipts and binds the SHA-256 of their
exact bytes. Edited, unready, substituted, symlinked, or inventory-mismatched
receipts fail closed.

Prepare the input from
[`checkpoint-zero-program.example.json`](checkpoint-zero-program.example.json).
Replace every example identifier, timestamp, count, and digest with the real
private artifact metadata. Digests are identifiers only; never put source
content, personal names, contact details, credentials, file paths, or provider
endpoints in this input.

Run:

```sh
make saas-program-approval-check \
  PLATFORM_INVENTORY=/secure/platform-inventory.json \
  LAUNCH_SCOPE_RECEIPT=/secure/launch-scope-receipt.json \
  EXTERNAL_INTEGRATION_RECEIPT=/secure/integration-receipt.json \
  PROGRAM_APPROVAL_INPUT=/secure/cp0-input.json \
  PROGRAM_APPROVAL_RECEIPT=/secure/cp0-receipt.json
```

The destination must not exist. The collector writes it once with mode `0600`.
Exit `0` means the normalized evidence is ready; `3` means complete but
valid-unready evidence; `2` means CLI misuse; `1` means unsafe input,
contradiction, prerequisite failure, or write failure.

Exactly four blocker categories are reconciled as
`total = resolved + deferred + open`. Deferral is permitted, but ready evidence
has zero open and zero unowned blockers. Both micro-USD cost forecasts must be
at or below their separately approved caps, the beta account cap must be
positive, and on-call, support, and notice each need primary and backup slots
covering their required minutes.

Keep the private source artifacts and normalized receipt under the applicable
custody and retention policy. Obtain a separate current external signature for
CP0-A from Product/Counsel/Architecture and for CP0-B from
Product/Finance/Operations, then add their dossiers to the unchanged signed
external-evidence index. A ready receipt alone does not close either row.
