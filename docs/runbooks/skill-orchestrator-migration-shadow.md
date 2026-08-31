# Skill Orchestrator Migration and Shadow Gate

The background orchestrator stays in `disabled` or `shadow` mode until this gate is ready. The gate is read-only: it discovers incomplete lifecycle state and predicts jobs, but does not create workflows, transition revisions, or materialize skills.

## Procedure

1. Apply all database migrations. Confirm the expected schema version is `30` for standalone SQLite or `0037_skill_orchestrator_budget` for hosted PostgreSQL.
2. Keep the versioned orchestrator mode `disabled` or `shadow`.
3. During restore, set the scoped restore-pause flag before exposing the database to workers.
4. Run the migration inventory with a bound large enough to avoid truncation. It reports only identifiers, states, digests, and whether an open workflow already exists.
5. Review predictions for proposed or accepted candidates, testing revisions, canaries, and incomplete activation operations.
6. Run the same inventory again. The shadow digest must be identical and lifecycle/workflow row counts must not change.
7. Compare standalone and registered-project hosted predictions. Bind the approved shadow digest to release evidence.
8. Clear restore pause only after schema, lease, activation, materialization, policy, and tombstone reconciliation pass.

## Fail-closed outcomes

- `migration_version_mismatch`: finish or roll back migrations; do not start claims.
- `restore_reconciliation_paused`: complete post-restore reconciliation first.
- `unsafe_migration_mode`: return configuration to `disabled` or `shadow`.
- `inventory_truncated`: increase the bounded inventory limit or partition the review.
- `unsupported_contract_version`: drain the mixed deployment or install a compatible worker.
- `shadow_parity_mismatch`: investigate deterministic inputs; never overwrite the approved digest.

Completed lifecycle history remains lifecycle history. The gate never fabricates historical workflows for already completed work.
