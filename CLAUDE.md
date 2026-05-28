# agent-memory — Project Instructions for AI Agents

## Rules (ENFORCED)
- Specs-first: follow `.kiro/specs/<feature>/requirements.md` → `design.md` → `tasks.md`.
- If no relevant spec exists for non-trivial work, create it before changing code.
- Mermaid: always quote node labels, e.g. `Node["Label (safe)"]`.
- Update spec task checkboxes in `.kiro/specs/**/tasks.md` when completing work.

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
