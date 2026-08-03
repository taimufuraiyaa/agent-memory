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

test("lists only the compact core tools by default", async (t) => {
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
  ]);
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
