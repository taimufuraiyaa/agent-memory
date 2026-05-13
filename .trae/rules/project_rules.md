## IDE / Codebase Agent Rules (ENFORCED)

### 1) Specs-First (forced)

For any non-trivial feature/bugfix, the agent MUST consult the relevant Kiro spec(s) and derive the implementation plan from `requirements.md` → `design.md` → `tasks.md` (under `.kiro/specs/<feature>/`).

If no relevant spec exists for the requested work, the agent MUST create a requirements-first spec (requirements/design/tasks) before implementing changes.

### 2) Solution Architect Depth (forced)

For any Kiro `design.md` and `tasks.md` the agent MUST write as a Solution Architect:
- Consider multiple approaches and alternatives.
- Document trade-offs, drawbacks, risks, and edge cases.
- Specify data contracts, failure modes, performance/scalability considerations, and rollout strategy where relevant.
- Do NOT include example code in `design.md`.

### 3) Kiro Specs: Architecture Flowchart (forced)

When creating or updating any Kiro spec `design.md`, the agent MUST include an architecture/system design flowchart section using a Mermaid diagram.

Mermaid safety rule: ALWAYS enclose node labels in double quotes to avoid syntax errors with special characters.

### 4) Task Progress Updates (forced)

If the agent is implementing work that maps to a defined Kiro spec task list (`.kiro/specs/**/tasks.md`), the agent MUST update the relevant checkbox items in that `tasks.md` to reflect completion before finishing the implementation.

### 5) Shell Commands MUST Use RTK (forced)

When suggesting or running shell commands, ALWAYS prefix the command with `rtk` to minimize token consumption.
