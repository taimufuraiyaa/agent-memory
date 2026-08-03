# agent-memory — Project Instructions for AI Agents

## Rules (ENFORCED)
- Specs-first: follow `.kiro/specs/<feature>/requirements.md` → `design.md` → `tasks.md`.
- If no relevant spec exists for non-trivial work, create it before changing code.
- Mermaid: always quote node labels, e.g. `Node["Label (safe)"]`.
- Update spec task checkboxes in `.kiro/specs/**/tasks.md` when completing work.

---


## Specs-First (FORCED)
- For any non-trivial feature/bugfix, consult the relevant spec in `.kiro/specs/<feature>/` and derive the plan from `requirements.md` → `design.md` → `tasks.md`.
- If no relevant spec exists, create it (requirements first, then design, then tasks) before implementing code changes.
- If implementation maps to `.kiro/specs/**/tasks.md`, update the relevant checkbox items before finishing the work.

## Design Depth (FORCED)
- Write `design.md` and `tasks.md` as a Solution Architect: alternatives, trade-offs, risks, edge cases, data contracts, failure modes, performance/scaling, and rollout strategy.
- Do NOT include example code in `design.md`.

## Mermaid Safety (ENFORCED)
- For any Mermaid diagram, ALWAYS enclose node labels in double quotes.
  - Correct: `Node["Process (step 1)"]`
  - Incorrect: `Node[Process (step 1)]`

## Truthfulness (ENFORCED)
- Do not guess or fabricate details. If information is missing, explicitly say what's missing and then proceed by inspecting the repo.

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
- You MUST proactively package reusable scripts, grep queries, workflows, or complex setup/learnings into a custom agent skill under `.agents/skills/` (using `agent-memory distill` or manual packaging) if they are valuable and highly likely to be reused. Do NOT wait for the user to ask; proactively distill skills once a workflow or learning pattern is successfully validated.
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
- `agent-memory write --type outcome --content "<what you tried> (result: success|failure|partial, approach: <how>, reason: <why>)"`
- `agent-memory session-end --transcript "<session summary or transcript>" --format json`
