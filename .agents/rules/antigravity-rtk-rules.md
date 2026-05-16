# agent-memory (MANDATORY)

Always use `agent-memory` as the memory system:
- Before doing any self-research: run memory `search`, then task `recall`.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `session-end`.

Commands

```bash
# one-time per repo
agent-memory init

# before self-research
agent-memory search --query "<keywords/entities>" --top-k 8
agent-memory recall --task "<one-line task>" --budget 800 --format raw

# write back durable knowledge
agent-memory write --type semantic --content "<durable fact + source>"
agent-memory write --type procedural --content "<repeatable steps/checklist>"

# record outcomes (success/failure)
agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"

# end of session
agent-memory session-end --transcript "<session summary or transcript>" --format json
```
