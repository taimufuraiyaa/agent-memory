---
name: am
description: Control Agent Memory's opt-in Claude Code prompt listener
disable-model-invocation: true
---

<!-- agent-memory managed Claude skill -->

Interpret $ARGUMENTS strictly:
- "listen" or "listen on": run `agent-memory listen on --workspace agent-memory`.
- "listen all": this explicitly opts into sending bounded, redacted future prompts and project skill names/descriptions to TypeSafe Jev as well as local recall. Run `agent-memory listen on --jev --workspace agent-memory` only for this explicit form.
- "listen off": run `agent-memory listen off --workspace agent-memory`.
- "listen status": run `agent-memory listen status --workspace agent-memory`.
- "jev on": compatibility alias for enabling Jev on an already-running local listener; explain remote prompt egress, then run `agent-memory listen jev-on --workspace agent-memory` only if explicitly requested.
- "jev off": compatibility alias for stopping only Jev advice; run `agent-memory listen jev-off --workspace agent-memory`.
- For anything else, explain the supported forms; do not run a command.

Report the command result. Plain listen is local-only unless Jev was already separately enabled; listen off stops both. With Jev listening and a private Claude model catalog, Agent Memory may route Claude Code Agent subtask calls to Jev's selected model. It does not switch the main session, automatically load another skill, or authorize an action.
