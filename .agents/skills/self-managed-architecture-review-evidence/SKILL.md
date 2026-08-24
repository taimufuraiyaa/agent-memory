---
name: self-managed-architecture-review-evidence
description: Verify or extend Agent Memory P0.2-A inventory-bound architecture, topology, facility, data-flow, integration-contract, and accountable-review evidence.
---

# Self-managed architecture review evidence

## Preserve these invariants

- Bind the exact opened bytes of one valid staging or production self-managed
  inventory. Never query infrastructure or accept local/mock classification.
- Require all eight inventory components and exactly ownership, custody,
  capacity, failure-isolation, cost, and incident-response reviews per component.
- Reconcile reviewed failure-domain count to inventory. Require at least one
  independent staging domain and two independent production domains.
- Require exactly eight fixed data-flow reviews and payment/email/model states
  matching inventory. Enabled integrations use `approved_contract`; disabled
  integrations use `disabled_decision` and remain reviewed.
- Derive ten exact checks and bind inventory, service/topology ADR, facility,
  failure-domain, data-flow, integration-contract, and accountable-review
  artifacts to their declared digests.
- Preserve complete failed/inconclusive evidence as valid-unready. Reject
  omissions, unknowns, duplicates, substitutions, stale clocks, and aggregate,
  outcome, or readiness contradictions.
- Exclude provider/facility/site/host/endpoint names, topology, contracts,
  costs, people, credentials, diagrams, paths, logs, raw reports, and customer
  data. Publish create-only mode `0600`; CLI output is aggregate-only with exits
  `0/3/2/1`.
- Local fixtures never close P0.2-A or alter the exact 57-control catalog.

## Verification

Run focused package/CLI/platform-inventory/contract race tests, full Go tests
and vet, Kubernetes/release gates, all JSON parsing, `git diff --check`, and the
exact catalog/matrix reconciliation in `verify-external-evidence-index`.
