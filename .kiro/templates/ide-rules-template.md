# agent-memory IDE Rules Template
# This is the single source of truth for IDE-specific agent rules
# Use scripts/generate-ide-rules.py to generate IDE-specific files

---
section: specs-first
applies_to: [cursorrules, aierules, windsurfrules, trae, claude]
---

## Specs-First (FORCED)
- For any non-trivial feature/bugfix, consult the relevant spec in `.kiro/specs/<feature>/` and derive the plan from `requirements.md` → `design.md` → `tasks.md`.
- If no relevant spec exists, create it (requirements first, then design, then tasks) before implementing code changes.
- If implementation maps to `.kiro/specs/**/tasks.md`, update the relevant checkbox items before finishing the work.

---
section: design-depth
applies_to: [cursorrules, aierules, windsurfrules, trae, claude]
---

## Design Depth (FORCED)
- Write `design.md` and `tasks.md` as a Solution Architect: alternatives, trade-offs, risks, edge cases, data contracts, failure modes, performance/scaling, and rollout strategy.
- Do NOT include example code in `design.md`.

---
section: mermaid-safety
applies_to: [cursorrules, aierules, windsurfrules, trae, claude]
---

## Mermaid Safety (ENFORCED)
- For any Mermaid diagram, ALWAYS enclose node labels in double quotes.
  - Correct: `Node["Process (step 1)"]`
  - Incorrect: `Node[Process (step 1)]`

---
section: truthfulness
applies_to: [cursorrules, aierules, windsurfrules, trae, claude]
---

## Truthfulness (ENFORCED)
- Do not guess or fabricate details. If information is missing, explicitly say what's missing and then proceed by inspecting the repo.

---
section: memory-policy-detailed
applies_to: [cursor]
---

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

---
section: memory-policy-compact
applies_to: [agents, antigravity, trae, claude, aierules, cursorrules, windsurfrules]
---

## agent-memory (MANDATORY)

workspace: agent-memory

Always use `agent-memory` as the memory system:
- Before doing any self-research: run memory `search` first. Run task `recall` only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `session-end`.

Commands:
- `agent-memory init`
- `agent-memory search --query "<keywords/entities>" --top-k 8`
- `agent-memory recall --task "<one-line task>" --budget 800 --format raw --include-observations`
- `agent-memory write --type semantic --content "<durable fact + source>"`
- `agent-memory write --type procedural --content "<repeatable steps/checklist>"`
- `agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`
- `agent-memory session-end --transcript "<session summary or transcript>" --format json`
