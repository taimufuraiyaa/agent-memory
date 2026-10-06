---
name: jev-model-router
description: Use when the user asks Jev to recommend a model for a ChatGPT or Codex task, or asks whether a Jev model recommendation can be applied to the current chat. Do not invoke for ordinary task execution.
---

# Jev model recommendation in ChatGPT/Codex

Agent Memory's `chatgpt` catalog is a user-approved candidate list, not a live account entitlement. From a registered project, `agent-memory model-choice --workspace <name> --host chatgpt --allow-jev` accepts a bounded task on stdin and returns an advisory model ID. Run it only when the user explicitly authorizes sending that task and the catalog candidate descriptions to TypeSafe Jev; never interpolate private task text into shell command arguments or logs. If the catalog or token is missing, explain setup instead of inventing a model.

Verify the recommendation is available in this account's model picker. This skill cannot change the model of the current chat. Tell the user how to select the recommended model in the picker, or use a supported new-task model parameter only when the user explicitly asks to create a new task. Do not edit global model defaults as a substitute.

OpenAI API execution is a separate paid surface with its own `openai_api` catalog, API key, and `agent-memory model-run` command. Do not run it merely because the user asked for an app recommendation. Never claim API access from a ChatGPT subscription or app picker.
