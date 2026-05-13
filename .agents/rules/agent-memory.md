# agent-memory
workspace: agent-memory

## agent-memory (MANDATORY)

workspace: agent-memory

Always use `agent-memory` as the memory system:
- Before doing any self-research: run memory `search`, then task `recall`.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `session-end`.

Commands:
- `rtk agent-memory init`
- `rtk agent-memory search --query "<keywords/entities>" --top-k 8`
- `rtk agent-memory recall --task "<one-line task>" --budget 800 --format raw`
- `rtk agent-memory write --type semantic --content "<durable fact + source>"`
- `rtk agent-memory write --type procedural --content "<repeatable steps/checklist>"`
- `rtk agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`
- `rtk agent-memory session-end --transcript "<session summary or transcript>" --format json`

