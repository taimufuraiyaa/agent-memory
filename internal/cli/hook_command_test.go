package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
