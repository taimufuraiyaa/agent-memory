---
name: solution-path-natural-verification
description: Verify Agent Memory's complete “remember how” workflow through the same public standalone surfaces an agent uses. Trigger after changing solution capture, finalization, How recall, promotion provenance, How History, or standalone dashboard routing/runtime selection.
---

# Solution-Path Natural Verification

Use a fresh temporary database. Do not reuse the developer's normal workspace database and do not treat isolated repository tests as proof of the complete workflow.

## Preconditions

- Build or run the branch under test.
- Use a unique workspace, session, principal, client, idempotency keys, and temporary database path.
- Keep captured summaries safe and externally explainable; never add raw chain-of-thought.
- If a hosted webapp is already running, use `dashboard --force-local` when validating standalone address or database behavior.

## Public workflow

1. Run `work start` with a concrete goal.
2. Add multiple `work step` records representing a meaningful decision/action and a verified result. Retain at least one source step ID.
3. Run `work checkpoint` and verify `work show` returns the bounded next action.
4. Run `session-end` with the structured session identity and a terminal status. Retain the episode and summary IDs.
5. Ask an explicit method-seeking question through ordinary `recall`, such as “How do I verify this release?”. Require a non-empty `how_recall`, `how_request_id`, and solution-path context.
6. Run `work recall` with the same task and require the finalized episode in `paths`.
7. Run `work promote` with the summary ID, a durable memory type, and the source step ID. Require a published, non-partial result.
8. Inspect solution activity/How History detail. Require an available promotion target under What and explicit step/evidence provenance; do not accept similarity-only grouping.
9. Write an unrelated memory and verify it remains available through the ungrouped-memory query.

## Dashboard runtime checks

- Launch standalone with a deliberately non-derived database filename and verify the fixed workspace resolves that exact path.
- Verify GET and HEAD of `/w/<workspace>/knowledge/history` return the embedded SPA shell after a direct request or refresh.
- Verify a mutation to the `/w/` client-route prefix is rejected.
- Verify default background start may reuse the hosted webapp, while `--force-local` bypasses discovery and honors the requested loopback address and database.

## Release gates

Run:

```text
go test ./...
go vet ./...
npm --prefix tools/agent-memory/mcp-server test
npm --prefix tools/agent-memory/dashboard test
npm --prefix tools/agent-memory/dashboard run typecheck
npm --prefix tools/agent-memory/dashboard run build
```

Report failures as product wiring gaps, including the exact public surface that failed. A stored episode alone is not success: ordinary recall, explicit recall, promotion, provenance-tree visibility, and standalone browser/runtime behavior must all work.
