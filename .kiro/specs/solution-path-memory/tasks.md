# Solution-Path Memory Implementation Tasks

## Phase 1 — Contracts and Safe Persistence

- [x] **Task 1: Freeze episode and step domain invariants**
  - Acceptance: Domain types cover episode lifecycle, step kinds and outcomes, references, capture policy, tool identity, summary versions, and promotion states; raw-reasoning fields do not exist.
  - Verify: `go test ./internal/core -run 'TestSolutionEpisode|TestSolutionStep|TestWorkingState'`
  - Dependencies: None.
  - Files: core contracts and focused tests.
  - Estimated scope: Small.

- [x] **Task 2: Add additive SQLite episode storage**
  - Acceptance: Migrations and repositories persist episodes, append server-ordered steps transactionally, page by ordinal, and leave existing databases compatible.
  - Verify: migration round-trip, concurrent append, duplicate idempotency, and paging tests in `internal/storage/sqlite`.
  - Dependencies: Task 1.
  - Files: schema migration, episode repository, repository tests, fixtures.
  - Estimated scope: Medium.

- [x] **Task 3: Enforce solution-content admission**
  - Acceptance: The shared origin-aware policy bounds and classifies episode fields, rejects explicit raw chain-of-thought and secrets, supports typed redaction/quarantine, and emits content-free audit outcomes.
  - Verify: `go test ./internal/engine ./internal/application -run 'TestSolutionAdmission|TestRationaleBoundary'`
  - Dependencies: Task 1.
  - Files: admission policy extension, orchestration boundary, adversarial fixtures, tests.
  - Estimated scope: Medium.

### Checkpoint A — Safe Foundation

- [x] Existing workspace databases migrate without changing current memory or observation behavior.
- [x] Concurrent and duplicate events produce stable ordering and no duplicate steps.
- [x] Chain-of-thought, secret, injection, oversize, and cross-workspace fixtures fail closed.

## Phase 2 — Live Continuation

- [x] **Task 4: Deliver explicit episode lifecycle service**
  - Acceptance: Start, resume, pause, handoff, complete, partial, abandon, and cancel enforce authorized transitions and optimistic episode versioning.
  - Verify: application service transition-table, idempotency, unknown-reference, and authorization tests.
  - Dependencies: Checkpoint A.
  - Files: application service, authorization adapter, tests, audit mapping.
  - Estimated scope: Medium.

- [x] **Task 5: Add expiring working state**
  - Acceptance: Bounded goal, constraints, plan, open questions, next action, and artifact references use compare-and-swap updates; expired state is immediately unreadable and cleanup is bounded.
  - Verify: controllable-clock expiry, stale-generation conflict, clear, cleanup restart, and session-privacy tests.
  - Dependencies: Tasks 2 and 4.
  - Files: core working-state contract, SQLite repository, application service, tests.
  - Estimated scope: Medium.

- [x] **Task 6: Expose local CLI and MCP continuation operations**
  - Acceptance: Clients can start an episode, append safe steps, checkpoint state, inspect current state, and end or hand off using stable JSON contracts; old MCP profiles remain compatible.
  - Verify: `go test ./internal/cli -run 'TestWork'` and `npm --prefix tools/agent-memory/mcp-server test`.
  - Dependencies: Tasks 4-5.
  - Files: CLI commands, HTTP mapping, MCP tools, contract tests.
  - Estimated scope: Medium.

### Checkpoint B — Interrupted Task Resume

- [x] A local session can checkpoint, restart, and recover bounded current state without transcript replay.
- [x] Another principal cannot read active state without an audited handoff.
- [x] Existing clients operate unchanged when episode capability is absent.

## Phase 3 — Evidence and Finalization

- [x] **Task 7: Link observations and artifacts to solution steps**
  - Acceptance: Explicit references validate same-workspace/session scope, preserve tombstoned evidence, and support bounded correlation proposals without asserting causality.
  - Verify: explicit link, ambiguous correlation, deleted observation, cross-session, and cross-workspace tests.
  - Dependencies: Checkpoint B.
  - Files: reference repository, correlation service, provenance queries, tests.
  - Estimated scope: Medium.

- [ ] **Task 8: Add deterministic solution-path finalization**
  - Acceptance: A terminal snapshot yields a versioned summary of outcome, decisive steps, useful failures, evidence, risks, and next guidance; retry and supersession are idempotent.
  - Verify: golden structured episodes, partial evidence, crash retry, re-finalization, and bounded-size tests.
  - Dependencies: Task 7.
  - Files: finalizer, summary repository, assembler, tests.
  - Estimated scope: Medium.

- [ ] **Task 9: Promote validated paths through the existing write pipeline**
  - Acceptance: Selected summaries create existing episodic, semantic, procedural, or outcome memories with episode/step/observation provenance; failures report exact partial state and retry safely.
  - Verify: promotion matrix, poison-policy parity, duplicate retry, provenance integrity, and partial write tests.
  - Dependencies: Tasks 3 and 8.
  - Files: promotion orchestrator, provenance storage, pipeline adapter, tests.
  - Estimated scope: Medium.

### Checkpoint C — Remember How

- [ ] A successful episode becomes an inspectable, evidence-linked solution path and selected durable memories.
- [ ] A failed episode can record an avoid lesson without claiming success.
- [ ] No finalization path bypasses admission, provenance, or existing memory lifecycle rules.

## Phase 4 — Tool Learning and How Recall

- [ ] **Task 10: Derive validated tool lessons**
  - Acceptance: Tool discovery, selection, invocation, and result remain distinct; lessons state capability, preconditions, limitations, failures, fallback, evidence, and validation state.
  - Verify: considered-only, failed invocation, task-unverified success, repeated success, and conflicting-version tests.
  - Dependencies: Checkpoint C.
  - Files: tool contracts, derivation service, repository, tests.
  - Estimated scope: Medium.

- [ ] **Task 11: Connect tool lessons to skill distillation**
  - Acceptance: Reviewed or success-evidenced lessons can seed procedural memory and skill packaging with source-episode provenance and without duplicating skill contents.
  - Verify: `go test ./internal/workspace ./internal/cli -run 'Test.*Distill|TestToolLessonPromotion'`.
  - Dependencies: Task 10.
  - Files: distillation input adapter, promotion records, tests, generated-skill metadata.
  - Estimated scope: Small.

- [ ] **Task 12: Deliver how-oriented retrieval and recall assembly**
  - Acceptance: Retrieval ranks validated paths, partial paths, tool lessons, procedures, skills, and authorized current state; output sections remain bounded and expose evidence quality and warnings.
  - Verify: ranking fixtures, token-budget tests, harmful suppression, current-session privacy, and retrieval feedback tests.
  - Dependencies: Tasks 8-11.
  - Files: retrieval candidate source, ranker, recall assembly, feedback mapping, tests.
  - Estimated scope: Medium.

### Checkpoint D — Reuse and Skill Promotion

- [ ] A similar task recalls what worked, decisive evidence, useful failed approaches, and applicable tool or skill guidance.
- [ ] Feedback on paths remains independent from feedback on promoted memories.
- [ ] Repeated validated tool use can produce a reviewable skill seed with full provenance.

## Phase 5 — Product Surface and Hosted Parity

- [ ] **Task 13: Add Activity episode inspection and review**
  - Acceptance: The unified Workspace Activity surface lists and opens episodes, shows only safe steps and provenance, and supports correction, misleading-step feedback, pin, supersession, redaction, and deletion.
  - Verify: dashboard component, keyboard, responsive, redaction, and cross-workspace gateway tests.
  - Dependencies: Checkpoint D.
  - Files: gateway types, Activity episode slice, detail panel, tests.
  - Estimated scope: Medium.

- [ ] **Task 14: Add hosted lifecycle and isolation parity**
  - Acceptance: Tenant/workspace/principal authorization covers every episode record, handoff, finalization, recall, export, and deletion operation; local-owner routing accepts only registered project identities.
  - Verify: two-tenant, two-principal, unknown-project, arbitrary-path, timing-signal, and capability tests in hosted and local APIs.
  - Dependencies: Tasks 6, 9, 12, and 13.
  - Files: hosted service and handlers, local-project adapter, authorization tests, contract fixtures.
  - Estimated scope: Medium.

- [ ] **Task 15: Integrate session-end and compatibility fallback**
  - Acceptance: Structured active episodes finalize through the new path, sessions without episodes retain current heuristic extraction, and both flows report explicit partial failures.
  - Verify: current session-end golden tests plus structured, mixed-client, retry, and no-provider fixtures.
  - Dependencies: Tasks 8-9 and 14.
  - Files: session-end coordinator, CLI/API adapters, integration tests.
  - Estimated scope: Medium.

### Final Checkpoint — Release Gate

- [ ] Core, storage, engine, application, CLI, API, hosted, MCP, and dashboard focused suites pass.
- [ ] `go test ./...`, dashboard type checking, production build, and embedded dashboard verification pass.
- [ ] Security gates cover raw-reasoning rejection, secret redaction, prompt injection, session privacy, tenant isolation, registered-root resolution, export, and deletion.
- [ ] Evaluation shows better interrupted-task resume and similar-task reuse without increased sensitive-content retention.
- [ ] Product review resolves default working-state expiry, completed-step compaction, model-assisted finalization default, and first-release editing scope.
