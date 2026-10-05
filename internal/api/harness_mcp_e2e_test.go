package api

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
)

// mcpAdapter drives the real Node MCP adapter over stdio, exactly as an MCP client
// would, against the real Go gateway over a real local socket.
type mcpAdapter struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	lines  chan string
	nextID int
}

func startAdapter(t *testing.T, serviceURL, token string, extraEnv ...string) *mcpAdapter {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the MCP end-to-end test needs it")
	}
	_, here, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(here), "..", "..", "tools", "agent-memory", "mcp-server", "src", "server.js")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("adapter script not found: %v", err)
	}
	cmd := exec.Command(node, script)
	cmd.Env = append(os.Environ(), "AGENT_MEMORY_API_URL="+serviceURL, "AGENT_MEMORY_HARNESS_TOKEN="+token, "AGENT_MEMORY_CLIENT_ID=", "AGENT_MEMORY_MCP_PROFILE=default")
	cmd.Env = append(cmd.Env, extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	a := &mcpAdapter{t: t, cmd: cmd, stdin: bufio.NewWriter(stdin), lines: make(chan string, 64), nextID: 1}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			a.lines <- scanner.Text()
		}
		close(a.lines)
	}()
	t.Cleanup(a.close)
	return a
}

func (a *mcpAdapter) close() {
	if a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
		_, _ = a.cmd.Process.Wait()
	}
}

func (a *mcpAdapter) rpc(method string, params any) map[string]any {
	a.t.Helper()
	id := a.nextID
	a.nextID++
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := a.stdin.Write(append(line, '\n')); err != nil {
		a.t.Fatal(err)
	}
	if err := a.stdin.Flush(); err != nil {
		a.t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case raw, ok := <-a.lines:
			if !ok {
				a.t.Fatal("the adapter exited")
			}
			var message map[string]any
			if err := json.Unmarshal([]byte(raw), &message); err != nil {
				a.t.Fatalf("non-JSON line on the protocol stream: %q", raw)
			}
			if message["id"] == float64(id) {
				return message
			}
		case <-timeout:
			a.t.Fatalf("timed out waiting for %s", method)
		}
	}
}

// call returns structured content on success and the error text when the tool failed.
func (a *mcpAdapter) call(name string, args map[string]any) (map[string]any, string) {
	a.t.Helper()
	message := a.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	result, _ := message["result"].(map[string]any)
	if result == nil {
		a.t.Fatalf("no result: %v", message)
	}
	if result["isError"] == true {
		content := result["content"].([]any)[0].(map[string]any)
		return nil, content["text"].(string)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	return structured, ""
}

func (a *mcpAdapter) toolNames() []string {
	a.t.Helper()
	tools := a.rpc("tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.(map[string]any)["name"].(string)
	}
	return names
}

func mcpRun(t *testing.T, structured map[string]any) map[string]any {
	t.Helper()
	run, ok := structured["run"].(map[string]any)
	if !ok {
		t.Fatalf("no run in %v", structured)
	}
	return run
}

func (a *mcpAdapter) waitRun(id string, states ...string) map[string]any {
	a.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		data, failure := a.call("harness_status", map[string]any{"workspace": "agent-memory", "run_id": id})
		if failure != "" {
			a.t.Fatalf("status failed: %s", failure)
		}
		run := mcpRun(a.t, data)
		for _, state := range states {
			if run["state"] == state {
				return run
			}
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("timed out; last run %v", run)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func TestHarnessThroughTheRealMCPAdapter(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 120 * time.Millisecond, CostMicros: 10, Script: []harnesstest.Step{{Text: "fake provider result"}}}})
	owner, _ := e.grant("claude-desktop")
	intruder, _ := e.grant("codex-cli")

	a := startAdapter(t, e.server.URL, owner)
	names := a.toolNames()
	harnessCount := 0
	for _, name := range names {
		if strings.HasPrefix(name, "harness_") {
			harnessCount++
		}
	}
	if harnessCount != 8 {
		t.Fatalf("tools = %v", names)
	}

	caps, failure := a.call("harness_capabilities", map[string]any{"workspace": "agent-memory"})
	if failure != "" || len(caps["operations"].([]any)) != 6 || caps["features"].(map[string]any)["fake_providers"] != true {
		t.Fatalf("capabilities = %v %s", caps, failure)
	}

	began := time.Now()
	started, failure := a.call("harness_start", map[string]any{"workspace": "agent-memory", "goal": "an end to end goal", "idempotency_key": "e2e-key-000001"})
	if failure != "" || time.Since(began) > 2*time.Second {
		t.Fatalf("start = %v %s after %v", started, failure, time.Since(began))
	}
	id := mcpRun(t, started)["id"].(string)
	done := a.waitRun(id, "completed", "failed")
	if done["state"] != "completed" || done["usage"].(map[string]any)["spend_micros"] != float64(10) {
		t.Fatalf("done = %v", done)
	}
	encoded, _ := json.Marshal(done)
	if strings.Contains(string(encoded), "an end to end goal") || strings.Contains(string(encoded), "fake provider result") {
		t.Fatalf("status leaked run content through MCP: %s", encoded)
	}

	// Reconnect: a brand-new adapter process with the same grant sees the same run,
	// and retrying the start with the same key is deduplicated.
	a.close()
	b := startAdapter(t, e.server.URL, owner)
	again, failure := b.call("harness_status", map[string]any{"workspace": "agent-memory", "run_id": id})
	if failure != "" || mcpRun(t, again)["state"] != "completed" {
		t.Fatalf("after reconnect = %v %s", again, failure)
	}
	retry, failure := b.call("harness_start", map[string]any{"workspace": "agent-memory", "goal": "an end to end goal", "idempotency_key": "e2e-key-000001"})
	if failure != "" || mcpRun(t, retry)["id"] != id || mcpRun(t, retry)["deduplicated"] != true {
		t.Fatalf("retry after reconnect = %v %s", retry, failure)
	}

	// Isolation: another client cannot tell this run from one that never existed.
	c := startAdapter(t, e.server.URL, intruder)
	_, real := c.call("harness_status", map[string]any{"workspace": "agent-memory", "run_id": id})
	_, missing := c.call("harness_status", map[string]any{"workspace": "agent-memory", "run_id": "run_" + strings.Repeat("0", 32)})
	if real == "" || real != missing || !strings.Contains(real, "not_found") {
		t.Fatalf("existence is distinguishable: %q vs %q", real, missing)
	}

	// Errors keep their fixed codes and never carry credentials.
	if _, failure := b.call("harness_cancel", map[string]any{"workspace": "agent-memory", "run_id": id, "expected_generation": 1}); !strings.Contains(failure, "stale_generation") {
		t.Fatalf("stale cancel = %q", failure)
	}
	for _, text := range []string{real, missing} {
		if strings.Contains(text, owner) || strings.Contains(text, intruder) {
			t.Fatalf("a credential leaked into an error: %q", text)
		}
	}
}

func TestHarnessCancelAndRevocationThroughTheRealMCPAdapter(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "never"}}}})
	token, info := e.grant("claude-desktop")
	a := startAdapter(t, e.server.URL, token)

	started, failure := a.call("harness_start", map[string]any{"workspace": "agent-memory", "goal": "slow work", "budget": map[string]any{"max_turns": 3}})
	if failure != "" {
		t.Fatal(failure)
	}
	id := mcpRun(t, started)["id"].(string)
	running := a.waitRun(id, "running")
	if running["budget"].(map[string]any)["max_turns"] != float64(3) {
		t.Fatalf("budget = %v", running["budget"])
	}

	if _, failure := a.call("harness_cancel", map[string]any{"workspace": "agent-memory", "run_id": id, "expected_generation": float64(running["generation"].(float64)) - 1}); !strings.Contains(failure, "stale_generation") {
		t.Fatalf("stale cancel = %q", failure)
	}
	began := time.Now()
	_, failure = a.call("harness_cancel", map[string]any{"workspace": "agent-memory", "run_id": id, "expected_generation": running["generation"], "idempotency_key": "e2e-cancel-001"})
	if failure != "" {
		t.Fatal(failure)
	}
	if done := a.waitRun(id, "cancelled"); time.Since(began) > 2*time.Second || done["code"] != "cancelled" {
		t.Fatalf("cancellation slow or wrong: %v after %v", done, time.Since(began))
	}

	// Revoking the grant ends the adapter's access with the same fixed denial.
	if _, err := e.auth.Revoke(bg, info.ID); err != nil {
		t.Fatal(err)
	}
	_, failure = a.call("harness_status", map[string]any{"workspace": "agent-memory", "run_id": id})
	if !strings.Contains(failure, "harness unauthorized: access denied") || strings.Contains(failure, token) {
		t.Fatalf("after revocation = %q", failure)
	}
	if _, failure := a.call("harness_start", map[string]any{"workspace": "agent-memory", "goal": "again"}); !strings.Contains(failure, "unauthorized") {
		t.Fatalf("start after revocation = %q", failure)
	}
}
