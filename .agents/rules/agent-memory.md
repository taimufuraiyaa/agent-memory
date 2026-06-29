---
trigger: always_on
---
# agent-memory
workspace: agent-memory

## agent-memory (MANDATORY)

workspace: agent-memory

Always use `agent-memory` as the memory system:
- Before doing any self-research: run memory `search` first. Run task `recall` only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- After running search or recall: you MUST immediately submit a feedback score from 0 (useless) to 5 (extremely helpful) using command `agent-memory feedback --request-id <request_id> --score <0-5>`. You MUST score honestly and objectively based on the true usefulness of the retrieved memories (e.g., score 0 if retrieved memories were completely irrelevant or did not help, score 5 if they directly contained the solution or crucial context). Do not default to high scores.
- After learning durable new knowledge: write it to memory immediately.
- Be self-aware of reusable scripts, grep queries, or workflows: package them into custom skills under `.agents/skills/` (using `agent-memory distill` or manual packaging) for later reuse.
- At the end of a session: store a short session summary via `session-end`.

Commands:
- `agent-memory init`
- `agent-memory search --query "<keywords/entities>" --top-k 8`
- `agent-memory recall --task "<one-line task>" --budget 800 --format raw --include-observations`
- `agent-memory feedback --request-id "<id>" --score <0-5>`
- `agent-memory write --type semantic --content "<durable fact + source>"`
- `agent-memory write --type procedural --content "<repeatable steps/checklist>"`
- `agent-memory write --type outcome --content "<what you tried> (result: success|failure|partial, approach: <how>, reason: <why>)"`
- `agent-memory session-end --transcript "<session summary or transcript>" --format json`

