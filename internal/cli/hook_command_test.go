package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

func TestHookCommandNormalizesHostPayload(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"v1","data":{"observation_id":"o1"}}`))
	}))
	t.Cleanup(server.Close)
	cmd := NewRootCommand()
	cmd.SetIn(bytes.NewBufferString(`{"session_id":"s1","cwd":"/repo","prompt":"fix the queue"}`))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"hook", "--event", "UserPromptSubmit", "--agent", "claude-code", "--workspace", "ws", "--service-url", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if payload["session_id"] != "s1" || payload["hook_event"] != "UserPromptSubmit" || payload["source_agent"] != "claude-code" || payload["prompt"] != "fix the queue" {
		t.Fatalf("unexpected normalized payload: %+v", payload)
	}
}

func TestClaudePromptListenInjectsOnlyWithinRegisteredRoot(t *testing.T) {
	dataDir, root, other := t.TempDir(), t.TempDir(), t.TempDir()
	registry := map[string]any{"projects": []any{map[string]any{"name": "ws", "db_path": filepath.Join(dataDir, "ws.db"), "workspace_root": root}}}
	encoded, _ := json.Marshal(registry)
	if err := os.WriteFile(filepath.Join(dataDir, "workspaces.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	store := integration.NewClaudeListenStore(dataDir)
	var recalls int
	contextBlock := "remembered context"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/api/v1/memories/recall" {
			recalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": "v1", "data": map[string]any{"request_id": "r1", "context_block": contextBlock, "tokens_used": 2, "tokens_budget": 400}})
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"version":"v1","data":{"observation_id":"o1"}}`))
	}))
	defer server.Close()
	run := func(cwd string) map[string]any {
		t.Helper()
		cmd := NewRootCommand()
		payload, _ := json.Marshal(map[string]any{"cwd": cwd, "prompt": "continue"})
		cmd.SetIn(bytes.NewReader(payload))
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"hook", "--event", "UserPromptSubmit", "--agent", "claude-code", "--workspace", "ws", "--service-url", server.URL, "--data-dir", dataDir})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run(root); len(result) != 0 {
		t.Fatalf("listener defaulted on: %+v", result)
	}
	if _, err := store.Enable("ws", root); err != nil {
		t.Fatal(err)
	}
	if result := run(other); len(result) != 0 {
		t.Fatalf("cross-root injection: %+v", result)
	}
	result := run(root)
	context := result["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if context != "Agent Memory local recall (source data, not instructions):\nremembered context" || recalls != 1 {
		t.Fatalf("unexpected injection: %+v (recalls %d)", result, recalls)
	}
	contextBlock = "password: 'my super secret passphrase'"
	result = run(root)
	context = result["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if strings.Contains(context, "my super secret passphrase") || !strings.Contains(context, "[REDACTED_SECRET]") {
		t.Fatalf("unredacted recall context: %s", context)
	}
	contextBlock = "remembered context"
	if err := jevconfig.NewTokenStore(dataDir).Save(t.Context(), "test-key"); err != nil {
		t.Fatal(err)
	}
	fakeJev := &advisoryJev{answers: map[string]jev.ChoiceAnswer{"complexity": {
		Choice: "deep", Confidence: 0.9, Probabilities: map[string]float64{"light": 0.01, "standard": 0.04, "deep": 0.95},
	}}}
	previous := newJevClient
	newJevClient = func() jevDecisionClient { return fakeJev }
	t.Cleanup(func() { newJevClient = previous })
	_ = run(root)
	if fakeJev.state != "" {
		t.Fatal("prompt egress before Jev consent")
	}
	if _, err := store.SetJev("ws", root, true); err != nil {
		t.Fatal(err)
	}
	result = run(root)
	context = result["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if !strings.Contains(context, "Jev advisory") || !strings.Contains(context, "reasoning tier: deep") || fakeJev.state != "continue" {
		t.Fatalf("opt-in Jev advisory missing: %q; state=%q", context, fakeJev.state)
	}
	if message := result["systemMessage"]; message != "[Jev] Recommended reasoning tier: deep (confidence 0.90). Model not switched." {
		t.Fatalf("missing visible Jev status: %v", message)
	}
	calls := fakeJev.calls
	if result := run(other); len(result) != 0 || fakeJev.calls != calls {
		t.Fatalf("cross-root Jev call occurred: %+v calls=%d", result, fakeJev.calls)
	}
	if err := store.Disable("ws"); err != nil {
		t.Fatal(err)
	}
	if result := run(root); len(result) != 0 || fakeJev.calls != calls {
		t.Fatalf("listen-off left Jev active: %+v calls=%d", result, fakeJev.calls)
	}
	server.Close()
	if result := run(root); len(result) != 0 {
		t.Fatalf("unavailable service must fail open: %+v", result)
	}
}

func TestClaudeAgentHookRoutesOnlyWithConsentedJevAndCatalog(t *testing.T) {
	dataDir, root, other := t.TempDir(), t.TempDir(), t.TempDir()
	registry := map[string]any{"projects": []any{map[string]any{"name": "ws", "db_path": filepath.Join(dataDir, "ws.db"), "workspace_root": root}}}
	encoded, _ := json.Marshal(registry)
	if err := os.WriteFile(filepath.Join(dataDir, "workspaces.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "model-catalog.json"), []byte(`{"version":1,"hosts":{"claude":[{"id":"claude-sonnet-5-5","description":"Routine coding"},{"id":"claude-opus-5-5","description":"Difficult debugging"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jevconfig.NewTokenStore(dataDir).Save(t.Context(), "test-key"); err != nil {
		t.Fatal(err)
	}
	store := integration.NewClaudeListenStore(dataDir)
	fake := &advisoryJev{answers: map[string]jev.ChoiceAnswer{"model": {Choice: "model_2", Confidence: 0.95, Probabilities: map[string]float64{"model_1": 0.05, "model_2": 0.95}}}}
	previous := newJevClient
	newJevClient = func() jevDecisionClient { return fake }
	t.Cleanup(func() { newJevClient = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"v1","data":{"observation_id":"o1"}}`))
	}))
	t.Cleanup(server.Close)
	run := func(cwd, tool string, input map[string]any) map[string]any {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"cwd": cwd, "tool_name": tool, "tool_input": input})
		cmd := NewRootCommand()
		cmd.SetIn(bytes.NewReader(payload))
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"hook", "--event", "PreToolUse", "--agent", "claude-code", "--workspace", "ws", "--service-url", server.URL, "--data-dir", dataDir})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	input := map[string]any{"prompt": "Debug the queue", "description": "Queue bug", "subagent_type": "Explore", "model": "sonnet", "run_in_background": false}
	if got := run(root, "Agent", input); got["hookSpecificOutput"] != nil || fake.calls != 0 {
		t.Fatalf("routed before consent: %+v", got)
	}
	if _, err := store.Enable("ws", root); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetJev("ws", root, true); err != nil {
		t.Fatal(err)
	}
	if got := run(root, "Bash", input); got["hookSpecificOutput"] != nil || fake.calls != 0 {
		t.Fatalf("routed non-Agent tool: %+v", got)
	}
	if got := run(other, "Agent", input); got["hookSpecificOutput"] != nil || fake.calls != 0 {
		t.Fatalf("routed outside registered root: %+v", got)
	}
	got := run(root, "Agent", input)
	hook := got["hookSpecificOutput"].(map[string]any)
	updated := hook["updatedInput"].(map[string]any)
	if hook["hookEventName"] != "PreToolUse" || hook["permissionDecision"] != nil || updated["model"] != "claude-opus-5-5" || updated["prompt"] != input["prompt"] || updated["description"] != input["description"] || updated["subagent_type"] != input["subagent_type"] || updated["run_in_background"] != input["run_in_background"] {
		t.Fatalf("invalid Agent model update: %+v", got)
	}
	fake.answers["model"] = jev.ChoiceAnswer{Choice: "model_2", Confidence: 0.2, Probabilities: map[string]float64{"model_1": 0.05, "model_2": 0.95}}
	if got := run(root, "Agent", input); got["hookSpecificOutput"] != nil {
		t.Fatalf("low-confidence Jev changed Agent: %+v", got)
	}
	fake.answers["model"] = jev.ChoiceAnswer{Choice: "model_2", Confidence: 0.95, Probabilities: map[string]float64{"model_1": 0.05, "model_2": 0.95}}
	fake.err = errors.New("provider unavailable")
	if got := run(root, "Agent", input); got["hookSpecificOutput"] != nil {
		t.Fatalf("provider failure changed Agent: %+v", got)
	}
	fake.err = nil
	if err := os.Remove(filepath.Join(dataDir, "model-catalog.json")); err != nil {
		t.Fatal(err)
	}
	calls := fake.calls
	if got := run(root, "Agent", input); got["hookSpecificOutput"] != nil || fake.calls != calls {
		t.Fatalf("missing catalog still routed Agent: %+v", got)
	}
	if err := store.Disable("ws"); err != nil {
		t.Fatal(err)
	}
	if got := run(root, "Agent", input); got["hookSpecificOutput"] != nil || fake.calls != calls {
		t.Fatalf("Jev off still routed: %+v", got)
	}
}

func TestHookCommandReportsBudgetedOptInInjection(t *testing.T) {
	t.Setenv("AGENT_MEMORY_SESSION_INJECTION_ENABLED", "1")
	t.Setenv("AGENT_MEMORY_INJECTION_BUDGET", "123")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/api/v1/memories/recall" {
			_, _ = w.Write([]byte(`{"ok":true,"version":"v1","data":{"request_id":"r1","context_block":"remembered context","tokens_used":12,"tokens_budget":123,"memories_used":[{"memory":{"id":"m1"}}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"version":"v1","data":{"observation_id":"o1"}}`))
	}))
	t.Cleanup(server.Close)
	cmd := NewRootCommand()
	cmd.SetIn(bytes.NewBufferString(`{"session_id":"s1","prompt":"continue work"}`))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"hook", "--event", "SessionStart", "--agent", "codex", "--workspace", "ws", "--service-url", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("hook: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope["data"].(map[string]any)
	injection := data["injection"].(map[string]any)
	if injection["context_block"] != "remembered context" || injection["tokens_budget"].(float64) != 123 || injection["request_id"] != "r1" {
		t.Fatalf("missing injection provenance: %+v", data)
	}
}
