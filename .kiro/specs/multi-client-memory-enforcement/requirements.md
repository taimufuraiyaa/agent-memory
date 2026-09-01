# Multi-client memory enforcement requirements

## Context

Agent Memory can store facts, locations, outcomes, feedback, and structured solution paths, but installation behavior differs by coding-agent host. A project owner needs one upgrade path that makes Kiro, Cursor, Codex, Claude Code, and supported generic agents aware of the same memory operating contract.

The contract records safe, concise summaries of decisions and actions. It must not request or persist private chain-of-thought.

## Terminology

- **What**: durable facts, outcomes, and artifacts.
- **Where**: source paths, commands, URLs, memory identifiers, and other locators.
- **When**: session, episode, step ordering, timestamps, checkpoints, and terminal status.
- **How**: a bounded solution episode containing safe action, observation, decision, rationale-summary, result, and handoff steps.
- **Feedback**: an honest usefulness score and corrective outcome attached to retrieval.
- **Operating contract**: the versioned, generated instructions every supported agent receives.
- **Host enforcement**: executable lifecycle hooks invoked by a client.
- **Instruction enforcement**: mandatory project rules that the client supplies to its model; obedience still depends on the host and model.

## Requirements

### R1 — One versioned operating contract

1. Generated rules for Kiro, Cursor, Codex, Claude Code, and generic supported agents SHALL describe What, Where, When, How, and Feedback.
2. The contract SHALL require search before self-research, immediate retrieval feedback, durable writes, and session finalization.
3. For non-trivial work, the contract SHALL require `work start`, meaningful `work step` records, periodic `work checkpoint`, a terminal transition, and `session-end`.
4. A future how-oriented task SHALL use `work recall`; verified reusable outcomes SHALL be eligible for `work promote`.
5. The contract SHALL explicitly prohibit storing private chain-of-thought and SHALL request only concise rationale summaries.
6. Every managed rule SHALL contain a stable contract-version marker so diagnostics can distinguish current from stale installations.
7. Every managed rule SHALL require agents in connected projects to invoke the PATH-installed `agent-memory` executable and SHALL explicitly prohibit guessing or using the source-repository-relative `./bin/agent-memory` path.
8. If `agent-memory` is unavailable on `PATH`, the contract SHALL require the agent to report the installation problem and use the documented install or repair flow instead of inventing a project-local executable path.

### R2 — Complete supported-client installation

1. `init --ide all` and `reinstall --ide all` SHALL install managed artifacts for Kiro, Cursor, Codex, and Claude Code in addition to existing generic targets.
2. Explicit `--ide kiro` SHALL be accepted.
3. Auto-detection SHALL recognize `.kiro/` as a Kiro installation signal.
4. Existing user-authored rule content and hook entries SHALL be preserved where managed sections are embedded in shared files.
5. Reinstallation SHALL be idempotent.
6. `upgrade --hooks-only --all --ide all` SHALL refresh the contract for every supported client in every registered project without replacing the binary.

### R3 — Executable lifecycle capture

1. Codex and Claude Code connections SHALL install both the operating contract and their supported lifecycle hooks.
2. Kiro SHALL receive prompt-submit and agent-stop hooks that direct the agent through retrieval, feedback, solution capture, and finalization.
3. Cursor SHALL receive an always-applied operating contract. The product SHALL not claim executable hook enforcement where Cursor exposes no compatible project hook surface.
4. Connection verification SHALL validate the current operating-contract marker as well as managed MCP/hook configuration.
5. Disconnect SHALL remove only Agent Memory-owned content and preserve unrelated user configuration.

### R4 — Solution tools available to ordinary agents

1. The default MCP profile SHALL expose the solution lifecycle tools required to capture, resume, recall, hand off, and promote How history.
2. The expanded profile SHALL continue to include operator/diagnostic tools.
3. Client-profile APIs and UI SHALL recognize Kiro alongside Codex, Claude, Cursor, and Other.
4. Dashboard copy and tests SHALL report tool-profile behavior accurately rather than relying on stale counts.

### R5 — Verifiable upgrade and truthful reporting

1. Diagnostics SHALL detect missing or stale managed rules for detected supported clients.
2. Diagnostics SHALL distinguish instruction-enforced rules from host-enforced lifecycle hooks.
3. Documentation SHALL give a project-wide upgrade command, client support matrix, restart guidance, and verification procedure.
4. Natural verification SHALL exercise public CLI/MCP surfaces in an isolated workspace for all four named clients.
5. The product SHALL not claim that prose rules can guarantee arbitrary model obedience.

## Acceptance criteria

- A clean isolated project upgraded with `--ide all` contains current managed artifacts for Kiro, Cursor, Codex, and Claude Code.
- Every named client receives the same versioned What/Where/When/How/Feedback contract.
- Hook-capable clients capture lifecycle events, while Cursor is accurately labeled instruction-enforced.
- The default MCP tool list includes the complete solution workflow.
- Tests prove idempotency, preservation of user-owned content, stale-contract detection, and multi-client coverage.
- Generated rules explicitly distinguish cross-project CLI invocation from source-repository development commands.
