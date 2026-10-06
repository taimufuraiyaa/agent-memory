---
name: repository-cleanup-audit
description: Audit a repository for evidence-backed redundant, duplicated, unreachable, and dead code, then apply safe consolidations with regression verification.
---

# Repository cleanup audit

Use this skill for broad cleanup requests where the repository must be inspected
before code is removed. It is not a license to delete code based on naming or
lint output alone.

## Workflow

1. Read the repository operating instructions and the relevant spec. If no
   relevant spec exists, create `requirements.md`, `design.md`, and `tasks.md`
   before implementation. Include alternatives, trade-offs, failure modes,
   data contracts, performance, and rollout considerations in the design.
2. Establish a baseline from the current worktree. Record the test, vet/build,
   and static-analysis state before changing code. Preserve unrelated user
   changes.
3. Inventory candidates with repository-wide searches and static analysis:
   repeated local helpers, unreferenced declarations, unused fields/imports,
   unreachable branches, impossible assertions, duplicated configuration
   parsing, stale lifecycle hooks, and developer-specific fixtures. Trace every
   candidate to callers, reflection/plugin/generated-code boundaries, tests,
   and compatibility contracts before classifying it.
4. Implement in small slices. Prefer one shared dependency-light helper for
   exact duplication, remove an unused surface only after reference checks,
   and keep domain-specific implementations separate when their contracts
   differ. Do not replace a behavior-sensitive bug with a deletion merely to
   silence a diagnostic; either fix it fail-closed with tests or document it as
   deferred.
5. Update the spec task checkboxes as each slice and checkpoint is completed.
   Keep deferred findings explicit, especially test-only helpers, generated
   assets, command frameworks, deprecations, style diagnostics, and TODO
   scaffolding that still has a plausible future owner.
6. Verify proportionally: focused tests after each slice, all repository tests
   and vet, frontend tests/typecheck/build when present, whitespace checks, and
   a final static-analysis pass focused on dead-code and redundancy findings.
   If a suite failure is intermittent, reproduce the affected test in isolation
   before changing unrelated code.

## Evidence rules

- “Unused” requires source-reference checks plus consideration of public API,
  reflection, plugins, generated code, and external compatibility.
- “Duplicate” requires equivalent behavior, not merely similar syntax.
- “Dead” requires an unreachable or unassigned execution path; a TODO or
  incomplete feature is not automatically dead.
- Record what changed, where it changed, when verification ran, and what was
  intentionally deferred. Do not store private chain-of-thought.

## Agent Memory integration

For this repository, use the `agent-memory` executable from `PATH` with
`--workspace agent-memory` on every memory operation. Search before research,
score the retrieval immediately, start a solution episode for non-trivial
cleanup, append bounded work steps, write the durable outcome, and run
`session-end` before closing the episode.
