---
name: launch-notice-readiness-evidence
description: Verify or extend the Agent Memory shared P6.5-A/CP6-A launch-notice legal and staffing evidence boundary. Use when changing P0.1 receipt binding, jurisdiction-route coverage, notice deadlines, escalation, staffing, tabletop scenarios, schemas, CLI behavior, or readiness documentation.
---

# Launch notice readiness evidence

Use this workflow for `internal/saas/noticereadinessevidence`,
`cmd/agent-memory-notice-readiness`, or related schemas and documentation.

## Invariants

- Reload and revalidate the exact ready P0.1 receipt and bind its exact opened
  bytes. Reject unready, edited, symlinked, substituted, or contradictory
  prerequisites.
- Require exactly one route per P0.1 notice-jurisdiction count. Routes use
  unique jurisdiction-reference SHA-256 values and are sorted by that hash.
  Never add country codes, jurisdiction names, copy, contacts, or endpoints.
- Each route binds copy, routing, deadline, and escalation digests. Required
  language count and normal/urgent deadlines are positive; covered languages
  meet the requirement; urgent is no longer than normal; and primary plus
  backup escalation counts are positive for readiness.
- Keep exactly three staffing domains: `notice_intake`, `legal_review`, and
  `user_response`. Each has positive required coverage, primary and backup
  coverage meeting it, and positive primary and backup slots.
- Keep exactly four scenarios: valid, invalid, conflicting, and urgent notice.
  Reconcile `executed = passed + failed + inconclusive`, require positive
  executions/targets/observations, and derive outcome and duration compliance.
- Keep exactly ten checks. Derive launch-scope, routing, copy/language,
  deadline, escalation, staffing, and tabletop outcomes independently.
  Preserve complete adverse evidence as valid-unready.
- Inputs and receipts are strict, bounded, content-free regular JSON files.
  Receipts are create-only mode `0600`; CLI exits are 0 ready, 3 valid-unready,
  2 misuse, and 1 malformed/unsafe/error.
- One receipt supports two dossiers but approves neither. P6.5-A needs a real
  Counsel decision; CP6-A needs a separate Legal Operations/Support decision.

## Verification workflow

1. Read R64, P6.5, P6.6, and Checkpoint 6 in the SaaS spec.
2. Add a failing focused test before behavior changes.
3. Run:

   ```sh
   go test ./internal/saas/launchscopeevidence \
     ./internal/saas/noticereadinessevidence \
     ./cmd/agent-memory-notice-readiness
   ```

4. Preserve the full-chain test that publishes a real P0.1 receipt and rejects
   a tampered one. Pure `build` tests alone do not prove the boundary.
5. Keep input and receipt schemas closed and exact-shape tested. Parse all JSON.
6. Update the example, runbook, Make target, P6.6 boxes, both matrix rows, and
   implementation ledger together.
7. Run focused race tests, all Go tests, vet, contracts, Kubernetes/release
   scripts, exact 57-control reconciliation, and `git diff --check`.
8. Confirm both external rows remain open unless authoritative artifacts and
   current accountable signatures genuinely exist.
