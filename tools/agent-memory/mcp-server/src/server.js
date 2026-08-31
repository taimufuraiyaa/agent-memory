#!/usr/bin/env node
import readline from "node:readline";
import { randomUUID } from "node:crypto";

const protocolVersion = "2025-03-26";
const serviceURL = (process.env.AGENT_MEMORY_API_URL || process.env.AGENT_MEMORY_URL || "http://127.0.0.1:3210").replace(/\/$/, "");
const serviceMode = process.env.AGENT_MEMORY_MODE || "local";
const hostedToken = process.env.AGENT_MEMORY_TOKEN || "";
const hostedTenant = process.env.AGENT_MEMORY_TENANT_ID || process.env.AGENT_MEMORY_TENANT || "";
const maxResponseBytes = Number(process.env.AGENT_MEMORY_MCP_MAX_RESPONSE_BYTES || 262144);
const clientID = process.env.AGENT_MEMORY_CLIENT_ID || "";
const legacyProfile = process.env.AGENT_MEMORY_MCP_PROFILE || "default";

if (!new Set(["local", "hosted"]).has(serviceMode)) {
  process.stderr.write(`unsupported AGENT_MEMORY_MODE: ${serviceMode}\n`);
  process.exit(2);
}

let profile;
try {
  profile = await resolveToolProfile();
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : "cannot resolve MCP tool profile"}\n`);
  process.exit(2);
}

async function resolveToolProfile() {
  if (!clientID) {
    if (!new Set(["default", "expanded"]).has(legacyProfile)) {
      throw new Error(`unsupported AGENT_MEMORY_MCP_PROFILE: ${legacyProfile}`);
    }
    return legacyProfile;
  }
  if (!/^[a-z][a-z0-9_-]{0,63}$/.test(clientID)) {
    throw new Error("AGENT_MEMORY_CLIENT_ID must be a lowercase slug up to 64 characters");
  }
  if (serviceMode !== "local") {
    throw new Error("AGENT_MEMORY_CLIENT_ID profile resolution is available only in local mode");
  }
  let response;
  try {
    response = await fetch(`${serviceURL}/api/v1/client-profiles/${clientID}`, {
      method: "GET",
      headers: { accept: "application/json" },
      signal: AbortSignal.timeout(3000),
    });
  } catch {
    throw new Error(`cannot resolve AGENT_MEMORY_CLIENT_ID ${clientID}: local service is unavailable`);
  }
  if (!response.ok) {
    throw new Error(`cannot resolve AGENT_MEMORY_CLIENT_ID ${clientID}: service returned HTTP ${response.status}`);
  }
  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new Error(`cannot resolve AGENT_MEMORY_CLIENT_ID ${clientID}: service returned invalid JSON`);
  }
  const resolved = payload?.data?.profile;
  if (payload?.ok === false || resolved?.id !== clientID || !new Set(["default", "expanded"]).has(resolved?.tool_profile)) {
    throw new Error(`cannot resolve AGENT_MEMORY_CLIENT_ID ${clientID}: service returned an invalid client profile`);
  }
  return resolved.tool_profile;
}
if (serviceMode === "hosted" && (!hostedToken || !hostedTenant)) {
  process.stderr.write("hosted MCP mode requires AGENT_MEMORY_TOKEN and AGENT_MEMORY_TENANT; inject the token from an OS keyring-backed launcher\n");
  process.exit(2);
}

const allTools = [
  tool("memory_health", "Check the local agent-memory service", {}),
  tool("memory_write", "Store one durable memory", {
    content: { type: "string" },
    type: { type: "string", enum: ["episodic", "semantic", "procedural", "outcome"] },
    workspace: { type: "string" },
  }, ["content"]),
  tool("memory_search", "Search memories with bounded compact results", {
    query: { type: "string" },
    workspace: { type: "string" },
    top_k: { type: "integer", minimum: 1, maximum: 200 },
    cursor: { type: "string", maxLength: 2048 },
    format: { type: "string", enum: ["compact", "full"] },
    source_ids: { type: "array", items: { type: "string" } },
  }, ["query"]),
  tool("memory_recall", "Build token-budgeted context for a task", {
    task: { type: "string" },
    workspace: { type: "string" },
    top_k: { type: "integer", minimum: 1, maximum: 200 },
    budget: { type: "integer", minimum: 1, maximum: 32000 },
    format: { type: "string", enum: ["compact", "full"] },
    source_ids: { type: "array", items: { type: "string" } },
  }, ["task"]),
  tool("memory_feedback", "Record usefulness feedback for a retrieval request", {
    request_id: { type: "string" },
    score: { type: "integer", minimum: 0, maximum: 5 },
    reason: { type: "string" },
    useful_count: { type: "integer", minimum: 0 },
    total_count: { type: "integer", minimum: 0 },
    workspace: { type: "string" },
    memory_id: { type: "string" },
  }, ["request_id", "score"]),
  tool("memory_sessions", "List recent captured sessions", {
    workspace: { type: "string" },
    limit: { type: "integer", minimum: 1, maximum: 200 },
  }),
  tool("memory_session_end", "Extract durable learnings from a completed session", {
    transcript: { type: "string" },
    workspace: { type: "string" },
    session_id: { type: "string" },
    principal_id: { type: "string" },
    terminal_status: { type: "string", enum: ["completed", "partial", "abandoned", "cancelled"] },
    idempotency_key: { type: "string" },
  }),
  tool("solution_start", "Start a bounded solution episode", {
    workspace: { type: "string" }, session_id: { type: "string" }, principal_id: { type: "string" }, client_id: { type: "string" },
    goal_summary: { type: "string" }, capture_policy: { type: "string", enum: ["structured", "summary_only"] },
    retention_class: { type: "string", enum: ["transient", "standard", "pinned"] }, idempotency_key: { type: "string" },
  }, ["session_id", "principal_id", "client_id", "goal_summary"]),
  tool("solution_step", "Append a safe structured solution step", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" },
    kind: { type: "string", enum: ["hypothesis", "action", "observation", "decision", "checkpoint", "result", "handoff"] },
    status: { type: "string", enum: ["proposed", "running", "completed", "failed", "skipped"] }, summary: { type: "string" },
    rationale_summary: { type: "string" }, source: { type: "string" }, parent_step_ids: { type: "array", items: { type: "string" } },
    references: { type: "array", items: { type: "object" } }, confidence: { type: "number", minimum: 0, maximum: 1 },
    sensitivity: { type: "string", enum: ["public", "internal", "sensitive", "restricted"] }, idempotency_key: { type: "string" },
  }, ["principal_id", "episode_id", "kind", "status", "summary"]),
  tool("solution_checkpoint", "Checkpoint expiring bounded continuation state", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" }, expected_generation: { type: "integer", minimum: 0 },
    goal_summary: { type: "string" }, constraints: { type: "array", items: { type: "string" } }, plan_items: { type: "array", items: { type: "object" } },
    completed_items: { type: "array", items: { type: "string" } }, open_questions: { type: "array", items: { type: "string" } }, next_action: { type: "string" },
    artifacts: { type: "array", items: { type: "object" } }, sensitivity: { type: "string", enum: ["public", "internal", "sensitive", "restricted"] },
    ttl_seconds: { type: "integer", minimum: 1, maximum: 604800 },
  }, ["principal_id", "episode_id", "goal_summary"]),
  tool("solution_state", "Recover authorized current continuation state", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" },
  }, ["principal_id", "episode_id"]),
  tool("solution_transition", "Resume, pause, or end a solution episode", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" }, expected_version: { type: "integer", minimum: 1 },
    status: { type: "string", enum: ["active", "paused", "completed", "partial", "abandoned", "cancelled"] }, idempotency_key: { type: "string" },
  }, ["principal_id", "episode_id", "expected_version", "status"]),
  tool("solution_handoff", "Transfer a solution episode to another principal and session", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" }, expected_version: { type: "integer", minimum: 1 },
    target_principal_id: { type: "string" }, target_session_id: { type: "string" }, idempotency_key: { type: "string" },
  }, ["principal_id", "episode_id", "expected_version", "target_principal_id", "target_session_id"]),
  tool("solution_recall", "Recall bounded prior solution paths for a how-oriented task", {
    workspace: { type: "string" }, principal_id: { type: "string" }, session_id: { type: "string" }, task: { type: "string" },
    token_budget: { type: "integer", minimum: 1, maximum: 32000 }, max_candidates: { type: "integer", minimum: 1, maximum: 100 },
  }, ["task"]),
  tool("solution_promote", "Promote verified solution-path knowledge into durable memory", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" }, summary_id: { type: "string" },
    idempotency_key: { type: "string" }, targets: { type: "array", minItems: 1, maxItems: 8, items: { type: "object" } },
  }, ["principal_id", "episode_id", "summary_id", "targets"]),
  tool("solution_tool_event", "Record a safe tool event for validated lesson derivation", {
    workspace: { type: "string" }, principal_id: { type: "string" }, episode_id: { type: "string" }, step_id: { type: "string" },
    kind: { type: "string", enum: ["discovery", "selection", "invocation", "result"] }, tool_name: { type: "string" },
    tool_version: { type: "string" }, operation: { type: "string" }, capability: { type: "string" }, input_summary: { type: "string" },
    result_class: { type: "string", enum: ["success", "failure", "partial", "cancelled", "unknown"] }, task_verified: { type: "boolean" },
    duration_ms: { type: "integer", minimum: 0 }, evidence: { type: "array", items: { type: "object" } }, idempotency_key: { type: "string" },
  }, ["principal_id", "episode_id", "step_id", "tool_name", "operation", "capability"]),
  tool("solution_tool_lesson_derive", "Derive a bounded validated lesson from authorized tool events", {
    workspace: { type: "string" }, principal_id: { type: "string" }, event_ids: { type: "array", minItems: 1, maxItems: 100, items: { type: "string" } },
    fallback: { type: "string" }, reviewed: { type: "boolean" },
  }, ["principal_id", "event_ids"]),
  tool("solution_tool_lesson_promote", "Promote a verified tool lesson into durable procedural memory", {
    workspace: { type: "string" }, principal_id: { type: "string" }, lesson_id: { type: "string" }, idempotency_key: { type: "string" },
  }, ["principal_id", "lesson_id"]),
  tool("skill_list", "List logical skills and lifecycle state", { workspace: { type: "string" }, limit: { type: "integer", minimum: 1, maximum: 200 } }),
  tool("skill_inspect", "Inspect revisions and activation state for one logical skill", { workspace: { type: "string" }, skill_id: { type: "string" }, environment: { type: "string" } }, ["skill_id"]),
  tool("skill_propose", "Build an immutable draft from an authorized candidate", {
    workspace: { type: "string" }, actor: { type: "string" }, candidate_id: { type: "string" }, skill_name: { type: "string" },
    description: { type: "string" }, owner_group: { type: "string" }, files: { type: "object" }, removal_reasons: { type: "object" },
    compatibility: { type: "object" }, protected_sections: { type: "array", items: { type: "string" } },
  }, ["actor", "candidate_id", "files"]),
  tool("skill_resolve", "Resolve an exact authorized logical skill revision", {
    workspace: { type: "string" }, actor: { type: "string" }, environment: { type: "string" }, principal_id: { type: "string" }, task_id: { type: "string" },
    skill_id: { type: "string" }, explicit_revision_id: { type: "string" }, environment_revision_id: { type: "string" }, platform: { type: "string" },
    architecture: { type: "string" }, runtime_version: { type: "string" }, capabilities: { type: "array", items: { type: "string" } }, policy_version: { type: "integer", minimum: 1 },
    canary_basis_points: { type: "integer", minimum: 0, maximum: 10000 }, canary_approved: { type: "boolean" }, acknowledgement_supported: { type: "boolean" },
  }, ["actor", "principal_id", "task_id", "skill_id", "platform", "architecture", "runtime_version"]),
  tool("skill_acknowledge", "Acknowledge the exact loaded revision and digest", {
    workspace: { type: "string" }, actor: { type: "string" }, resolution_id: { type: "string" }, principal_id: { type: "string" }, task_id: { type: "string" }, revision_id: { type: "string" }, digest: { type: "string" }, token: { type: "string" },
  }, ["actor", "resolution_id", "principal_id", "task_id", "revision_id", "digest", "token"]),
  tool("skill_complete", "Complete acknowledged skill execution with bounded telemetry", {
    workspace: { type: "string" }, actor: { type: "string" }, id: { type: "string" }, resolution_id: { type: "string" }, episode_id: { type: "string" },
    outcome: { type: "string", enum: ["success", "failure", "partial", "cancelled"] }, independently_verified: { type: "boolean" }, failure_class: { type: "string" }, feedback_class: { type: "string" },
    started_at: { type: "string" }, completed_at: { type: "string" }, input_tokens: { type: "integer", minimum: 0 }, output_tokens: { type: "integer", minimum: 0 }, tool_calls: { type: "integer", minimum: 0 },
  }, ["actor", "id", "resolution_id", "episode_id", "outcome", "started_at", "completed_at"]),
  tool("skill_review", "Run an authorized evaluation, approval, canary, promotion, disable, pin, or rollback operation", {
    workspace: { type: "string" }, actor: { type: "string" }, operation: { type: "string", enum: ["evaluate", "approve", "canary", "promote", "disable", "pin", "rollback"] }, payload: { type: "object" },
  }, ["actor", "operation", "payload"]),
  tool("skill_orchestration_status", "Inspect one skill workflow with bounded job and event pages", {
    workspace: { type: "string" }, actor: { type: "string" }, workflow_id: { type: "string" }, environment: { type: "string" },
    job_cursor: { type: "string", maxLength: 2048 }, event_cursor: { type: "string", maxLength: 2048 }, limit: { type: "integer", minimum: 1, maximum: 200 },
  }, ["actor", "workflow_id"]),
  tool("skill_orchestration_control", "Pause, resume, cancel, reconcile, retry, replay, or drain skill orchestration work", {
    workspace: { type: "string" }, environment: { type: "string" }, actor: { type: "string" },
    action: { type: "string", enum: ["pause", "resume", "cancel", "reconcile", "retry", "replay", "drain"] },
    workflow_id: { type: "string" }, job_id: { type: "string" }, expected_generation: { type: "integer", minimum: 0 },
    reason_code: { type: "string" }, idempotency_key: { type: "string" }, limit: { type: "integer", minimum: 1, maximum: 200 },
  }, ["actor", "action"]),
];
const defaultToolNames = new Set([
  "memory_write",
  "memory_search",
  "memory_recall",
  "memory_feedback",
  "memory_session_end",
	"solution_start",
	"solution_step",
	"solution_checkpoint",
	"solution_state",
	"solution_transition",
	"solution_handoff",
	"solution_recall",
	"solution_promote",
]);
const tools = profile === "expanded"
  ? allTools
  : allTools.filter((definition) => defaultToolNames.has(definition.name));

function tool(name, description, properties, required = []) {
  return { name, description, inputSchema: { type: "object", properties, required, additionalProperties: false } };
}

function result(id, value) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result: value })}\n`);
}

function error(id, code, message, data) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id: id ?? null, error: { code, message, ...(data ? { data } : {}) } })}\n`);
}

function toolResult(id, data) {
  const withTransport = { ...data, _transport: { mode: serviceMode, protocol: "http", degraded: false } };
  const text = JSON.stringify(withTransport);
  if (Buffer.byteLength(text) > maxResponseBytes) {
    error(id, -32002, `service response exceeds ${maxResponseBytes} bytes`);
    return;
  }
  result(id, { content: [{ type: "text", text }], structuredContent: withTransport });
}

function validateArguments(definition, args) {
  const schema = definition.inputSchema;
  for (const name of schema.required || []) {
    if (args[name] === undefined || args[name] === "") throw new Error(`${name} is required`);
  }
  for (const name of Object.keys(args)) {
    if (!schema.properties[name]) throw new Error(`unknown argument: ${name}`);
  }
}

async function requestService(path, { method = "POST", body } = {}) {
  const headers = body ? { "content-type": "application/json" } : {};
  if (serviceMode === "hosted") {
    headers.authorization = `Bearer ${hostedToken}`;
    headers["x-agent-memory-tenant"] = hostedTenant;
    headers["idempotency-key"] = randomUUID();
  }
  const response = await fetch(`${serviceURL}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  const payload = await response.json();
  if (!response.ok || payload.ok === false) {
    throw new Error(payload.error?.message || `service returned HTTP ${response.status}`);
  }
  return payload.data ?? payload;
}

function compactSearch(data) {
  return {
    request_id: data.request_id,
    workspace: data.workspace,
    results: (data.results || []).map(({ id, type, content, score }) => ({ id, type, content, score })),
    total_tokens: data.total_tokens,
  };
}

function compactRecall(data) {
  return {
    request_id: data.request_id,
    context_block: data.context_block,
    tokens_used: data.tokens_used,
    tokens_budget: data.tokens_budget,
    workspace: data.workspace,
    memories_used: (data.memories_used || []).map((hit) => ({
      id: hit.memory?.id ?? hit.id,
      type: hit.memory?.type ?? hit.type,
      score: hit.score,
    })),
  };
}

async function callTool(name, args) {
  const definition = tools.find((candidate) => candidate.name === name);
  if (!definition) throw new Error(`unknown tool: ${name}`);
  validateArguments(definition, args);
  switch (name) {
    case "memory_health":
      return requestService(serviceMode === "hosted" ? "/health/live" : "/health", { method: "GET" });
    case "memory_write":
      if (serviceMode === "hosted") {
        if (!args.workspace) throw new Error("workspace is required in hosted mode");
        return requestService("/v1/memories", { body: { workspace_id: args.workspace, content: args.content, type: args.type || "semantic", source: { type: "user_input" } } });
      }
      return requestService("/api/v1/memories/write", { body: { ...args, type: args.type || "semantic" } });
    case "memory_search": {
      if (serviceMode === "hosted") {
        if (args.workspace) {
          const body = { workspace_id: args.workspace, query: args.query };
          if (args.top_k !== undefined) body.limit = args.top_k;
          if (args.cursor) body.cursor = args.cursor;
          const data = await requestService("/v1/search", { body });
          if (args.format === "full") return data;
          return {
            results: (data.items || []).map(({ id, type, content, score }) => ({ id, type, content, score })),
            ...(data.next_cursor ? { next_cursor: data.next_cursor } : {}),
          };
        }
        if (!args.source_ids?.length) throw new Error("workspace or source_ids are required in hosted mode");
        const data = await requestService("/v1/source-queries", { body: { source_ids: args.source_ids, query: args.query, limit: args.top_k, provider: "local-minilm-scaffold", model: "local-hash-v1" } });
        return args.format === "full" ? data : { request_id: data.request_id, results: (data.evidence || []).map(({ passage_id: id, text: content, score, citation_id }) => ({ id, type: "source_passage", content, score, citation_id })) };
      }
      const data = await requestService("/api/v1/memories/search", { body: args });
      return args.format === "full" ? data : compactSearch(data);
    }
    case "memory_recall": {
      if (serviceMode === "hosted") {
        if (!args.source_ids?.length) throw new Error("source_ids are required in hosted mode");
        const data = await requestService("/v1/source-queries", { body: { source_ids: args.source_ids, query: args.task, context_token_budget: args.budget, limit: args.top_k, provider: "local-minilm-scaffold", model: "local-hash-v1" } });
        return { request_id: data.request_id, context_block: (data.evidence || []).map((item) => `[${item.citation_id}] ${item.text}`).join("\n"), tokens_used: data.context?.used_tokens || 0, tokens_budget: data.context?.budget || args.budget, memories_used: data.evidence || [] };
      }
      const data = await requestService("/api/v1/memories/recall", { body: args });
      return args.format === "full" ? data : compactRecall(data);
    }
    case "memory_feedback":
      if (serviceMode === "hosted") {
        if (!args.memory_id) throw new Error("memory_id is required in hosted mode");
        return requestService(`/v1/memories/${encodeURIComponent(args.memory_id)}/feedback`, { body: { request_id: args.request_id, outcome: args.score >= 4 ? "helpful" : args.score <= 1 ? "rejected" : "ignored", reason_category: args.reason || "" } });
      }
      return requestService("/api/v1/requests/feedback", { body: args });
    case "memory_sessions": {
      if (serviceMode === "hosted") throw new Error("memory_sessions is not available in hosted compatibility v1");
      const query = new URLSearchParams();
      if (args.workspace) query.set("workspace", args.workspace);
      if (args.limit) query.set("limit", String(args.limit));
      return requestService(`/api/v1/sessions${query.size ? `?${query}` : ""}`, { method: "GET" });
    }
    case "memory_session_end":
      if (serviceMode === "hosted") {
        if (!args.session_id || !args.workspace) throw new Error("session_id and workspace are required in hosted mode");
        return requestService(`/v1/sessions/${encodeURIComponent(args.session_id)}/end`, { body: { workspace_id: args.workspace, transcript: args.transcript } });
      }
      if (!args.transcript && !(args.session_id && args.principal_id)) {
        throw new Error("transcript or session_id plus principal_id are required");
      }
      return requestService("/api/v1/memories/session-end", { body: args });
    case "solution_start":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/start", { body: {
        ...args, capture_policy: args.capture_policy || "structured", retention_class: args.retention_class || "standard",
        idempotency_key: args.idempotency_key || randomUUID(),
      } });
    case "solution_step":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/steps", { body: {
        ...args, source: args.source || "agent", confidence: args.confidence ?? 0.5,
        sensitivity: args.sensitivity || "internal", idempotency_key: args.idempotency_key || randomUUID(),
      } });
    case "solution_checkpoint":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/checkpoint", { body: {
        ...args, expected_generation: args.expected_generation ?? 0, sensitivity: args.sensitivity || "internal",
        ttl_seconds: args.ttl_seconds || 86400,
      } });
    case "solution_state": {
      requireLocalSolutionTool();
      const query = new URLSearchParams({ principal_id: args.principal_id, episode_id: args.episode_id });
      if (args.workspace) query.set("workspace", args.workspace);
      return requestService(`/api/v1/solutions/state?${query}`, { method: "GET" });
    }
    case "solution_transition":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/transition", { body: { ...args, idempotency_key: args.idempotency_key || randomUUID() } });
    case "solution_handoff":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/handoff", { body: { ...args, idempotency_key: args.idempotency_key || randomUUID() } });
    case "solution_recall":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/recall", { body: {
        ...args, token_budget: args.token_budget || 800, max_candidates: args.max_candidates || 50,
      } });
    case "solution_promote":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/promote", { body: { ...args, idempotency_key: args.idempotency_key || randomUUID() } });
    case "solution_tool_event":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/tool-events", { body: {
        ...args, kind: args.kind || "result", result_class: args.result_class || "unknown",
        task_verified: args.task_verified || false, idempotency_key: args.idempotency_key || randomUUID(),
      } });
    case "solution_tool_lesson_derive":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/tool-lessons/derive", { body: args });
    case "solution_tool_lesson_promote":
      requireLocalSolutionTool();
      return requestService("/api/v1/solutions/tool-lessons/promote", { body: { ...args, idempotency_key: args.idempotency_key || randomUUID() } });
    case "skill_list": {
      requireLocalSolutionTool(); const query = new URLSearchParams(); if (args.workspace) query.set("workspace", args.workspace); if (args.limit) query.set("limit", String(args.limit));
      return requestService(`/api/v1/skills/lifecycle/list${query.size ? `?${query}` : ""}`, { method: "GET" });
    }
    case "skill_inspect": {
      requireLocalSolutionTool(); const query = new URLSearchParams({ skill_id: args.skill_id, environment: args.environment || "local" }); if (args.workspace) query.set("workspace", args.workspace);
      return requestService(`/api/v1/skills/inspect?${query}`, { method: "GET" });
    }
    case "skill_propose": {
      requireLocalSolutionTool(); const { workspace, actor, ...payload } = args;
      return requestService("/api/v1/skills/lifecycle", { body: { operation: "propose", workspace, actor, payload } });
    }
    case "skill_resolve": {
      requireLocalSolutionTool(); const { workspace, actor, ...payload } = args;
      return requestService("/api/v1/skills/lifecycle", { body: { operation: "resolve", workspace, actor, payload: { ...payload, environment: payload.environment || "local", policy_version: payload.policy_version || 1, acknowledgement_supported: payload.acknowledgement_supported ?? true } } });
    }
    case "skill_acknowledge": {
      requireLocalSolutionTool(); const { workspace, actor, ...payload } = args;
      return requestService("/api/v1/skills/lifecycle", { body: { operation: "acknowledge", workspace, actor, payload } });
    }
    case "skill_complete": {
      requireLocalSolutionTool(); const { workspace, actor, ...payload } = args;
      return requestService("/api/v1/skills/lifecycle", { body: { operation: "complete", workspace, actor, payload } });
    }
    case "skill_review":
      requireLocalSolutionTool();
      return requestService("/api/v1/skills/lifecycle", { body: { operation: args.operation, workspace: args.workspace, actor: args.actor, payload: args.payload } });
    case "skill_orchestration_status": {
      requireLocalSkillOrchestrationTool();
      const query = new URLSearchParams();
      for (const key of ["workspace", "actor", "workflow_id", "environment", "job_cursor", "event_cursor", "limit"]) {
        if (args[key] !== undefined && args[key] !== "") query.set(key, String(args[key]));
      }
      return requestService(`/api/v1/skills/orchestration/status?${query}`, { method: "GET" });
    }
    case "skill_orchestration_control":
      requireLocalSkillOrchestrationTool();
      validateSkillOrchestrationControl(args);
      return requestService("/api/v1/skills/orchestration/control", { body: args });
    default:
      throw new Error(`tool execution is not available for ${name}`);
  }
}

function validateSkillOrchestrationControl(args) {
  if (new Set(["pause", "resume", "reconcile"]).has(args.action) && !args.workflow_id) {
    throw new Error(`workflow_id is required for ${args.action}`);
  }
  if (new Set(["cancel", "retry", "replay"]).has(args.action) && !args.job_id) {
    throw new Error(`job_id is required for ${args.action}`);
  }
  if (args.action === "replay" && (!args.reason_code || !args.idempotency_key)) {
    throw new Error("reason_code and idempotency_key are required for replay");
  }
}

function requireLocalSolutionTool() {
  if (serviceMode === "hosted") throw new Error("solution continuation tools are available only in local mode");
}

function requireLocalSkillOrchestrationTool() {
  if (serviceMode === "hosted") throw new Error("skill orchestration tools are available only in local mode");
}

async function dispatch(message) {
  switch (message.method) {
    case "initialize":
      result(message.id, {
        protocolVersion,
        capabilities: { tools: {} },
        serverInfo: { name: "agent-memory", version: "0.7.0" },
      });
      return;
    case "notifications/initialized":
      return;
    case "ping":
      result(message.id, {});
      return;
    case "tools/list":
      result(message.id, { tools });
      return;
    case "tools/call":
      try {
        toolResult(message.id, await callTool(message.params?.name, message.params?.arguments || {}));
      } catch (cause) {
        const detail = cause instanceof Error ? cause.message : String(cause);
        result(message.id, { content: [{ type: "text", text: `[transport=http degraded=true] ${detail}` }], isError: true });
      }
      return;
    default:
      if (message.id !== undefined) error(message.id, -32601, `method not found: ${message.method}`);
  }
}

const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
input.on("line", async (line) => {
  if (!line.trim()) return;
  try {
    await dispatch(JSON.parse(line));
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : String(cause);
    error(null, -32700, "parse error", message);
  }
});
