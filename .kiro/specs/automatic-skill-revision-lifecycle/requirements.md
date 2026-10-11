# Automatic Skill Revision Lifecycle Requirements

## Objective

Extend Agent Memory from explicit skill distillation into a production-safe learning loop that can propose, validate, canary, activate, observe, and roll back immutable skill revisions. An AI agent chooses a logical skill; Agent Memory resolves the single active compatible revision for the requested workspace and environment.

The feature is successful when repeated validated work can improve a skill without silently overwriting history, exposing unsafe revisions, or requiring agent runtimes to guess which revision to load.

## Assumptions

1. Skills are workspace-scoped by default; hosted records are also tenant-scoped.
2. The agent may propose a revision but cannot mark it active directly.
3. Each workspace and environment has at most one active revision per logical skill.
4. Low-risk revisions may activate automatically after policy, test, and canary gates pass.
5. Medium- and high-risk revisions require accountable approval after automated evaluation.
6. The current root `.agents/skills/<name>/SKILL.md` remains the compatibility surface for agent runtimes.
7. Existing skills are imported as revision 1 without changing their contents.

## Terminology

- **Logical skill:** Stable identity and name used by agents and retrieval.
- **Skill revision:** Immutable, content-addressed skill bundle with provenance and compatibility metadata.
- **Candidate:** A proposed create, revise, merge, or split operation that is not executable by default.
- **Evaluation suite:** Versioned positive, negative-trigger, regression, safety, and compatibility cases.
- **Canary:** A revision eligible for a bounded share of compatible executions while the current active revision remains the default.
- **Activation:** Atomic selection of one revision as the default for a workspace and environment.
- **Last-known-good:** Most recent non-disabled revision eligible for rollback.
- **Skill execution:** A recorded resolution and acknowledged use of an exact revision during a solution episode.

## User Stories

- As an agent, I request a logical skill and receive one resolved revision with a reason and fallback.
- As a user, I can inspect why a revision was proposed, tested, promoted, rejected, or rolled back.
- As an operator, I can define risk-based automatic-promotion policies without granting the proposing agent activation authority.
- As a reviewer, I can compare a candidate with the active revision and approve or reject higher-risk changes.
- As a future agent, I can reproduce an outcome using the exact skill revision recorded by the original episode.
- As a tenant administrator, I can pin, pause, roll back, or disable a skill without affecting another tenant or environment.

## Functional Requirements

### R1 — Stable Skill Identity

- A logical skill must have a stable ID, workspace, normalized unique name, description, risk tier, ownership metadata, and lifecycle status.
- Renaming a skill must preserve identity and revision lineage.
- Create, rename, archive, and restore operations must be idempotent and audited.
- Skill selection must use name, description, trigger conditions, capabilities, compatibility, and feedback rather than name-only matching.

### R2 — Immutable Revisions

- Every revision must have a monotonically increasing number, immutable bundle digest, parent revision, author identity, source candidate, provenance, compatibility constraints, and creation time.
- Revision states must include `draft`, `testing`, `canary`, `active`, `previous`, `disabled`, and `rejected`.
- Published revision content must never be overwritten. Corrections create a new revision.
- The system must retain enough lineage to compare, reproduce, export, and roll back revisions.
- Supporting scripts, references, and assets are part of the revision digest.

### R3 — Candidate Detection and Proposal

- An agent or background detector may propose `create`, `revise`, `merge`, or `split` candidates.
- Automatic detection must use bounded, validated solution episodes, tool lessons, outcomes, and explicit skill-use evidence.
- Repetition alone must not publish or activate a skill.
- Candidates must identify source records, similarity to existing skills, expected benefit, risks, confidence, and proposed evaluation suite changes.
- Duplicate proposals for the same evidence and target revision must deduplicate.

### R4 — Safe Revision Construction

- Revision construction must preserve explicitly protected human-authored sections and declared supporting assets.
- Generated content must pass existing admission controls for secrets, prompt injection, personal data, unsafe procedures, and size limits.
- The builder must produce a bounded diff against the parent revision and reject unexplained destructive removal.
- Script-bearing revisions must declare execution requirements and run only in the configured evaluation sandbox.
- Model-generated revisions remain untrusted proposals until all required gates pass.

### R5 — Versioned Evaluation Suites

- Each logical skill must support a versioned evaluation suite with positive cases, negative-trigger cases, regressions, safety cases, expected artifacts, and compatibility checks.
- Every revision must be evaluated against the same applicable suite as its active baseline plus any new cases introduced by the candidate.
- Evaluation records must include inputs by bounded reference, environment, evaluator version, results, metrics, failures, timestamps, and content digests.
- Missing or stale evaluation evidence must fail closed.
- An evaluator must not treat model self-assessment as task verification.

### R6 — Promotion Policy

- Promotion must be decided by a versioned policy, never by the proposing agent alone.
- Policy inputs must include risk tier, test verdict, baseline comparison, canary evidence, harmful feedback, approval requirements, and compatibility.
- Low-risk revisions may progress automatically when every configured gate passes.
- Medium- and high-risk revisions must require an authorized approval; high-risk revisions must never auto-activate.
- Policy changes must not retroactively reinterpret historical decisions.

### R7 — Canary Execution

- Canary allocation must be deterministic, bounded, reversible, and limited to compatible low- or approved medium-risk revisions.
- The active revision must remain the default until canary success thresholds are satisfied.
- Canary and baseline executions must record the exact resolved revision and comparable outcome metrics.
- Insufficient samples, evaluator outages, or ambiguous results must leave the candidate in canary or testing rather than promote it.
- Hard safety failures must immediately stop canary eligibility.

### R8 — Atomic Activation and Resolution

- Activation must atomically select exactly one active revision per skill, workspace, and environment using optimistic concurrency.
- Resolution order must honor authorized explicit pins, environment pins, compatibility, canary allocation, active revision, and last-known-good fallback.
- The resolution response must contain logical skill ID, revision ID and number, digest, reason, compatibility decision, and fallback revision.
- The root compatibility `SKILL.md` must be materialized atomically from the activated immutable revision and verified by digest.
- Database state and filesystem materialization must recover deterministically after crashes or partial failures.

### R9 — Skill-Use Acknowledgement and Telemetry

- Selecting a revision must not imply it was used; the agent runtime must acknowledge loading it.
- Each acknowledged execution must link workspace, environment, episode, task class, logical skill, revision, resolution reason, start and completion times, outcome, verification status, token/tool counts when available, and feedback.
- Raw prompts, secrets, unrestricted tool output, and hidden reasoning must not be telemetry fields.
- Metrics must support revision-versus-baseline comparison without unbounded labels or customer-content leakage.

### R10 — Automatic Rollback and Disablement

- Hard safety failures, digest mismatch, unauthorized content, or configured harmful-feedback thresholds must disable the affected revision immediately.
- Rollback must atomically reactivate the last-known-good compatible revision and rematerialize the root skill file.
- Soft performance regressions must pause promotion and open a review rather than cause uncontrolled oscillation.
- Rollback and disablement must be idempotent, audited, and visible to agents and users.
- A disabled or rejected revision must never be selected by default.

### R11 — Public Contracts

- Standalone CLI, HTTP, expanded MCP, and registered-project hosted APIs must support candidate proposal, revision inspection, evaluation, promotion, resolution, acknowledgement, rollback, and policy inspection.
- Legacy clients must continue to read the active root `SKILL.md` without revision awareness.
- Remote clients must use registered workspace identities and must never submit arbitrary local paths.
- All mutation contracts must require idempotency and expected revision or generation where applicable.

### R12 — Dashboard and Review

- The Skills surface must show logical skills, active revision, candidates, revision history, evaluation results, canary progress, approvals, usage, feedback, and rollback events.
- Users must be able to compare revisions, inspect provenance, approve eligible revisions, pause canaries, pin a revision, and roll back according to authorization.
- The UI must clearly distinguish latest, active, canary, previous, disabled, and rejected.
- No state may be inferred from file timestamps or Git history when stored lifecycle data is absent.

### R13 — Isolation, Security, and Audit

- Every skill, revision, candidate, evaluation, activation, execution, approval, and rollback must be tenant- and workspace-authorized.
- Skill bundle reads and writes must remain contained by the registered project root and reject symlink or replacement-parent escapes.
- Revision digests, evaluator identities, policy versions, and accountable decisions must be auditable without logging customer content.
- Export and deletion must preserve applicable retention, legal hold, and provenance rules.
- Automatic execution must use least privilege and must not grant new capabilities merely because a skill requests them.

### R14 — Reliability, Scale, and Compatibility

- Detection, evaluation, canary analysis, activation, and rollback jobs must be restartable and idempotent.
- Candidate scans and evaluation queues must be bounded, backpressured, and fair across workspaces.
- Existing `distill` behavior must remain available but must create a draft revision instead of overwriting an active artifact.
- Existing skill metadata must migrate without losing paths or provenance.
- Hosted and standalone runtimes must provide equivalent lifecycle semantics.

## Commands

- Focused Go tests: `go test ./internal/core ./internal/storage/sqlite ./internal/application ./internal/engine ./internal/workspace ./internal/cli ./internal/api`
- Hosted tests: `go test ./internal/saas/...`
- MCP tests: `npm --prefix tools/agent-memory/mcp-server test`
- Dashboard tests: `npm --prefix tools/agent-memory/dashboard test`
- Dashboard type check: `npm --prefix tools/agent-memory/dashboard run typecheck`
- Dashboard build: `npm --prefix tools/agent-memory/dashboard run build`
- Full verification: `go test ./... && go vet ./...`

## Boundaries

- Always: preserve immutable revision history, explicit provenance, digest verification, one-active-revision invariant, and exact-use acknowledgement.
- Ask first: change default risk classifications, automatic-promotion thresholds, canary allocation, or retention periods.
- Never: allow an agent proposal to activate itself, overwrite a published revision, execute unreviewed high-risk skills, infer usage from retrieval, or select a disabled revision.

## Success Criteria

1. An existing filesystem skill migrates to revision 1 and remains usable by legacy clients.
2. A repeated validated workflow produces a reviewable candidate without activating it.
3. A low-risk candidate automatically passes tests and canary gates, becomes active atomically, and records the exact policy decision.
4. A high-risk candidate cannot activate without authorized approval.
5. Every execution identifies the exact loaded revision and supports baseline comparison.
6. A hard canary failure automatically restores the last-known-good revision.
7. Concurrent promotion attempts cannot produce two active revisions.
8. Cross-workspace, path-escape, stale-evidence, poisoned-input, and digest-mismatch tests fail closed.
9. Dashboard and public contracts expose the same stored lifecycle and provenance.
10. Full regression and compatibility suites pass without changing factual memory retrieval behavior.

## Product Decisions Requiring Review

1. Default thresholds and minimum canary sample sizes for each risk tier.
2. Which skill capabilities are always classified high risk.
3. Whether medium-risk auto-promotion may be enabled by an administrator.
4. Retention duration for execution-level telemetry and rejected revision bundles.
5. Whether a workspace pin may override a globally disabled revision; the recommended answer is no.
