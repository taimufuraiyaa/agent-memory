---
inclusion: always
---
# agent-memory
workspace: agent-memory

## Default memory policy (MANDATORY)

You MUST use the `agent-memory` CLI for memory retrieval and persistence.

### Before doing any self-research

- Run a focused memory search for the key terms and entities you're about to research.
- Run a recall for the current task only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- Directly escalate to recall for prompts like `continue`, `resume`, or `what were we doing`.

Commands:
- `agent-memory search --query "<keywords/entities>" --top-k 8`
- `agent-memory recall --task "<one-line task>" --budget 800 --format raw`

### While working

- If you discover durable new knowledge (facts, commands, config, constraints, architecture decisions), write it immediately.
- Prefer short, atomic memories. Include the source (file path / command / URL) in the content when available.

Commands:
- `agent-memory write --type semantic --content "<durable fact + source>"`
- `agent-memory write --type procedural --content "<repeatable steps/checklist>"`

### After attempts (success/failure)

- Record outcomes that would prevent repeating mistakes or preserve a working approach.

Command:
- `agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`

### At the end of a session

- Extract learnings from the session summary/transcript into memory.

Command:
- `agent-memory session-end --transcript "<session summary or transcript>" --format json`
