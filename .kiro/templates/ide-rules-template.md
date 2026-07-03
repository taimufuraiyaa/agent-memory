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

### After running search or recall

- You MUST immediately submit a feedback score from 0 (useless) to 5 (extremely helpful), indicating how many retrieved memories were useful.
- You MUST score honestly and objectively based on the true usefulness of the retrieved memories. Do not default to high scores.
- You MUST always provide a reason regardless of the score; if the score is below 4, you MUST provide a detailed explanation (the command will fail if reason is omitted for scores below 4).

Command:
- `agent-memory feedback --request-id "<request_id>" --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`

### While working

- If you discover durable new knowledge (facts, commands, config, constraints, architecture decisions), write it immediately.
- Prefer short, atomic memories. Include the source (file path / command / URL) in the content when available.
- Be self-aware of reusable scripts, grep queries, or workflows: package them into custom skills under `.agents/skills/` (using `agent-memory distill` or manual packaging) for later reuse.
  - Do NOT use generic, numbered, or index-based filenames (like `part1.md`, `workflows_part1.md`).
  - Always use clear, descriptive, and meaningful names for all custom skill reference files (e.g., `db_performance.md`, `ui_fixes.md`).
  - Limit every individual skill file's size strictly to a maximum of 12,000 characters. If a skill grows beyond this, partition it by domain/feature and place the detailed references into a `references/` subdirectory with descriptive, meaningful filenames.

Commands:
- `agent-memory write --type semantic --content "<durable fact + source>"`
- `agent-memory write --type procedural --content "<repeatable steps/checklist>"`

### Corrective action on low-scoring queries (Score 0-3)

If retrieval has a low score (0-3), proceed to investigate and solve the task. Once correct information is learned/verified:
- **Missing information**: Write a new memory with `agent-memory write`.
- **Out-of-date or incorrect memory**:
  1. Write the corrected memory entry using `agent-memory write` and copy the returned memory ID.
  2. Update the old memory by linking it to the new successor:
     `agent-memory feedback --memory-id <old_id> --outcome rejected --reconsolidation-action superseded --successor-memory-id <new_id> --reason "<explanation>"`

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
- After running search or recall: you MUST immediately submit a feedback score from 0 (useless) to 5 (extremely helpful) using command `agent-memory feedback --request-id <request_id> --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`. You MUST score honestly and objectively. If the score is below 4, you MUST provide a detailed explanation.
- If a query receives a low score (0 to 3), proceed to investigate. After finding the correct information:
  - If information was **missing**: Use `agent-memory write` to write the correct fact.
  - If a retrieved memory was **incorrect or out-of-date**:
    1. Write the new corrected memory first using `agent-memory write` and copy the new memory ID.
    2. Link the old incorrect memory to the new one by running `agent-memory feedback --memory-id <old_id> --outcome rejected --reconsolidation-action superseded --successor-memory-id <new_id> --reason "<explanation>"`.
- After learning durable new knowledge: write it to memory immediately.
- Be self-aware of reusable scripts, grep queries, or workflows: package them into custom skills under `.agents/skills/` (using `agent-memory distill` or manual packaging) for later reuse.
  - Do NOT use generic, numbered, or index-based filenames (like `part1.md`, `workflows_part1.md`).
  - Always use clear, descriptive, and meaningful names for all custom skill reference files (e.g., `db_performance.md`, `ui_fixes.md`).
  - Limit every individual skill file's size strictly to a maximum of 12,000 characters. If a skill grows beyond this, partition it by domain/feature and place the detailed references into a `references/` subdirectory with descriptive, meaningful filenames.
- At the end of a session: store a short session summary via `session-end`.

Commands:
- `agent-memory init`
- `agent-memory search --query "<keywords/entities>" --top-k 8`
- `agent-memory recall --task "<one-line task>" --budget 800 --format raw --include-observations`
- `agent-memory feedback --request-id "<id>" --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`
- `agent-memory feedback --memory-id "<old_id>" --outcome rejected --reconsolidation-action superseded --successor-memory-id "<new_id>" --reason "<explanation>"`
- `agent-memory write --type semantic --content "<durable fact + source>"`
- `agent-memory write --type procedural --content "<repeatable steps/checklist>"`
- `agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`
- `agent-memory session-end --transcript "<session summary or transcript>" --format json`

