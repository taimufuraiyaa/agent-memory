# Skill Orchestrator Capacity Evidence

This report defines the production capacity boundary for background skill work. It is evidence for Task 27, not permission to enable automation.

## Enforced limits

| Boundary | Enforcement | Verification |
|---|---|---|
| Process-global execution | Fixed hosted lane pools and `SkillCapacityCoordinator.Global` | `TestSkillCapacityCoordinatorPreservesRollbackAndSkipsNoisyTenantHead` |
| Tenant and workspace execution | Shared coordinator counters keyed by full scope | coordinator test and `TestRuntimeSlowTenantDoesNotBlockOtherAssignments` |
| Stage execution | Explicit per-stage permit limits; evaluation is capped below global and activation at one | coordinator validation and hosted lane construction |
| Rollback reserve | Ordinary work cannot consume reserved global permits; rollback has its own fixed worker pool | coordinator test and hosted runtime lane tests |
| Reconciliation work | Existing batch, run-time, and per-domain timeout limits | `TestSkillReconcilerHonorsTimeBudgetShutdownAndConcurrentCAS` |
| Evaluation cost | Atomic period account plus expiring reservation, commit, release, replay, and exhaustion | `TestSQLiteSkillEvaluationBudgetIsAtomicReplaySafeAndScoped` |
| SQLite responsiveness | Fixed worker concurrency and bounded claims; no goroutine per skill or workspace | `TestSQLiteSkillStandaloneRuntimeKeepsMultipleWorkspaceCyclesResponsive` |
| Horizontal hosted work | PostgreSQL `SKIP LOCKED`, lane filters, RLS, fixed pools, and replay-safe jobs | `TestRuntimeTwoReplicasAndWorkerLossPreserveOneClaimOutcome` and PostgreSQL lane tests |

## Shipped bounds

- Hosted defaults: concurrency 8, tenant concurrency 4, workspace concurrency 2, rollback reserve 2.
- Each lane call claims one job, preventing a large workspace from pre-claiming an entire lease batch.
- Evaluation defaults to at most half of process concurrency; activation is serialized per process.
- Budget accounts are bound to tenant, workspace, environment, policy version, and period start.
- Expired reservations release units before new admission. Committed units never return to the period budget.

## Operational interpretation

Process-global limits are per worker replica. Deployment replica count is therefore part of the approved capacity envelope. PostgreSQL remains authoritative for duplicate prevention, tenant isolation, and atomic budget totals across replicas. Retrieval runs outside these worker pools and is not admitted through skill-work capacity.
