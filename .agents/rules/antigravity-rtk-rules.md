# agent-memory (MANDATORY)

Always use `agent-memory` as the memory system:
- Before doing any self-research: run memory `search` first. Run task `recall` only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- After running search or recall: you MUST immediately submit a feedback score from 0 (useless) to 5 (extremely helpful) using command `agent-memory feedback --request-id <request_id> --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`.
- If a query receives a low score (0 to 3), proceed to investigate. After finding the correct information:
  - If information was **missing**: Use `agent-memory write` to write the correct fact.
  - If a retrieved memory was **incorrect or out-of-date**:
    1. Write the new corrected memory first using `agent-memory write` and copy the new memory ID.
    2. Link the old incorrect memory to the new one by running `agent-memory feedback --memory-id <old_id> --outcome rejected --reconsolidation-action superseded --successor-memory-id <new_id> --reason "<explanation>"`.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `session-end`.

Commands

```bash
# one-time per repo
agent-memory init

# before self-research
agent-memory search --query "<keywords/entities>" --top-k 8
agent-memory recall --task "<one-line task>" --budget 800 --format raw --include-observations

# feedback score on query (required immediately after search/recall)
agent-memory feedback --request-id "<id>" --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>

# feedback correction on old out-of-date memory
agent-memory feedback --memory-id "<old_id>" --outcome rejected --reconsolidation-action superseded --successor-memory-id "<new_id>" --reason "<explanation>"

# write back durable knowledge
agent-memory write --type semantic --content "<durable fact + source>"
agent-memory write --type procedural --content "<repeatable steps/checklist>"

# record outcomes (success/failure)
agent-memory write --type outcome --content "<what you tried> (result: success|failure|partial, approach: <how>, reason: <why>)"

# end of session
agent-memory session-end --transcript "<session summary or transcript>" --format json
```

