# Solution-Path Memory Requirements

## Objective

Extend Agent Memory from remembering primarily **what** was learned and **where** it came from to also remembering **how** an agent reached a useful result. The system must preserve a compact, evidence-linked problem-solving path, tool-discovery lessons, and expiring working state so a future session can resume or reuse the method without replaying the whole transcript.

The feature is successful when a later agent can answer four questions from bounded recall:

1. What goal was being pursued?
2. Which approaches were tried, in what meaningful order, and what happened?
3. Which evidence, tools, scripts, or skills mattered?
4. What should the next agent repeat, avoid, or continue?

## Terminology

- **Working state:** Mutable, session-scoped goal, constraints, plan, open questions, and active artifact references. It expires automatically.
- **Solution episode:** One bounded attempt to solve a task, from declared goal through completion, abandonment, or handoff.
- **Solution step:** A concise externalizable record of a hypothesis, action, observation, decision, checkpoint, or result.
- **Solution path:** The ordered, validated subset of steps that explains the achieved result or the best-known partial result.
- **Tool lesson:** A capability, invocation constraint, failure mode, or selection rule learned while using a tool, function, script, or skill.
- **Promotion:** Conversion of validated solution-path knowledge into an existing episodic, semantic, procedural, or outcome memory, or into a skill artifact.

## Assumptions

1. “Remember how” means storing concise rationale summaries, actions, evidence, and outcomes. It does not mean storing hidden or raw chain-of-thought.
2. Existing memory types remain stable. Solution episodes and working state are supporting records with explicit promotion into durable memory.
3. Existing observations are admissible evidence, but an observation stream alone is not a solution path because it lacks task intent, causal structure, decision state, and validation.
4. Working state defaults to expiry 24 hours after the last session activity, with a configurable shorter duration and a hard maximum of 7 days unless explicitly promoted.
5. Completed solution-path summaries may be durable, but temporary prompts, raw tool payloads, secrets, and unbounded command output are never made durable by this feature.

## User Stories

- As an agent resuming interrupted work, I can recover the current goal, constraints, completed steps, open questions, and next action within a bounded token budget.
- As an agent facing a similar task, I can retrieve a prior successful solution path and understand which decisions and evidence made it work.
- As an agent choosing a tool, I can recall prior capability lessons, required setup, common failures, and safer alternatives.
- As an agent finishing work, I can finalize the episode and promote only verified, reusable knowledge.
- As a user, I can inspect, correct, pin, export, or delete solution-path records without exposing hidden model reasoning.
- As an operator, I can enforce retention, content admission, workspace isolation, and audit rules independently of the model provider.

## Functional Requirements

### R1 — Explicit Episode Lifecycle

- A client must be able to start, resume, checkpoint, complete, abandon, and hand off a solution episode.
- Starting an episode requires a workspace, session identity, goal summary, and capture policy.
- At most one episode may be active for the same workspace, session, and client unless the client explicitly supplies a separate episode identity.
- Repeated lifecycle requests with the same idempotency key must not create duplicate episodes or steps.
- Terminal episodes are immutable except for review metadata, retention, redaction, and supersession operations.

### R2 — Structured Solution Steps

- Each step must have a stable identity, episode identity, ordinal, kind, concise summary, status, timestamp, and source identity.
- A step may reference parent steps, observations, memories, files, tests, tool invocations, skills, and generated artifacts by stable identifier or bounded locator.
- Supported step kinds must include hypothesis, action, observation, decision, checkpoint, result, and handoff.
- Decision steps must record the chosen option, rejected alternatives when known, and a concise externally communicable rationale.
- Result steps must distinguish success, failure, partial, cancelled, and unknown states and may include verification evidence.
- The system must reject client-supplied ordinals that create ambiguous ordering and must preserve append order under concurrent writers.

### R3 — Safe Rationale Boundary

- The API and documentation must request concise rationale summaries and observable evidence, never private chain-of-thought or unrestricted reasoning transcripts.
- Admission must reject or quarantine requests explicitly labeled as hidden reasoning, chain-of-thought, scratchpad, internal monologue, or equivalent raw cognitive trace.
- Content must pass the shared origin-aware memory-admission policy for secrets, prompt injection, personal data, size limits, and unsafe procedural content before persistence or promotion.
- Tool inputs and outputs must be summarized and scrubbed by default. Full payload storage must be opt-in, bounded, separately classified, and disabled for secrets or credentials.
- Model-provider raw completion payloads are never part of the domain contract.

### R4 — Expiring Working State

- Working state must support the current goal, constraints, plan items, completed items, open questions, next action, and active artifact references.
- Updates must use optimistic concurrency so stale clients cannot silently overwrite newer state.
- Every working-state record must carry `expires_at`, last updater, generation, and sensitivity classification.
- Expired state must be excluded from recall immediately and removed by bounded lifecycle cleanup.
- Completion may distill selected working-state fields into a handoff or solution path, but it must not copy the entire working state automatically.
- Users and clients must be able to clear working state immediately.

### R5 — Tool and Skill Learning

- Tool-use steps must identify the logical tool or skill, operation, capability sought, summarized input shape, result class, duration when available, and evidence references.
- The system must distinguish tool discovery, tool selection, tool invocation, and tool result so that merely considering a tool does not imply it worked.
- A tool lesson must be derived from one or more validated steps and state the reusable capability, preconditions, limitations, failure modes, and confidence.
- Tool lessons may promote to procedural memory or seed skill distillation only after success evidence or explicit human review.
- Skill promotion must link back to the source episode and memories without duplicating the generated skill contents in the episode.

### R6 — Finalization and Promotion

- Finalization must produce a compact solution-path summary containing the goal, outcome, decisive steps, failed approaches worth avoiding, evidence, tool lessons, open risks, and reusable next-time guidance.
- Finalization must be deterministic when the client supplies a complete structured episode; model-assisted summarization is optional and its output remains an untrusted proposal until schema and policy validation pass.
- Promotions must use the existing write pipeline and create provenance edges back to the episode, steps, and observations.
- A single episode may yield zero or more existing memory types; it must not create a new durable memory type solely to bypass existing lifecycle rules.
- Re-finalization must create a new version or superseding summary rather than silently rewriting historical output.

### R7 — How-Oriented Recall

- Recall must support an explicit `how` intent in addition to existing factual and procedural retrieval.
- How-oriented recall must rank validated successful paths first, then relevant partial paths, tool lessons, and current working state when the caller is authorized for the active session.
- Recall output must separate current working state, prior solution paths, reusable procedures or skills, and warnings from failed approaches.
- Every included path must expose outcome, confidence, recency, evidence quality, and provenance.
- Token budgeting must prefer the compact path summary and decisive steps; verbose step detail is loaded only on request.
- Retrieval feedback must apply independently to a solution path and to any promoted memories or skills.

### R8 — Inspection and Control

- The unified Workspace Activity surface must list active and completed episodes without requiring a separate application.
- Episode detail must show the safe ordered path, outcome, linked evidence, promotions, retention state, and redactions.
- A user must be able to correct summaries, mark steps as misleading, supersede a path, pin an episode, or delete it subject to existing audit and retention policy.
- Hidden/private reasoning must never appear as an empty or locked UI section; it is outside the product data model entirely.

### R9 — Isolation, Authorization, and Audit

- Every episode, step, working-state record, tool lesson, and promotion must be workspace-scoped and principal-authorized.
- Session-scoped working state must not be visible to another principal merely because both principals can access the workspace.
- Hosted operations must enforce tenant and workspace boundaries; local-owner operations must resolve only registered projects and must not accept arbitrary database paths.
- Lifecycle transitions, policy rejections, promotions, redactions, and deletions must emit content-free audit metadata.
- Export and deletion must include solution-path records and their provenance links without resurrecting expired working state.

### R10 — Reliability and Compatibility

- Existing write, search, recall, feedback, session-end, observation, consolidation, import/export, and skill-distillation behavior must remain compatible.
- Clients that do not emit structured episode events must continue to work and may receive only current memory behavior.
- Partial capture must be valid: missing tool timing or unavailable observations cannot invalidate an otherwise useful episode.
- A failed finalization must leave the episode resumable and must not partially publish promoted memories without an explicit partial result.
- Cleanup, finalization, and promotion jobs must be restartable and idempotent.

## Commands

Run from the repository root unless a command says otherwise.

- Core and storage tests: `go test ./internal/core ./internal/storage/sqlite ./internal/application ./internal/engine`
- CLI and API tests: `go test ./internal/cli ./internal/api`
- Hosted isolation tests: `go test ./internal/saas/...`
- MCP server tests: `npm --prefix tools/agent-memory/mcp-server test`
- Dashboard tests: `npm --prefix tools/agent-memory/dashboard test`
- Dashboard type check: `npm --prefix tools/agent-memory/dashboard run typecheck`
- Dashboard build: `npm --prefix tools/agent-memory/dashboard run build`
- Full verification: `go test ./...`

## Project Structure

- `internal/core/`: episode, step, tool-lesson, working-state, and promotion contracts.
- `internal/storage/sqlite/`: migrations, repositories, expiry, provenance, and transactional ordering.
- `internal/application/`: lifecycle orchestration, authorization, finalization, recall assembly, and promotion.
- `internal/engine/`: admission, ranking, summarization proposal validation, and consolidation integration.
- `internal/cli/`: episode and working-state commands plus compatible session-end integration.
- `internal/api/` and `internal/saas/api/`: local and hosted HTTP contracts.
- `tools/agent-memory/mcp-server/`: client-facing workflow tools.
- `tools/agent-memory/dashboard/`: Activity episode list/detail and review controls.

## Boundaries

- Always preserve structured provenance, idempotency, workspace isolation, explicit expiry, safe summaries, and reviewable promotion.
- Ask before changing default retention beyond the assumed 24-hour working-state expiry or enabling full tool payload retention.
- Never store hidden/raw chain-of-thought, credentials, unrestricted tool output, arbitrary local paths from remote clients, or model output as verified fact without validation.

## Success Criteria

1. A session can checkpoint work, restart, and recover its goal, constraints, progress, open questions, and next action without replaying the transcript.
2. A completed episode can be recalled for a similar task as an ordered, evidence-linked explanation of what worked and what failed.
3. A verified tool lesson can become procedural memory or a skill seed with provenance to the episode that demonstrated it.
4. Expired working state is unavailable to recall and is eventually deleted, while promoted durable knowledge remains linked and valid.
5. Adversarial tests prove raw-chain-of-thought rejection, secret scrubbing, idempotent event handling, concurrent ordering, session privacy, and cross-workspace isolation.
6. Existing unstructured clients and all current memory types remain compatible.

## Open Questions for Product Review

1. Is 24 hours after last activity the correct default expiry for working state, or should expiry be tied strictly to session end?
2. Should safe completed step detail be retained indefinitely with lifecycle decay, or compacted after a fixed period once a solution-path summary is verified?
3. Should model-assisted finalization be enabled by default when a configured provider is ready, or remain explicitly requested?
4. Should the first release expose episode authoring only through CLI/MCP, or also allow manual episode editing in the dashboard?
