import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import http from "node:http";
import test from "node:test";

function startServer(env = {}) {
  return spawn(process.execPath, ["src/server.js"], {
    cwd: new URL("..", import.meta.url),
    env: { ...process.env, ...env },
    stdio: ["pipe", "pipe", "pipe"],
  });
}

function readMessage(child) {
  return new Promise((resolve, reject) => {
    let buffer = "";
    const timer = setTimeout(() => reject(new Error("timed out waiting for MCP response")), 3000);
    child.stdout.on("data", (chunk) => {
      buffer += chunk.toString("utf8");
      const newline = buffer.indexOf("\n");
      if (newline >= 0) {
        clearTimeout(timer);
        resolve(JSON.parse(buffer.slice(0, newline)));
      }
    });
    child.once("error", reject);
  });
}

test("initializes over stdio without protocol noise", async (t) => {
  const child = startServer();
  t.after(() => child.kill());
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-03-26" } })}\n`);

  const response = await readMessage(child);

  assert.equal(response.id, 1);
  assert.equal(response.result.protocolVersion, "2025-03-26");
  assert.equal(response.result.serverInfo.name, "agent-memory");
  assert.deepEqual(response.result.capabilities, { tools: {} });
});

test("lists memory and complete solution workflow tools by default", async (t) => {
  const child = startServer();
  t.after(() => child.kill());
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} })}\n`);

  const response = await readMessage(child);

  assert.deepEqual(response.result.tools.map((tool) => tool.name), [
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
});

test("lists operational tools only in the expanded profile", async (t) => {
  const child = startServer({ AGENT_MEMORY_MCP_PROFILE: "expanded" });
  t.after(() => child.kill());
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 20, method: "tools/list", params: {} })}\n`);

  const response = await readMessage(child);

  assert.deepEqual(response.result.tools.map((tool) => tool.name), [
    "memory_health",
    "memory_write",
    "memory_search",
    "memory_recall",
    "memory_feedback",
    "memory_sessions",
    "memory_session_end",
    "solution_start",
    "solution_step",
    "solution_checkpoint",
    "solution_state",
    "solution_transition",
    "solution_handoff",
    "solution_recall",
    "solution_promote",
	"solution_tool_event",
	"solution_tool_lesson_derive",
	"solution_tool_lesson_promote",
  ]);
});

test("proxies default solution continuation tools", async (t) => {
  const requests = [];
  const server = http.createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      requests.push({ method: request.method, url: request.url, body: body ? JSON.parse(body) : null });
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ ok: true, version: "v1", data: { episode: { id: "ep-1", status: "active" } } }));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({ AGENT_MEMORY_URL: `http://127.0.0.1:${server.address().port}` });
  t.after(() => child.kill());

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 22, method: "tools/call", params: { name: "solution_start", arguments: {
    workspace: "ws", session_id: "s1", principal_id: "p1", client_id: "codex", goal_summary: "Continue safely", idempotency_key: "start-1",
  } } })}\n`);
  const response = await readMessage(child);

  assert.equal(response.result.structuredContent.episode.id, "ep-1");
  assert.deepEqual(requests[0], {
    method: "POST", url: "/api/v1/solutions/start",
    body: { workspace: "ws", session_id: "s1", principal_id: "p1", client_id: "codex", goal_summary: "Continue safely", idempotency_key: "start-1", capture_policy: "structured", retention_class: "standard" },
  });

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 23, method: "tools/call", params: { name: "solution_recall", arguments: {
    workspace: "ws", task: "How did we continue safely?",
  } } })}\n`);
  await readMessage(child);
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 24, method: "tools/call", params: { name: "solution_promote", arguments: {
    workspace: "ws", principal_id: "p1", episode_id: "ep-1", summary_id: "sum-1", targets: [{ memory_type: "procedural" }], idempotency_key: "promote-1",
  } } })}\n`);
  await readMessage(child);
  assert.deepEqual(requests.slice(1), [
    { method: "POST", url: "/api/v1/solutions/recall", body: { workspace: "ws", task: "How did we continue safely?", token_budget: 800, max_candidates: 50 } },
    { method: "POST", url: "/api/v1/solutions/promote", body: { workspace: "ws", principal_id: "p1", episode_id: "ep-1", summary_id: "sum-1", targets: [{ memory_type: "procedural" }], idempotency_key: "promote-1" } },
  ]);
});

test("proxies tool lesson capture only in the expanded profile", async (t) => {
  const requests = [];
  const server = http.createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      requests.push({ url: request.url, body: JSON.parse(body) });
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ ok: true, data: {} }));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({ AGENT_MEMORY_MCP_PROFILE: "expanded", AGENT_MEMORY_URL: `http://127.0.0.1:${server.address().port}` });
  t.after(() => child.kill());

  for (const [id, name, args] of [
    [51, "solution_tool_event", { principal_id: "p1", episode_id: "ep1", step_id: "st1", tool_name: "safe-tool", operation: "verify", capability: "verify artifact" }],
    [52, "solution_tool_lesson_derive", { principal_id: "p1", event_ids: ["ev1", "ev2"] }],
    [53, "solution_tool_lesson_promote", { principal_id: "p1", lesson_id: "lesson1", idempotency_key: "promote1" }],
  ]) {
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method: "tools/call", params: { name, arguments: args } })}\n`);
    await readMessage(child);
  }

  assert.deepEqual(requests.map((request) => request.url), [
    "/api/v1/solutions/tool-events", "/api/v1/solutions/tool-lessons/derive", "/api/v1/solutions/tool-lessons/promote",
  ]);
  assert.equal(requests[0].body.kind, "result");
  assert.equal(requests[0].body.result_class, "unknown");
  assert.equal(requests[2].body.idempotency_key, "promote1");
});

test("rejects calls to tools hidden from the default profile", async (t) => {
  const child = startServer();
  t.after(() => child.kill());
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 21, method: "tools/call", params: { name: "memory_health", arguments: {} } })}\n`);

  const response = await readMessage(child);

  assert.equal(response.result.isError, true);
  assert.match(response.result.content[0].text, /unknown tool: memory_health/);
});

test("fails closed for an unsupported tool profile", async () => {
  const child = startServer({ AGENT_MEMORY_MCP_PROFILE: "everything" });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk.toString("utf8"); });

  const exitCode = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", resolve);
  });

  assert.notEqual(exitCode, 0);
  assert.match(stderr, /unsupported AGENT_MEMORY_MCP_PROFILE: everything/);
});

test("resolves distinct persisted profiles by client id before listing tools", async (t) => {
  const server = http.createServer((request, response) => {
    const id = request.url.split("/").at(-1);
    const toolProfile = id === "claude" ? "expanded" : "default";
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ ok: true, version: "v1", data: { profile: { id, tool_profile: toolProfile } } }));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const serviceURL = `http://127.0.0.1:${server.address().port}`;

  const codex = startServer({ AGENT_MEMORY_CLIENT_ID: "codex", AGENT_MEMORY_URL: serviceURL });
  const claude = startServer({ AGENT_MEMORY_CLIENT_ID: "claude", AGENT_MEMORY_URL: serviceURL });
  t.after(() => codex.kill());
  t.after(() => claude.kill());
  codex.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 40, method: "tools/list", params: {} })}\n`);
  claude.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 41, method: "tools/list", params: {} })}\n`);

  const [codexResponse, claudeResponse] = await Promise.all([readMessage(codex), readMessage(claude)]);
	assert.equal(codexResponse.result.tools.length, 13);
	assert.equal(claudeResponse.result.tools.length, 18);
  assert.ok(claudeResponse.result.tools.some((tool) => tool.name === "memory_sessions"));
  assert.ok(claudeResponse.result.tools.some((tool) => tool.name === "solution_checkpoint"));
});

test("persisted client profile is authoritative over the legacy profile variable", async (t) => {
  const server = http.createServer((_request, response) => {
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ ok: true, data: { profile: { id: "codex", tool_profile: "default" } } }));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({
    AGENT_MEMORY_CLIENT_ID: "codex",
    AGENT_MEMORY_MCP_PROFILE: "everything",
    AGENT_MEMORY_URL: `http://127.0.0.1:${server.address().port}`,
  });
  t.after(() => child.kill());
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 42, method: "tools/list", params: {} })}\n`);

	assert.equal((await readMessage(child)).result.tools.length, 13);
});

test("fails closed when an explicit client id cannot be resolved", async (t) => {
  const server = http.createServer((_request, response) => {
    response.writeHead(404, { "content-type": "application/json" });
    response.end(JSON.stringify({ ok: false, error: { code: "client_profile_not_found", message: "missing" } }));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({ AGENT_MEMORY_CLIENT_ID: "missing", AGENT_MEMORY_URL: `http://127.0.0.1:${server.address().port}` });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk.toString("utf8"); });

  const exitCode = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", resolve);
  });
  assert.notEqual(exitCode, 0);
  assert.match(stderr, /cannot resolve AGENT_MEMORY_CLIENT_ID missing: service returned HTTP 404/);
  assert.doesNotMatch(stderr, /client_profile_not_found|missing"/);
});

test("rejects malformed and hosted client-id resolution", async () => {
  for (const env of [
    { AGENT_MEMORY_CLIENT_ID: "Bad ID" },
    { AGENT_MEMORY_CLIENT_ID: "codex", AGENT_MEMORY_MODE: "hosted", AGENT_MEMORY_TOKEN: "token", AGENT_MEMORY_TENANT: "tenant" },
  ]) {
    const child = startServer(env);
    let stderr = "";
    child.stderr.on("data", (chunk) => { stderr += chunk.toString("utf8"); });
    const exitCode = await new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("exit", resolve);
    });
    assert.notEqual(exitCode, 0);
    assert.match(stderr, /AGENT_MEMORY_CLIENT_ID/);
  }
});

test("proxies health, write, and compact search to the service", async (t) => {
  const requests = [];
  const server = http.createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      requests.push({ method: request.method, url: request.url, body: body ? JSON.parse(body) : null });
      const data = request.url === "/health"
        ? { status: "ok" }
        : request.url.endsWith("/search")
          ? { request_id: "req-1", results: [{ id: "m1", type: "semantic", content: "order events", score: 0.9, extra: "drop" }] }
          : { id: "m1", storage_tier: "vector" };
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ ok: true, version: "v1", data }));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const address = server.address();
  const child = startServer({
    AGENT_MEMORY_MCP_PROFILE: "expanded",
    AGENT_MEMORY_URL: `http://127.0.0.1:${address.port}`,
  });
  t.after(() => child.kill());

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 3, method: "tools/call", params: { name: "memory_health", arguments: {} } })}\n`);
  const health = await readMessage(child);
  assert.equal(health.result.structuredContent.status, "ok");

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 4, method: "tools/call", params: { name: "memory_write", arguments: { workspace: "ws", content: "remember this", type: "semantic" } } })}\n`);
  const write = await readMessage(child);
  assert.equal(write.result.structuredContent.id, "m1");

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 5, method: "tools/call", params: { name: "memory_search", arguments: { workspace: "ws", query: "orders", format: "compact" } } })}\n`);
  const search = await readMessage(child);
  assert.deepEqual(search.result.structuredContent.results[0], { id: "m1", type: "semantic", content: "order events", score: 0.9 });
  assert.deepEqual(requests.map((request) => [request.method, request.url]), [
    ["GET", "/health"],
    ["POST", "/api/v1/memories/write"],
    ["POST", "/api/v1/memories/search"],
  ]);
});

test("proxies recall, feedback, sessions, and session end", async (t) => {
  const requests = [];
  const server = http.createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      requests.push({ url: request.url, body: body ? JSON.parse(body) : null });
      let data;
      if (request.url.startsWith("/api/v1/sessions")) data = { sessions: [{ session_id: "s1" }] };
      else if (request.url.endsWith("/recall")) data = { request_id: "req-2", context_block: "remembered", tokens_used: 2, memories_used: [{ memory: { id: "m1" }, score: 0.8 }] };
      else if (request.url.endsWith("/feedback")) data = { ok: true, request_id: "req-2" };
      else data = { total_extracted: 1, written_ids: ["m2"] };
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ ok: true, version: "v1", data }));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({
    AGENT_MEMORY_MCP_PROFILE: "expanded",
    AGENT_MEMORY_URL: `http://127.0.0.1:${server.address().port}`,
  });
  t.after(() => child.kill());

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 6, method: "tools/call", params: { name: "memory_recall", arguments: { workspace: "ws", task: "continue work", budget: 100 } } })}\n`);
  assert.equal((await readMessage(child)).result.structuredContent.context_block, "remembered");

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 7, method: "tools/call", params: { name: "memory_feedback", arguments: { workspace: "ws", request_id: "req-2", score: 5 } } })}\n`);
  assert.equal((await readMessage(child)).result.structuredContent.ok, true);

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 8, method: "tools/call", params: { name: "memory_sessions", arguments: { workspace: "ws", limit: 5 } } })}\n`);
  assert.equal((await readMessage(child)).result.structuredContent.sessions[0].session_id, "s1");

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 9, method: "tools/call", params: { name: "memory_session_end", arguments: { workspace: "ws", transcript: "finished task" } } })}\n`);
  assert.equal((await readMessage(child)).result.structuredContent.total_extracted, 1);
  assert.deepEqual(requests.map((request) => request.url), [
    "/api/v1/memories/recall",
    "/api/v1/requests/feedback",
    "/api/v1/sessions?workspace=ws&limit=5",
    "/api/v1/memories/session-end",
  ]);
});

test("session end forwards structured episode identity without requiring a transcript", async (t) => {
  const requests = [];
  const server = http.createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      requests.push({ url: request.url, body: JSON.parse(body) });
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ ok: true, version: "v1", data: { mode: "structured_episode", partial: false } }));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({
    AGENT_MEMORY_MCP_PROFILE: "expanded",
    AGENT_MEMORY_URL: `http://127.0.0.1:${server.address().port}`,
  });
  t.after(() => child.kill());

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 91, method: "tools/call", params: { name: "memory_session_end", arguments: {
    workspace: "ws", session_id: "session-1", principal_id: "principal-1", terminal_status: "completed", idempotency_key: "finish-1",
  } } })}\n`);
  const response = await readMessage(child);
  assert.equal(response.result.structuredContent.mode, "structured_episode");
  assert.deepEqual(requests, [{
    url: "/api/v1/memories/session-end",
    body: { workspace: "ws", session_id: "session-1", principal_id: "principal-1", terminal_status: "completed", idempotency_key: "finish-1" },
  }]);
});

test("reports HTTP transport degradation without silently changing backends", async (t) => {
  const child = startServer({
    AGENT_MEMORY_MCP_PROFILE: "expanded",
    AGENT_MEMORY_URL: "http://127.0.0.1:1",
  });
  t.after(() => child.kill());
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 10, method: "tools/call", params: { name: "memory_health", arguments: {} } })}\n`);

  const response = await readMessage(child);

  assert.equal(response.result.isError, true);
  assert.match(response.result.content[0].text, /transport=http/);
  assert.match(response.result.content[0].text, /degraded=true/);
});

test("hosted mode is explicit, authenticated, tenant-scoped, and uses hosted paths", async (t) => {
  const requests = [];
  const server = http.createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      requests.push({ url: request.url, authorization: request.headers.authorization, tenant: request.headers["x-agent-memory-tenant"], body: JSON.parse(body) });
      const data = request.url === "/v1/source-queries"
        ? { evidence: [{ passage_id: "p1", citation_id: "c1", text: "hosted evidence", score: 0.9 }], context: { used_tokens: 2, budget: 100 } }
        : request.url === "/v1/search"
          ? { items: [{ id: "m1", workspace_id: "workspace-1", type: "semantic", content: "hosted memory", score: 0.8 }], next_cursor: "next-1" }
          : { id: "m1" };
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ ok: true, version: "v1", data }));
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const child = startServer({
    AGENT_MEMORY_MODE: "hosted",
    AGENT_MEMORY_API_URL: `http://127.0.0.1:${server.address().port}`,
    AGENT_MEMORY_TOKEN: "hosted-secret",
    AGENT_MEMORY_TENANT_ID: "tenant-1",
  });
  t.after(() => child.kill());

  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 30, method: "tools/call", params: { name: "memory_write", arguments: { workspace: "workspace-1", content: "hosted fact", type: "semantic" } } })}\n`);
  const write = await readMessage(child);
  assert.equal(write.result.structuredContent._transport.mode, "hosted");
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: 31, method: "tools/call", params: { name: "memory_search", arguments: { workspace: "workspace-1", query: "hosted", top_k: 10 } } })}\n`);
  const search = await readMessage(child);
  assert.equal(search.result.structuredContent.results[0].content, "hosted memory");
  assert.equal(search.result.structuredContent.next_cursor, "next-1");
  assert.deepEqual(requests.map((request) => request.url), ["/v1/memories", "/v1/search"]);
  assert.ok(requests.every((request) => request.authorization === "Bearer hosted-secret" && request.tenant === "tenant-1"));
  assert.equal(requests[0].body.workspace_id, "workspace-1");
  assert.deepEqual(requests[1].body, { workspace_id: "workspace-1", query: "hosted", limit: 10 });
});

test("hosted mode fails closed without explicit credentials", async () => {
  const child = startServer({ AGENT_MEMORY_MODE: "hosted", AGENT_MEMORY_TOKEN: "", AGENT_MEMORY_TENANT: "" });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk.toString("utf8"); });
  const exitCode = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", resolve);
  });
  assert.notEqual(exitCode, 0);
  assert.match(stderr, /requires AGENT_MEMORY_TOKEN and AGENT_MEMORY_TENANT/);
});
