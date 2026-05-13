# RTK - Rust Token Killer (Google Antigravity)

Usage: Token-optimized CLI proxy for shell commands.

Rule

Always prefix shell commands with `rtk` to minimize token consumption.

Examples:

```bash
rtk git status
rtk cargo test
rtk ls src/
rtk grep "pattern" src/
rtk find "*.rs" .
rtk docker ps
rtk gh pr list
```

Meta Commands

```bash
rtk gain              # Show token savings
rtk gain --history    # Command history with savings
rtk discover          # Find missed RTK opportunities
rtk proxy <cmd>       # Run raw (no filtering, for debugging)
```

Why

RTK filters and compresses command output before it reaches the LLM context, saving 60-90% tokens on common operations. Always use `rtk <cmd>` instead of raw commands.

---

# agent-memory (MANDATORY)

Rule

Always use `agent-memory` as the memory system:
- Before doing any self-research: run memory `search`, then task `recall`.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `session-end`.

Commands

```bash
# one-time per repo
rtk agent-memory init

# before self-research
rtk agent-memory search --query "<keywords/entities>" --top-k 8
rtk agent-memory recall --task "<one-line task>" --budget 800 --format raw

# write back durable knowledge
rtk agent-memory write --type semantic --content "<durable fact + source>"
rtk agent-memory write --type procedural --content "<repeatable steps/checklist>"

# record outcomes (success/failure)
rtk agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"

# end of session
rtk agent-memory session-end --transcript "<session summary or transcript>" --format json
```
