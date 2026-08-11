---
name: checkpoint-zero-program-evidence
description: Verify or extend the Agent Memory shared CP0-A/CP0-B program-approval evidence boundary. Use when changing prerequisite receipt loading, architecture blockers, cost ceilings, beta caps, staffing coverage, schemas, CLI behavior, or readiness documentation.
---

# Checkpoint-zero program evidence

Use this workflow for changes to `internal/saas/programapprovalevidence`,
`cmd/agent-memory-program-approval`, or the CP0 documentation and schemas.

## Invariants

- CP0 is an early business and architecture checkpoint. Do not introduce
  dependencies on CP10, P11, cloud-provider accounts, deployment spending, or
  the UI's planning-budget setting.
- Reload a valid self-managed inventory and exact ready P0.1 and P0.2-C
  receipts. Hash the exact receipt bytes and reject unready, edited, symlinked,
  substituted, or inventory-mismatched prerequisites.
- Keep exactly four blocker categories: infrastructure ownership, topology,
  external integration, and jurisdiction.
- Require `total = resolved + deferred + open`. Deferrals are allowed, but
  ready evidence has zero open and zero unowned blockers.
- Keep separate non-negative micro-USD forecasts and approved ceilings for
  infrastructure and worst-case beta economics. The beta account cap is
  positive. Never treat configuration defaults as approval evidence.
- Keep exactly three staffing domains: on-call, support, and notice. Each needs
  positive primary and backup slots and both coverages must meet required
  minutes.
- Keep exactly ten checks and derive prerequisite, blocker, cost, and staffing
  outcomes independently. Preserve complete adverse evidence as valid-unready.
- Inputs and receipts are content-free, bounded, regular-file-only, strict JSON.
  Receipts are create-only mode `0600`; exit codes are 0 ready, 3 valid-unready,
  2 CLI misuse, and 1 unsafe/error.
- One receipt supports both CP0 rows, but does not approve either. CP0-A and
  CP0-B need separate current accountable signatures in the unchanged 57-row
  external-evidence index.

## Change workflow

1. Read R63 and P0.6 in the SaaS requirements, design, and tasks.
2. Add failing focused tests before behavior changes.
3. Run:

   ```sh
   go test ./internal/saas/launchscopeevidence \
     ./internal/saas/externalintegrationevidence \
     ./internal/saas/programapprovalevidence \
     ./cmd/agent-memory-program-approval
   ```

4. Exercise the full-chain test, not only pure normalization. It must create
   inventory, P0.1, and P0.2-C artifacts and verify exact-byte bindings.
5. Keep input and receipt schemas closed and aligned with the published Go
   shape. Parse every checked JSON file.
6. Update the example, runbook, Make target, matrix support text, status ledger,
   and P0.6 acceptance boxes together.
7. Run full Go tests, race tests for the four focused packages, vet, contract,
   Kubernetes, release-script, exact 57-control, and diff checks.
8. Confirm the external catalog still contains exactly 57 rows and CP0-A and
   CP0-B remain open unless real private artifacts and signatures exist.
