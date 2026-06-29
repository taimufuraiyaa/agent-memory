---
name: skill-packager
description: Package reusable learnings, scripts, or workflows into custom agent skills
---
# Skill Packager

Use this skill when you have solved a complex task, written a helper script, or established a reusable workflow that would benefit future agents.

## Self-Awareness Trigger Checklist
Ask yourself: *"Is this technique, script, or workflow generalizable/reusable for other workspaces or future agents?"*
You should package a skill after:
- Writing a complex Python or Bash script (e.g. calculation, cleanup, automation).
- Defining a complex grep/regex query or search command.
- Solving a difficult debugging case (e.g. permission issues, memory leaks).
- Implementing a multi-step architectural migration (e.g. DB schemas, plugins).

## Step-by-Step Packaging Process
1. **Choose a unique name**: Pick a descriptive, lowercase, kebab-case name (e.g., sqlite-blob-migration).
2. **Create the directory structure**:
   * Location: .agents/skills/<skill-name>/
   * Subdirectories: scripts/ (for helper scripts), examples/ (for usage examples), references/ (for detailed documentation).
3. **Write the SKILL.md file**:
   * It must contain YAML frontmatter:
     ```yaml
     ---
     name: <skill-name>
     description: <2-3 sentence summary of what this skill does and when to trigger it>
     ---
     ```
   * Write the body in clean Markdown, detailing instructions and references.
4. **Copy supporting assets**: Place any associated Python scripts, Bash scripts, or configs inside the scripts/ folder. Make sure scripts are executable and documented.
5. **Durable Memory Write**: Write a semantic memory in agent-memory explaining that this skill has been added, so future agents can search and retrieve it.
