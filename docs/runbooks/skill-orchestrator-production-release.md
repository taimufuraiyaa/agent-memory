# Skill Orchestrator Production Release

This runbook controls the only supported path from a default-off deployment to automatic low-risk operation. Execute it in staging twice before production. Preserve content-free evidence only: release identifiers, digests, counts, durations, bounded outcomes, and signer identities. Never place skill content, prompts, credentials, or customer identifiers in the evidence bundle.

## Preconditions

1. Pin the immutable build digest and migration digest. Apply migrations before starting a worker.
2. Verify the migration, standalone natural-flow, hosted natural-flow, chaos, independent security, capacity, and alert-routing report digests.
3. Verify each staged configuration through a signed configuration receipt. Recompute its configuration digest, then verify its policy digest, evaluation budgets, retention, retry/dead-letter policy, canary thresholds, rollback SLO, and drain timeout.
4. Confirm the base Kubernetes deployment still has zero skill-worker replicas and `AGENT_MEMORY_SKILL_WORKER_ENABLED=false`.
5. Confirm the release signer and accountable product approver use separate trusted keys and identities.

## Disabled drill

1. Install the exact build and migrations with the orchestrator configuration set to `disabled`.
2. Prove that no ordinary jobs are claimed and active skill resolution continues unchanged.
3. Record the signed configuration digest, active skill digest, audit record count, and alert-route verification.

## Shadow drill

1. Install the next immutable signed configuration in `shadow` mode.
2. Run deterministic discovery and decision simulation. Confirm that no bundle is built, no canary is allocated, and no activation or rollback mutates lifecycle state.
3. Bind the stable shadow report digest to the release evidence.

## Manual drill

1. Move to `manual` only after shadow parity passes.
2. Run authorized operator-controlled build, evaluation, and activation paths while automatic promotion remains blocked.
3. Verify active skill digest and audit lineage before and after each action.

## Canary drill

1. Move to `canary` with the approved allocation, minimum sample, minimum age, maximum age, and acknowledgement policy.
2. Verify exact-revision sampling, stale-canary routing, false-promotion evidence, and last-known-good availability.
3. Trigger a verified hard signal under ordinary and saturated capacity. The reserved rollback lane must restore last-known-good within the approved rollback SLO.

## Automatic low-risk approval

1. Collect the exact release evidence bundle after both complete staging iterations pass.
2. Obtain an independently signed accountable product approval for risk classes, thresholds, canary policy, retry/dead-letter policy, budgets, retention, SLOs, and automatic-low-risk enablement.
3. Verify every configuration-receipt signature plus the release-evidence and product-approval signatures, freshness, separation of duty, release ID, final automatic configuration digest, release-evidence digest, policy digest, build digest, and migration digest with the production release gate.
4. Only a `ready: true` gate report may be referenced by an `automatic_low_risk` configuration. Medium risk still requires revision approval; high risk never activates automatically.

## Pause and drain drill

1. Change the backend-owned configuration to `disabled` and stop new ordinary claims.
2. Permit only already-leased recovery work and the reserved rollback lane during the bounded drain timeout.
3. Record remaining claims, lease releases, active skill digest, audit record count, drain duration, and routed alerts. Do not mark interrupted jobs successful.

## Restore drill

1. Restore the database or object state with the orchestrator still `disabled`.
2. Verify schema, migration digest, configuration digest, leases, activation generations, safety parity, tombstones, and root materialization before enabling `shadow`.
3. Confirm the active skill digest is unchanged and the audit record count never decreases. Fresh build- and migration-bound approval is required before automatic mode.

## Complete shutdown drill

1. Disable recurrence, canary allocation, promotion, and ordinary claims; drain within the configured timeout.
2. Restore every affected root to its verified last-known-good revision and verify the active skill digest.
3. Stop the skill-worker deployment and leave it at zero replicas with its feature flag false.
4. Keep immutable revisions, workflows, attempts, safety signals, configuration history, and audit records readable. Confirm legacy agents continue resolving the restored active skills.

## Evidence signing and verification

1. Produce payloads that validate against `api/evidence/v2/skill-orchestrator-configuration-receipt.schema.json`, `skill-orchestrator-production-release-evidence.schema.json`, and `skill-orchestrator-product-approval.schema.json`; never hand-edit a signed payload.
2. Sign each full staged configuration receipt and the release evidence with the trusted release identity. A boolean claiming that a configuration signature was checked is not evidence.
3. Compute the release-evidence digest, bind it and the final automatic configuration digest into the accountable product approval, and sign that approval with a distinct trusted product key.
4. Run the release gate at the deployment boundary. Archive the payloads, signatures, public-key identifiers, configuration digest, evidence digest, and approval digest.
5. Version 1 release-evidence and product-approval payloads are rejected because they do not cryptographically bind the full staged configurations. Regenerate them under the version 2 schemas.
6. Any changed configuration, policy, build, migration, runbook, alert routing, or prerequisite report invalidates the old bundle and requires new staging drills and signatures.

## Abort conditions

Keep or return the system to `disabled` when any signature, binding, report, drill, alert route, rollback SLO, active skill digest, audit history, separation-of-duty check, or product control is missing or invalid. Preserve the failed evidence for review; never weaken a threshold or reuse an earlier approval to clear the gate.
