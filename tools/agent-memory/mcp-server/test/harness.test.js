import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import http from "node:http";
import test from "node:test";

const token = `hcap1.${"a".repeat(32)}.${"B".repeat(43)}`;
const runId = `run_${"c".repeat(32)}`;

// A line reader that resolves each JSON-RPC response by id, so one adapter process can
// serve many calls in a test.
class Adapter {
  constructor(env = {}) {
    this.child = spawn(process.execPath, ["src/server.js"], {
      cwd: new URL("..", import.meta.url),
      env: { ...process.env, AGENT_MEMORY_HARNESS_TOKEN: "", AGENT_MEMORY_CLIENT_ID: "", ...env },
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.buffer = "";
    this.waiting = new Map();
    this.stderr = "";
    this.nextId = 1;
    this.child.stdout.on("data", (chunk) => {
      this.buffer += chunk.toString("utf8");
      let newline;
      while ((newline = this.buffer.indexOf("\n")) >= 0) {
        const line = this.buffer.slice(0, newline);
        this.buffer = this.buffer.slice(newline + 1);
        const message = JSON.parse(line);
        this.waiting.get(message.id)?.(message);
      }
    });
    this.child.stderr.on("data", (chunk) => { this.stderr += chunk.toString("utf8"); });
    this.exited = new Promise((resolve) => this.child.once("exit", (code) => resolve(code)));
  }

  request(method, params = {}) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`timed out waiting for ${method}`)), 5000);
      this.waiting.set(id, (message) => { clearTimeout(timer); resolve(message); });
      this.child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    });
  }

  async call(name, args) {
    const response = await this.request("tools/call", { name, arguments: args });
    return response.result ?? response;
  }

  async toolNames() {
    return (await this.request("tools/list")).result.tools.map((tool) => tool.name);
  }

  close() {
    this.child.kill();
  }
}

async function stub(t, handler) {
  const requests = [];
  const server = http.createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const raw = Buffer.concat(chunks).toString("utf8");
    const recorded = { method: request.method, url: request.url, headers: request.headers, raw, body: raw ? JSON.parse(raw) : undefined };
    requests.push(recorded);
    await handler(recorded, response);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  return { url: `http://127.0.0.1:${server.address().port}`, requests };
}

function json(response, status, body) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
}

const ok = (data) => (_request, response) => json(response, 200, { ok: true, version: "v1", data });

function adapter(t, env) {
  const instance = new Adapter(env);
  t.after(() => instance.close());
  return instance;
}

const harnessNames = ["harness_capabilities", "harness_start", "harness_status", "harness_cancel", "harness_continue", "harness_events", "harness_artifact", "harness_readiness"];
const defaultNames = [
  "memory_write", "memory_search", "memory_recall", "memory_feedback", "memory_session_end",
  "solution_start", "solution_step", "solution_checkpoint", "solution_state", "solution_transition",
  "solution_handoff", "solution_recall", "solution_promote",
];

test("harness tools are absent without a grant and cannot be called", async (t) => {
  const service = await stub(t, ok({}));
  for (const profile of ["default", "expanded"]) {
    const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_MCP_PROFILE: profile });
    const names = await instance.toolNames();
    assert.equal(names.filter((name) => name.startsWith("harness_")).length, 0, `${profile} profile exposed harness tools`);
  }
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url });
  const result = await instance.call("harness_start", { workspace: "agent-memory", goal: "x" });
  assert.equal(result.isError, true);
  assert.match(result.content[0].text, /unknown tool/);
  assert.equal(service.requests.length, 0, "an unavailable tool still reached the service");
});

test("a configured grant adds exactly the eight harness tools after the profile tools", async (t) => {
  const service = await stub(t, ok({}));
  const withDefault = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token });
  assert.deepEqual(await withDefault.toolNames(), [...defaultNames, ...harnessNames]);
  const expanded = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token, AGENT_MEMORY_MCP_PROFILE: "expanded" });
  const names = await expanded.toolNames();
  assert.deepEqual(names.slice(-harnessNames.length), harnessNames);
  assert.ok(!names.includes("harness_approve") && !names.some((name) => /approve|approval/.test(name) && name.startsWith("harness")), "approval must not be an MCP tool");

  const listed = (await withDefault.request("tools/list")).result.tools.filter((tool) => tool.name.startsWith("harness_"));
  for (const tool of listed) {
    assert.equal(tool.inputSchema.additionalProperties, false, `${tool.name} accepts arbitrary arguments`);
    assert.ok(tool.inputSchema.required.includes("workspace"), `${tool.name} does not require a workspace`);
  }
  const start = listed.find((tool) => tool.name === "harness_start");
  assert.equal(start.inputSchema.properties.goal.maxLength, 4096);
  assert.equal(start.inputSchema.properties.budget.additionalProperties, false);
  assert.equal(start.inputSchema.properties.budget.properties.max_turns.maximum, 64);
});

test("a malformed grant fails closed at startup without echoing it", async () => {
  const secret = "hcap1.not-a-valid-grant-SECRET-VALUE";
  const instance = new Adapter({ AGENT_MEMORY_HARNESS_TOKEN: secret });
  const code = await instance.exited;
  assert.equal(code, 2);
  assert.match(instance.stderr, /not a valid harness grant/);
  assert.ok(!instance.stderr.includes("SECRET-VALUE"), "the rejected grant was echoed");
});

test("start forwards a bearer grant, a generated or supplied idempotency key, and a bounded budget", async (t) => {
  const service = await stub(t, ok({ run: { id: runId, state: "queued" } }));
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token });

  const generated = await instance.call("harness_start", { workspace: "agent-memory", goal: "do the thing" });
  assert.equal(generated.isError, undefined);
  assert.equal(generated.structuredContent.run.id, runId);
  const first = service.requests[0];
  assert.equal(first.method, "POST");
  assert.equal(first.url, "/api/v1/harness/runs");
  assert.equal(first.headers.authorization, `Bearer ${token}`);
  assert.equal(first.headers["x-agent-memory-client"], undefined);
  assert.deepEqual(Object.keys(first.body).sort(), ["budget", "goal", "idempotency_key", "workspace"]);
  assert.match(first.body.idempotency_key, /^[A-Za-z0-9._:-]{8,64}$/);
  assert.deepEqual(first.body.budget, {});
  assert.ok(!first.url.includes(token) && !first.raw.includes(token), "the grant appeared in the URL or body");

  await instance.call("harness_start", { workspace: "agent-memory", goal: "do the thing", idempotency_key: "retry-key-0001", budget: { max_turns: 3, max_active_seconds: 30 } });
  assert.equal(service.requests[1].body.idempotency_key, "retry-key-0001");
  assert.deepEqual(service.requests[1].body.budget, { max_turns: 3, max_active_seconds: 30 });
});

test("the client identity claim is forwarded when one is configured", async (t) => {
  const service = await stub(t, (request, response) => {
    if (request.url === "/api/v1/client-profiles/claude-desktop") {
      return json(response, 200, { ok: true, data: { profile: { id: "claude-desktop", tool_profile: "default" } } });
    }
    return json(response, 200, { ok: true, data: { workspace: "agent-memory", operations: ["status"] } });
  });
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token, AGENT_MEMORY_CLIENT_ID: "claude-desktop" });
  const result = await instance.call("harness_capabilities", { workspace: "agent-memory" });
  assert.equal(result.structuredContent.workspace, "agent-memory");
  const forwarded = service.requests.find((request) => request.url.startsWith("/api/v1/harness/capabilities"));
  assert.equal(forwarded.method, "GET");
  assert.equal(forwarded.url, "/api/v1/harness/capabilities?workspace=agent-memory");
  assert.equal(forwarded.headers["x-agent-memory-client"], "claude-desktop");
  assert.equal(forwarded.headers.authorization, `Bearer ${token}`);
});

test("status and cancel build fixed paths and bodies", async (t) => {
  const service = await stub(t, ok({ run: { id: runId, state: "running", generation: 4 } }));
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token });
  await instance.call("harness_status", { workspace: "agent-memory", run_id: runId });
  assert.equal(service.requests[0].method, "GET");
  assert.equal(service.requests[0].url, `/api/v1/harness/runs/${runId}?workspace=agent-memory`);
  assert.equal(service.requests[0].body, undefined);

  await instance.call("harness_cancel", { workspace: "agent-memory", run_id: runId, expected_generation: 4, idempotency_key: "cancel-key-001" });
  assert.equal(service.requests[1].method, "POST");
  assert.equal(service.requests[1].url, `/api/v1/harness/runs/${runId}/cancel`);
  assert.deepEqual(service.requests[1].body, { workspace: "agent-memory", expected_generation: 4, idempotency_key: "cancel-key-001" });

  await instance.call("harness_cancel", { workspace: "agent-memory", run_id: runId, expected_generation: 4 });
  assert.match(service.requests[2].body.idempotency_key, /^[A-Za-z0-9._:-]{8,64}$/);
});

test("invalid arguments are rejected locally and never reach the service", async (t) => {
  const service = await stub(t, ok({}));
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token });
  const bad = [
    ["harness_status", { workspace: "agent-memory", run_id: "run_x/../../admin" }],
    ["harness_status", { workspace: "agent-memory", run_id: "../../etc/passwd" }],
    ["harness_status", { workspace: "agent-memory", run_id: `${runId}?x=1` }],
    ["harness_status", { workspace: "../etc", run_id: runId }],
    ["harness_status", { workspace: "agent memory", run_id: runId }],
    ["harness_status", { workspace: "agent-memory" }],
    ["harness_status", { run_id: runId }],
    ["harness_status", { workspace: "agent-memory", run_id: runId, admin: true }],
    ["harness_capabilities", { workspace: "a".repeat(65) }],
    ["harness_start", { workspace: "agent-memory" }],
    ["harness_start", { workspace: "agent-memory", goal: "" }],
    ["harness_start", { workspace: "agent-memory", goal: "g", idempotency_key: "short" }],
    ["harness_start", { workspace: "agent-memory", goal: "g", idempotency_key: "has spaces in it" }],
    ["harness_start", { workspace: "agent-memory", goal: "g", budget: { max_turns: 9999 } }],
    ["harness_start", { workspace: "agent-memory", goal: "g", budget: { max_turns: 1.5 } }],
    ["harness_start", { workspace: "agent-memory", goal: "g", budget: { max_turns: "3" } }],
    ["harness_start", { workspace: "agent-memory", goal: "g", budget: { bogus: 1 } }],
    ["harness_start", { workspace: "agent-memory", goal: "g", budget: [1] }],
    ["harness_start", { workspace: "agent-memory", goal: "g", budget: null }],
    ["harness_cancel", { workspace: "agent-memory", run_id: runId }],
    ["harness_cancel", { workspace: "agent-memory", run_id: runId, expected_generation: 0 }],
    ["harness_cancel", { workspace: "agent-memory", run_id: runId, expected_generation: -3 }],
    ["harness_cancel", { workspace: "agent-memory", run_id: runId, expected_generation: 1.5 }],
    ["harness_approve", { workspace: "agent-memory", run_id: runId }],
  ];
  for (const [name, args] of bad) {
    const result = await instance.call(name, args);
    assert.equal(result.isError, true, `${name} ${JSON.stringify(args)} was accepted`);
  }
  assert.equal(service.requests.length, 0, `invalid calls reached the service: ${JSON.stringify(service.requests.map((r) => r.url))}`);
});

test("service errors keep only a fixed code and message and never carry the grant", async (t) => {
  const service = await stub(t, (request, response) => {
    if (request.url.includes("status-401")) return json(response, 401, { ok: false, error: { code: "unauthorized", message: "access denied" } });
    if (request.url.includes("echo")) return json(response, 400, { ok: false, error: { code: "invalid_request", message: `you sent ${token} by mistake` } });
    if (request.url.includes("html")) { response.writeHead(502, { "content-type": "text/html" }); return response.end("<html>bad gateway</html>"); }
    return json(response, 409, { ok: false, error: { code: "stale_generation", message: "the run changed since you last read it" } });
  });
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token });
  const denied = await instance.call("harness_capabilities", { workspace: "status-401" });
  assert.equal(denied.isError, true);
  assert.match(denied.content[0].text, /harness unauthorized: access denied/);
  const stale = await instance.call("harness_cancel", { workspace: "agent-memory", run_id: runId, expected_generation: 2 });
  assert.match(stale.content[0].text, /harness stale_generation: the run changed since you last read it/);
  const echoed = await instance.call("harness_capabilities", { workspace: "echo-me" });
  assert.equal(echoed.isError, true);
  assert.ok(!echoed.content[0].text.includes(token) && !echoed.content[0].text.includes("B".repeat(43)), "the grant leaked through an error message");
  assert.match(echoed.content[0].text, /\[redacted\]/);
  const html = await instance.call("harness_capabilities", { workspace: "html-page" });
  assert.match(html.content[0].text, /harness service returned HTTP 502/);
  assert.ok(!html.content[0].text.includes("<html>"));
});

test("an unreachable service is reported without detail", async (t) => {
  const instance = adapter(t, { AGENT_MEMORY_API_URL: "http://127.0.0.1:9", AGENT_MEMORY_HARNESS_TOKEN: token });
  const result = await instance.call("harness_status", { workspace: "agent-memory", run_id: runId });
  assert.equal(result.isError, true);
  assert.match(result.content[0].text, /harness service is unavailable/);
  assert.ok(!result.content[0].text.includes(token));
});

test("redirects are refused so the grant cannot be forwarded", async (t) => {
  const elsewhere = await stub(t, ok({ stolen: true }));
  const service = await stub(t, (_request, response) => {
    response.writeHead(307, { location: `${elsewhere.url}/collect` });
    response.end();
  });
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token });
  const result = await instance.call("harness_status", { workspace: "agent-memory", run_id: runId });
  assert.equal(result.isError, true);
  assert.equal(elsewhere.requests.length, 0, "the adapter followed a redirect carrying the grant");
});

test("oversized service results are refused rather than returned", async (t) => {
  const service = await stub(t, ok({ run: { id: runId, padding: "x".repeat(5000) } }));
  const instance = adapter(t, { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token, AGENT_MEMORY_MCP_MAX_RESPONSE_BYTES: "1024" });
  const response = await instance.request("tools/call", { name: "harness_status", arguments: { workspace: "agent-memory", run_id: runId } });
  assert.equal(response.error?.code, -32002);
});

test("a reconnecting adapter with the same grant sees the same run", async (t) => {
  const runs = new Map();
  const service = await stub(t, (request, response) => {
    if (request.method === "POST" && request.url === "/api/v1/harness/runs") {
      if (!runs.has(request.body.idempotency_key)) runs.set(request.body.idempotency_key, `run_${String(runs.size).padStart(32, "0")}`);
      return json(response, 202, { ok: true, data: { run: { id: runs.get(request.body.idempotency_key), state: "queued" } } });
    }
    const id = request.url.split("/")[5]?.split("?")[0];
    return json(response, 200, { ok: true, data: { run: { id, state: "running" } } });
  });
  const env = { AGENT_MEMORY_API_URL: service.url, AGENT_MEMORY_HARNESS_TOKEN: token };
  const first = new Adapter(env);
  const started = await first.call("harness_start", { workspace: "agent-memory", goal: "survive a reconnect", idempotency_key: "reconnect-key-1" });
  const id = started.structuredContent.run.id;
  first.close();
  await first.exited;

  const second = adapter(t, env);
  const status = await second.call("harness_status", { workspace: "agent-memory", run_id: id });
  assert.equal(status.structuredContent.run.id, id);
  // Retrying the start after the reconnect with the same key is the same run.
  const retry = await second.call("harness_start", { workspace: "agent-memory", goal: "survive a reconnect", idempotency_key: "reconnect-key-1" });
  assert.equal(retry.structuredContent.run.id, id);
});
