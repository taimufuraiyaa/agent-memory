#!/usr/bin/env node
import readline from "node:readline";

const protocolVersion = "2025-03-26";
const serviceURL = (process.env.AGENT_MEMORY_URL || "http://127.0.0.1:3210").replace(/\/$/, "");
const maxResponseBytes = Number(process.env.AGENT_MEMORY_MCP_MAX_RESPONSE_BYTES || 262144);

const tools = [
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
    format: { type: "string", enum: ["compact", "full"] },
  }, ["query"]),
  tool("memory_recall", "Build token-budgeted context for a task", {
    task: { type: "string" },
    workspace: { type: "string" },
    top_k: { type: "integer", minimum: 1, maximum: 200 },
    budget: { type: "integer", minimum: 1, maximum: 32000 },
    format: { type: "string", enum: ["compact", "full"] },
  }, ["task"]),
  tool("memory_feedback", "Record usefulness feedback for a retrieval request", {
    request_id: { type: "string" },
    score: { type: "integer", minimum: 0, maximum: 5 },
    reason: { type: "string" },
    useful_count: { type: "integer", minimum: 0 },
    total_count: { type: "integer", minimum: 0 },
    workspace: { type: "string" },
  }, ["request_id", "score"]),
  tool("memory_sessions", "List recent captured sessions", {
    workspace: { type: "string" },
    limit: { type: "integer", minimum: 1, maximum: 200 },
  }),
  tool("memory_session_end", "Extract durable learnings from a completed session", {
    transcript: { type: "string" },
    workspace: { type: "string" },
  }, ["transcript"]),
];

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
  const withTransport = { ...data, _transport: { mode: "http", degraded: false } };
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
  const response = await fetch(`${serviceURL}${path}`, {
    method,
    headers: body ? { "content-type": "application/json" } : undefined,
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
      return requestService("/health", { method: "GET" });
    case "memory_write":
      return requestService("/api/v1/memories/write", { body: { ...args, type: args.type || "semantic" } });
    case "memory_search": {
      const data = await requestService("/api/v1/memories/search", { body: args });
      return args.format === "full" ? data : compactSearch(data);
    }
    case "memory_recall": {
      const data = await requestService("/api/v1/memories/recall", { body: args });
      return args.format === "full" ? data : compactRecall(data);
    }
    case "memory_feedback":
      return requestService("/api/v1/requests/feedback", { body: args });
    case "memory_sessions": {
      const query = new URLSearchParams();
      if (args.workspace) query.set("workspace", args.workspace);
      if (args.limit) query.set("limit", String(args.limit));
      return requestService(`/api/v1/sessions${query.size ? `?${query}` : ""}`, { method: "GET" });
    }
    case "memory_session_end":
      return requestService("/api/v1/memories/session-end", { body: args });
    default:
      throw new Error(`tool execution is not available for ${name}`);
  }
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
