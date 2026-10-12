package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
)

// A scripted stand-in for the Anthropic Messages API plays the service through a whole tool
// round trip: the model asks to read a file, the harness runs the tool and sends the result
// back, and the model answers with it. This checks the adapter against the documented wire
// shapes (request fields, headers, tool_use and end_turn replies, usage accounting) without a
// key. It is a simulation: it cannot show that the live service accepts what is sent.
func TestAnthropicToolUseRoundTripAgainstAScriptedService(t *testing.T) {
	svc, _, root := harnessServiceRoot(t)
	const marker = "the harbor light is green-owl-42"
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(marker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var requests []map[string]any
	var headers []http.Header
	reply := func(turn int, body map[string]any) string {
		switch turn {
		case 1:
			return `{"id":"msg_01TURN1","type":"message","role":"assistant","model":"claude-x","content":[` +
				`{"type":"text","text":"I will read the notes."},` +
				`{"type":"tool_use","id":"toolu_01ABCDEF","name":"read_file","input":{"path":"notes.txt"}}],` +
				`"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1200,"output_tokens":30}}`
		default:
			// The service answers from what it was actually sent, as the real one would.
			text := "I could not see the file contents."
			if strings.Contains(fmt.Sprint(body), marker) {
				text = "The notes say: " + marker
			}
			encoded, _ := json.Marshal(text)
			return `{"id":"msg_01TURN2","type":"message","role":"assistant","model":"claude-x","content":[` +
				`{"type":"text","text":` + string(encoded) + `}],` +
				`"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1500,"output_tokens":20,"cache_read_input_tokens":0}}`
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/models/") {
			_, _ = w.Write([]byte(`{"id":"claude-x","type":"model","display_name":"X","created_at":"2026-01-01T00:00:00Z"}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad json"}}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, body)
		headers = append(headers, r.Header.Clone())
		turn := len(requests)
		mu.Unlock()
		_, _ = w.Write([]byte(reply(turn, body)))
	}))
	defer upstream.Close()

	anthropicEnv(t)
	t.Setenv(harnessToolsEnv, "read")
	var stderr bytes.Buffer
	gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{anthropicBaseURL: upstream.URL})
	if err != nil || gateway == nil {
		t.Fatalf("gateway=%v err=%v stderr=%s", gateway, err, stderr.String())
	}
	t.Cleanup(closeHarness)
	svc.Harness = gateway
	server := httptest.NewServer(api.LocalRequestBoundary(api.NewMux(svc)))
	defer server.Close()
	if err := gateway.Authority.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	token, _, err := gateway.Authority.Mint(context.Background(), harnessauth.MintRequest{ClientID: "claude-desktop", Workspaces: []string{"agent-memory"}, Operations: harnessauth.AllOperations()})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) []byte {
		var reader io.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		}
		request, _ := http.NewRequest(method, server.URL+path, reader)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return raw
	}
	started := call(http.MethodPost, "/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "tooluse-key-01", "goal": "what do the notes say?"})
	var envelope struct{ Data struct{ Run map[string]any } }
	_ = json.Unmarshal(started, &envelope)
	id, _ := envelope.Data.Run["id"].(string)
	if id == "" {
		t.Fatalf("start = %s", started)
	}
	var run map[string]any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(15 * time.Millisecond) {
		var e struct{ Data struct{ Run map[string]any } }
		_ = json.Unmarshal(call(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", nil), &e)
		if run = e.Data.Run; run["state"] == "completed" || run["state"] == "failed" || run["state"] == "partial" {
			break
		}
	}
	if run["state"] != "completed" {
		t.Fatalf("run = %v\nstderr: %s", run, stderr.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("the service saw %d requests, want 2 (tool call, then answer)", len(requests))
	}
	for i, body := range requests {
		turn := fmt.Sprintf("request %d", i+1)
		fields := make([]string, 0, len(body))
		for field := range body {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		prompt := ""
		if messages, ok := body["messages"].([]any); ok && len(messages) > 0 {
			prompt = fmt.Sprint(messages[len(messages)-1])
		}
		tail := prompt
		if len(tail) > 160 {
			tail = "..." + tail[len(tail)-160:]
		}
		t.Logf("service saw %s: fields=%v model=%v messages=%d tools=%d prompt tail=%q", turn, fields, body["model"], len(body["messages"].([]any)), len(body["tools"].([]any)), tail)
		// Required fields of the Messages API, and nothing the harness does not intend to send.
		allowed := map[string]bool{"model": true, "max_tokens": true, "messages": true, "system": true, "tools": true, "tool_choice": true, "cache_control": true, "temperature": true, "metadata": true, "stop_sequences": true, "stream": true}
		for field := range body {
			if !allowed[field] {
				t.Errorf("%s carries an unexpected field %q", turn, field)
			}
		}
		if body["model"] != "claude-x" {
			t.Errorf("%s model = %v", turn, body["model"])
		}
		if max, ok := body["max_tokens"].(float64); !ok || max < 1 {
			t.Errorf("%s max_tokens = %v", turn, body["max_tokens"])
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) == 0 {
			t.Fatalf("%s has no messages", turn)
		}
		if first, _ := messages[0].(map[string]any); first["role"] != "user" {
			t.Errorf("%s must start with a user message: %v", turn, first["role"])
		}
		tools, _ := body["tools"].([]any)
		found := false
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			schema, _ := tool["input_schema"].(map[string]any)
			if tool["name"] == "" || schema["type"] != "object" {
				t.Errorf("%s has a malformed tool %v", turn, tool)
			}
			if tool["name"] == "read_file" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s did not offer read_file", turn)
		}
		if strings.Contains(fmt.Sprint(body), anthropicTestKey) {
			t.Errorf("%s leaked the key", turn)
		}
	}
	for i, h := range headers {
		if h.Get("x-api-key") != anthropicTestKey || h.Get("anthropic-version") == "" || !strings.HasPrefix(h.Get("Content-Type"), "application/json") {
			t.Errorf("request %d headers: key set=%v version=%q type=%q", i+1, h.Get("x-api-key") == anthropicTestKey, h.Get("anthropic-version"), h.Get("Content-Type"))
		}
	}
	if !strings.Contains(fmt.Sprint(requests[1]), marker) {
		t.Errorf("the tool's result never reached the model on turn 2: %.400v", requests[1])
	}
	usage, _ := run["usage"].(map[string]any)
	if usage["tool_calls"] != float64(1) {
		t.Errorf("usage = %v, want exactly one tool call", usage)
	}
	if spend, _ := usage["spend_micros"].(float64); spend <= 0 {
		t.Errorf("usage = %v, want spend from both turns", usage)
	}
}
