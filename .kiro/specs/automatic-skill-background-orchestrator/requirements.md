# Automatic Skill Background Orchestrator Requirements

## Objective

Build the durable runtime that continuously advances eligible automatic-skill lifecycle work through detection, revision construction, evaluation, policy decision, canary, analysis, activation, safety handling, and rollback. The orchestrator coordinates the existing lifecycle services; it does not weaken their authorization, evidence, or state-transition rules.

Success means a standalone installation and a horizontally scaled hosted deployment can recover from process loss, duplicate delivery, stale leases, partial failure, and restart without skipping required evidence, activating the latest revision by accident, or requiring an operator to manually drive routine low-risk work.

## Approved Assumptions

- This is a separate production feature layered on `.kiro/specs/automatic-skill-revision-lifecycle/`.
- SQLite is authoritative for standalone jobs; PostgreSQL is authoritative for hosted jobs.
- The first production release uses database-backed enqueue and polling. A message broker may provide wakeups later but cannot become authoritative.
- Event-driven enqueue is paired with periodic reconciliation so a lost wakeup cannot strand work.
- Existing lifecycle application services remain the only executors of domain policy and state changes.
- Automatic candidate generation, canary, activation, and rollback are independently controllable and disabled by default.
- Low-risk automatic activation remains blocked until an accountable product-policy record enables it. Medium and high risk retain the lifecycle specification's approval constraints.

## Non-Goals

- Replacing immutable skill revisions, activation sagas, policy decisions, acknowledgement, telemetry, or materialization.
- Letting an agent, worker, queue payload, or filesystem timestamp choose the active revision.
- Introducing a general-purpose workflow engine or requiring an external broker.
- Automatically repairing rejected evidence, lowering evaluation thresholds, or converting inconclusive results into success.
- Running high-risk activation automatically.

## Terminology

- **Workflow:** one durable orchestration instance for a logical skill and originating signal.
- **Job:** one bounded stage attempt belonging to a workflow.
- **Wakeup:** a non-authoritative hint that claimable work may exist.
- **Reconciliation:** authoritative scanning that creates missing jobs and repairs stranded state.
- **Lease:** time-bounded ownership of a running job.
- **Fence:** monotonically increasing claim token that prevents an expired worker from committing.
- **Terminal:** `completed`, `cancelled`, `rejected`, or `dead_lettered`.

## R1 — Durable Workflow and Job State

- Every workflow and job must be persisted before execution and scoped by tenant, workspace, environment, and logical skill where applicable.
- Workflow stages must include `detect`, `build`, `evaluate`, `decide`, `start_canary`, `analyze_canary`, `activate`, `observe_safety`, `rollback`, and `reconcile_materialization`.
- Job states must include `queued`, `blocked`, `running`, `retry_wait`, `completed`, `cancelled`, and `dead_lettered`.
- A job must bind an immutable input digest, policy version, dependency IDs, attempt count, scheduling time, lease owner, lease expiry, fence, safe failure code, and timestamps.
- Payloads must contain identifiers and bounded safe summaries, never skill content, prompts, raw outputs, credentials, hidden reasoning, or arbitrary filesystem paths.
- Workflow and job transitions must be append-audited and idempotent.

## R2 — Enqueue and Dependency Graph

- Verified solution/tool-lesson events may enqueue detection without executing detection inside the request transaction.
- Detection output may enqueue build only for an authorized, non-duplicate candidate that satisfies configured confidence policy.
- Build output may enqueue evaluation only after immutable bundle admission succeeds.
- Evaluation may enqueue a policy decision only when candidate and baseline runs are terminal and comparable.
- A canary may start only from an eligible policy decision and current activation generation.
- Canary analysis may run only after the configured observation window and minimum acknowledged verified samples are available.
- Activation may be enqueued only by an eligible promote decision; rollback may be enqueued by a verified hard safety signal or accountable operator action.
- Dependency satisfaction must be determined from authoritative records, not delivery order.

## R3 — Claiming, Leasing, and Fencing

- Claiming must be atomic, bounded, oldest-ready-first, and fair across workspaces.
- Hosted claiming must use row locking with skip-locked semantics; standalone claiming must serialize SQLite writers without blocking normal reads for the lease duration.
- A claim increments the attempt and fence, assigns an owner, and sets a bounded expiry.
- Long-running stages must renew before expiry through compare-and-swap on owner and fence.
- Completion, failure, renewal, and cancellation must fail when ownership or fence is stale.
- Expired jobs must be safely reclaimable by another worker.
- Worker loss after domain success but before job completion must converge through idempotent replay.

## R4 — Retry, Backoff, and Dead Letter

- Failures must be classified as retryable dependency failure, retryable contention, inconclusive evidence, policy block, permanent validation failure, cancellation, or unknown internal failure.
- Retryable failures use bounded exponential backoff with deterministic jitter and per-stage maximum attempts.
- Policy blocks and insufficient evidence enter `blocked` with an explicit recheck condition rather than consuming attempts.
- Permanent failures dead-letter immediately; exhausted retryable failures dead-letter after the configured attempt ceiling.
- Dead-letter replay requires authorization, a reason, and a new idempotency key while preserving the original record.
- Error records expose stable content-free codes; raw third-party or model errors remain in protected diagnostics only.

## R5 — Reconciliation and Restart Recovery

- Startup reconciliation must recover expired leases, incomplete activation/materialization operations, missing successor jobs, and terminal workflows with non-terminal jobs.
- Periodic reconciliation must cover every enabled workspace within a bounded interval.
- Reconciliation must be safe under concurrent workers and repeated execution.
- A clean shutdown stops claiming, cancels claim waits, allows a bounded drain period, and releases or expires unfinished leases without marking them successful.
- Clock movement must not skip jobs; persisted UTC timestamps and fence checks remain authoritative.
- Database restore must not silently resume automatic activation until post-restore reconciliation and policy readiness succeed.

## R6 — Stage Execution Contracts

- Each stage adapter validates scope, immutable input digest, required dependencies, current lifecycle generation, and policy readiness before calling an existing application service.
- Stage adapters must return a bounded result classification plus authoritative output identifiers.
- The worker must never directly mutate skill lifecycle tables outside repository/job transitions and existing lifecycle service contracts.
- External evaluation execution must have an explicit timeout, cancellation, resource limits, environment fingerprint, and least-privilege capability set.
- A stage that observes already-achieved domain state completes idempotently only when the stored input bindings match.

## R7 — Canary and Promotion Scheduling

- Canary sampling remains request-driven through deterministic resolution; the orchestrator only manages canary lifecycle state and analysis timing.
- The analyzer must use acknowledged, independently verified, retention-valid execution records from the exact candidate and baseline revisions.
- Insufficient traffic reschedules analysis without reducing thresholds.
- Ambiguous or inconclusive results pause for review.
- Low-risk automatic activation requires an explicitly enabled immutable policy version and approved release gate.
- Medium risk requires the configured accountable approval; high risk can never enqueue automatic activation.
- A stale activation generation cancels the promotion job and triggers reconciliation rather than overwriting newer state.

## R8 — Safety Signal Ingress and Rollback Priority

- Accepted hard signals include verified digest mismatch, prohibited capability use, independently verified harmful outcome, materialization custody failure, and policy-defined critical regression.
- Signals must be authenticated, deduplicated, revision-bound, and stored before a rollback job is enqueued.
- Safety and rollback jobs have priority over detection, build, evaluation, and promotion within the same scope.
- A hard signal immediately prevents new canary allocation for the affected revision before rollback execution.
- Soft signals enter policy cooldown and analysis; repeated delivery must not oscillate activation.
- Rollback failure leaves the revision disabled, raises a critical alert, and never restores allocation automatically.

## R9 — Standalone Runtime

- Standalone orchestration runs only while an Agent Memory service process with the exact workspace database is active.
- One embedded worker may execute bounded jobs for multiple registered local workspaces without crossing roots or databases.
- SQLite polling must use configurable bounds and avoid a permanent background goroutine per skill.
- A single installation-level leader lease prevents duplicate sweep ownership while job leases still protect execution.
- CLI operations must expose status, pause, resume, reconcile, retry, and drain without requiring direct database access.

## R10 — Hosted Runtime

- Hosted orchestration runs as a distinct least-privilege worker deployment and may scale horizontally.
- Every query and job transition must retain tenant row-level isolation.
- Fair claiming must prevent one tenant or workspace from consuming all worker capacity.
- The hosted API creates jobs and reads status but cannot impersonate a worker lease owner.
- Worker, reconciler, and API identities must have separate database and object capabilities.

## R11 — Configuration and Enablement

- Configuration is backend-owned, versioned, bounded, and readable without secrets.
- Controls include master enablement, per-stage enablement, poll interval, reconciliation interval, claim batch, concurrency, lease, renewal interval, timeout, attempts, backoff bounds, drain timeout, and per-workspace quotas.
- Invalid or missing configuration fails readiness and prevents claims.
- Enablement changes affect new claims; already-running safety/rollback work may finish during disablement.
- Enabling automatic promotion requires a valid accountable approval reference and matching policy digest.
- Configuration changes emit content-free audit events.

## R12 — Authorization, Isolation, and Privacy

- Workflow creation, inspection, cancellation, retry, and policy enablement use the existing tenant/workspace authorization boundaries.
- Worker identities cannot approve their own policy, weaken risk, alter immutable evidence, or grant new skill capabilities.
- Local jobs use registered contained roots; hosted jobs never carry local paths.
- Logs, metrics, traces, job payloads, and dead letters exclude customer content and sensitive identifiers from unbounded labels.
- Export, deletion, retention, legal hold, and tombstone behavior must include orchestrator records without resurrecting deleted evidence.

## R13 — Observability and Operations

- Metrics cover queue depth, oldest-ready age, claim latency, stage duration, retry, blocked, dead-letter, lease expiry, renewal failure, reconciliation repair, safety signal, rollback latency, and drain outcome.
- Metrics use bounded stage, outcome, environment, and failure-class labels only.
- Readiness requires database access, valid configuration, migration compatibility, policy-gate state, and executor readiness for enabled stages.
- Liveness reports process health and must not become false merely because work is blocked by evidence or policy.
- Alerts cover stuck ready work, repeated lease expiry, dead-letter growth, evaluator outage, stale canary, rollback failure, reconciliation drift, and unexpected automatic activation.
- Runbooks must include safe pause, drain, retry, dead-letter review, evaluator outage, stuck canary, rollback failure, restore recovery, and complete feature shutdown.

## R14 — Performance, Capacity, and Fairness

- Claim batch, per-worker concurrency, per-tenant concurrency, and per-workspace concurrency are hard bounded.
- Reconciliation is cursor-based and bounded; it must not scan an entire corpus in one transaction.
- Normal skill resolution and memory retrieval cannot synchronously wait for orchestration work.
- Job table indexes must support ready claims, expired lease recovery, workflow lookup, and tenant/workspace status without full scans.
- Capacity evidence must cover a representative large workspace, many small workspaces, noisy-neighbor behavior, evaluator latency, and rollback priority.
- Cost controls must bound evaluation/model usage per workspace and stop enqueue when the approved budget is exhausted.

## R15 — Public Contracts and Compatibility

- Standalone CLI/HTTP, expanded MCP, and registered-project hosted surfaces expose equivalent status and authorized operations.
- Existing lifecycle operation payloads remain compatible; orchestration adds durable asynchronous operation IDs and status rather than changing revision semantics.
- Legacy agents continue reading the active materialized root skill and never see queue internals.
- API collections are paginated and return bounded content-free job summaries.
- Contract versions must allow rolling worker and API upgrades without mixed-version unsafe claims.

## R16 — Release and Evidence Gates

- Shadow mode must enqueue and simulate decisions without building, canarying, activating, or rolling back artifacts.
- A deterministic replay corpus must prove equivalent outcomes across standalone and hosted repositories.
- Chaos tests must cover crash before/after each domain side effect, lease loss, duplicate enqueue, stale fence, database outage, evaluator timeout, cancellation, and worker restart.
- Isolation tests must cover two tenants, two workspaces, forged job IDs, stale tokens, and timing-safe unknown-scope behavior.
- Automatic promotion remains disabled until shadow parity, canary quality, false-promotion, rollback, load, isolation, and accountable product-review evidence are approved.
- Production release requires signed configuration and approval evidence bound to the build and migration versions.

## Commands

- Focused domain/application/storage tests: `go test ./internal/core ./internal/application ./internal/storage/sqlite`
- Hosted repository and worker tests: `go test ./internal/saas/... ./cmd/agent-memory-skill-worker`
- Standalone runtime tests: `go test ./internal/cli ./internal/api ./internal/integration`
- MCP contracts: `npm --prefix tools/agent-memory/mcp-server test`
- Dashboard contracts: `npm --prefix tools/agent-memory/dashboard test`
- Type checking: `npm --prefix tools/agent-memory/dashboard run typecheck`
- Repository verification: `go vet ./... && go test ./...`
- Production dashboard gate: `make build-with-dashboard`

## Project Structure

- `internal/core/` — workflow/job/configuration domain contracts.
- `internal/application/` — enqueue, stage adapters, reconciliation, and orchestration policy coordination.
- `internal/storage/sqlite/` — standalone queue, leases, migration, and status repository.
- `internal/saas/postgres/` — hosted queue, RLS, skip-locked claims, and retention.
- `internal/saas/skillworker/` — provider-neutral worker runtime.
- `cmd/agent-memory-skill-worker/` — hosted worker process.
- `internal/cli/`, `internal/api/`, `internal/saas/api/` — authorized control/status surfaces.
- `internal/observability/` — bounded metrics and alerts.
- `docs/runbooks/` — operational procedures.

## Implementation Conventions

- Follow existing Go package boundaries, formatting, error wrapping, UTC timestamp, and table-driven test conventions.
- Define interfaces at the consuming application or worker boundary; keep SQLite and PostgreSQL implementations behaviorally interchangeable.
- Use explicit bounded enums and typed contracts rather than unvalidated string maps.
- Keep transaction scope short and separate claim/finalization transactions from stage execution.
- Return stable safe error codes at public boundaries and retain detailed errors only in protected diagnostics.
- Use deterministic clocks, identifiers, jitter sources, and executor fixtures in tests.

## Boundaries

- **Always:** persist before execution, fence every mutation, reuse lifecycle services, fail closed, keep automation opt-in, verify tenant/workspace scope, and preserve immutable evidence.
- **Require accountable approval:** automatic-promotion enablement, production thresholds, evaluation/model budgets, retention policy, and replay of policy or safety dead letters.
- **Never:** activate latest by recency, execute arbitrary job payload code, log customer content, bypass evaluation/approval, let expired workers commit, or weaken thresholds to clear a queue.

## Definition of Done

- All R1-R16 requirements have direct automated or accountable manual evidence.
- Standalone and hosted natural-flow tests progress eligible low-risk work without manual stage calls and recover after worker restart.
- Hard safety signals disable allocation and restore last-known-good within the approved rollback SLO.
- Duplicate delivery, stale workers, and partial failures cannot produce duplicate domain side effects or unauthorized activation.
- Feature shutdown leaves existing active skills usable and all durable workflow history auditable.
