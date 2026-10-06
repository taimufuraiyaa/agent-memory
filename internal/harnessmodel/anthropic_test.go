package harnessmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const claudeKey = "sk-ant-test-SECRET-4242"

type fakeClaude struct {
	mu       sync.Mutex
	status   int
	body     string
	probe    int
	delay    time.Duration
	requests []recorded
	server   *httptest.Server
}

func claudeBody(text, stop string, in, out, cacheRead int) string {
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":%q}],"stop_reason":%q,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0}}`, text, stop, in, out, cacheRead)
}

func claudeToolBody(name, input string) string {
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"tool_use","id":"toolu_1","name":%q,"input":%s}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":50,"output_tokens":5}}`, name, input)
}

func newFakeClaude(t *testing.T) *fakeClaude {
	t.Helper()
	f := &fakeClaude{status: 200, probe: 200, body: claudeBody("hello", "end_turn", 100, 10, 40)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("Content-Type"), raw})
		status, body, probe, delay := f.status, f.body, f.probe, f.delay
		f.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/models/") {
			if probe != 200 {
				w.WriteHeader(probe)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"x","message":"service says ` + claudeKey + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"claude-x","type":"model","display_name":"X","created_at":"2026-01-01T00:00:00Z"}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeClaude) set(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.mu.Unlock()
}
func (f *fakeClaude) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }
func (f *fakeClaude) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func claudeSession(t *testing.T, f *fakeClaude, tools func([]string) []ToolSpec) *harness.Session {
	t.Helper()
	p, err := NewAnthropicProvider(AnthropicConfig{Model: "claude-x", APIKey: func() string { return claudeKey }, BaseURL: f.server.URL, Pricing: openAIPricing, AllowEgress: true, Timeout: 5 * time.Second, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	registry := harness.NewRegistry()
	if err := p.Register(registry); err != nil {
		t.Fatal(err)
	}
	s, err := registry.OpenSession(context.Background(), AnthropicProviderID, harness.Scope{Workspace: "agent-memory", Run: "run-x", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func claudeTools(names ...string) func([]string) []ToolSpec {
	return func(ids []string) []ToolSpec {
		var out []ToolSpec
		for _, id := range ids {
			for _, n := range names {
				if id == n {
					out = append(out, ToolSpec{Name: id, Description: "d " + id, Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}, "additionalProperties": false}})
				}
			}
		}
		return out
	}
}

func claudeAsk(t *testing.T, s *harness.Session, prefix string, maxBytes int, offered ...string) harness.ModelAnswer {
	t.Helper()
	envelope, _ := s.Envelope(AnthropicCapability, maxBytes)
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: "PRIVATE-PROMPT do it", PrefixID: prefix, ToolSchemaIDs: offered})
	if err != nil {
		t.Fatalf("a provider problem must be a typed outcome: %v", err)
	}
	return answer
}

func TestAnthropicProviderRefusesToExistWithoutEveryRequirement(t *testing.T) {
	good := AnthropicConfig{Model: "claude-x", APIKey: func() string { return "k" }, Pricing: openAIPricing, AllowEgress: true}
	if _, err := NewAnthropicProvider(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AnthropicConfig){
		"no egress opt-in":     func(c *AnthropicConfig) { c.AllowEgress = false },
		"no key supplier":      func(c *AnthropicConfig) { c.APIKey = nil },
		"no model":             func(c *AnthropicConfig) { c.Model = "" },
		"a model with a space": func(c *AnthropicConfig) { c.Model = "claude x" },
		"a huge model":         func(c *AnthropicConfig) { c.Model = strings.Repeat("m", 101) },
		"bad prices": func(c *AnthropicConfig) {
			c.Pricing = Pricing{InputPerMTok: 1, CachedInputPerMTok: 5, OutputPerMTok: 1}
		},
	} {
		c := good
		mutate(&c)
		if _, err := NewAnthropicProvider(c); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAnthropicProbeMapsServiceStatusToLiveAccess(t *testing.T) {
	for status, want := range map[int]harness.AccessState{200: harness.AccessAvailable, 401: harness.AccessDenied, 403: harness.AccessDenied, 404: harness.AccessUnsupported, 429: harness.AccessUnavailable, 500: harness.AccessUnavailable} {
		f := newFakeClaude(t)
		f.probe = status
		s := claudeSession(t, f, nil)
		if got := s.State(AnthropicCapability); got != want {
			t.Errorf("%d: %s, want %s", status, got, want)
		}
	}
	// A missing key is denied with no network call.
	f := newFakeClaude(t)
	p, _ := NewAnthropicProvider(AnthropicConfig{Model: "claude-x", APIKey: func() string { return "" }, BaseURL: f.server.URL, Pricing: openAIPricing, AllowEgress: true})
	registry := harness.NewRegistry()
	_ = p.Register(registry)
	s, err := registry.OpenSession(context.Background(), AnthropicProviderID, harness.Scope{Workspace: "agent-memory", Run: "r", Generation: 1})
	if err != nil || s.State(AnthropicCapability) != harness.AccessDenied || f.count() != 0 {
		t.Fatalf("%v %s %d", err, s.State(AnthropicCapability), f.count())
	}
}

func TestAnthropicSendsExactlyOneStatelessRequestAndNothingElseFromTheRun(t *testing.T) {
	f := newFakeClaude(t)
	s := claudeSession(t, f, claudeTools("read_file", "search"))
	answer := claudeAsk(t, s, "", 4096, "read_file", "search", "unknown_tool")
	if answer.Outcome != harness.OutcomeOK || answer.Text != "hello" {
		t.Fatalf("%+v", answer)
	}
	req := f.last()
	if req.method != "POST" || req.path != "/v1/messages" || req.auth != claudeKey {
		t.Fatalf("request = %s %s", req.method, req.path)
	}
	var sent map[string]any
	if err := json.Unmarshal(req.body, &sent); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"temperature", "top_p", "top_k", "thinking", "stream", "system", "cache_control", "metadata"} {
		if _, present := sent[forbidden]; present {
			t.Errorf("%s was sent: %s", forbidden, req.body)
		}
	}
	msgs := sent["messages"].([]any)
	if sent["model"] != "claude-x" || sent["max_tokens"] != float64(64) || len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" || !strings.Contains(string(req.body), "PRIVATE-PROMPT do it") {
		t.Fatalf("sent = %s", req.body)
	}
	tools := sent["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "read_file" || tools[1].(map[string]any)["name"] != "search" {
		t.Fatalf("tools = %v", tools)
	}
	choice := sent["tool_choice"].(map[string]any)
	if choice["type"] != "auto" || choice["disable_parallel_tool_use"] != true {
		t.Fatalf("tool_choice = %v", choice)
	}
	schema := tools[0].(map[string]any)["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false || schema["required"].([]any)[0] != "path" {
		t.Fatalf("schema = %v", schema)
	}
}

func TestAnthropicMarksTheCacheOnlyForARunThatSuppliedAStablePrefix(t *testing.T) {
	f := newFakeClaude(t)
	s := claudeSession(t, f, nil)
	claudeAsk(t, s, strings.Repeat("a", 32), 4096)
	if !strings.Contains(string(f.last().body), "cache_control") {
		t.Fatalf("no cache marker with a prefix identity: %s", f.last().body)
	}
	claudeAsk(t, s, "", 4096)
	if strings.Contains(string(f.last().body), "cache_control") {
		t.Fatalf("a cache marker without a prefix identity: %s", f.last().body)
	}
	if strings.Contains(string(f.last().body), `"tools"`) {
		t.Fatal("tools were sent without being offered")
	}
}

func TestAnthropicReportsUsageCostAndCachedInputFromTheServiceOnly(t *testing.T) {
	f := newFakeClaude(t)
	s := claudeSession(t, f, nil)
	answer := claudeAsk(t, s, "", 4096)
	if answer.Usage.InputTokens != 140 || answer.Usage.OutputTokens != 10 || answer.Usage.CachedInputTokens != 40 || answer.Model != "claude-x" || answer.CostMicros != openAIPricing.Cost(answer.Usage) || answer.CostMicros <= 0 {
		t.Fatalf("%+v", answer)
	}
	f.set(200, `{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"abcdefgh"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`)
	if est := claudeAsk(t, s, "", 4096); est.Usage.InputTokens == 0 || est.CostMicros == 0 {
		t.Fatalf("a call with no reported usage was treated as free: %+v", est)
	}
}

func TestAnthropicMapsEveryReplyShapeToATypedAnswer(t *testing.T) {
	f := newFakeClaude(t)
	s := claudeSession(t, f, claudeTools("read_file", "clarify"))
	f.set(200, claudeToolBody("read_file", `{"path":"a.go"}`))
	if a := claudeAsk(t, s, "", 4096, "read_file"); a.Outcome != harness.OutcomeOK || a.ToolID != "read_file" || string(a.ToolArguments) != `{"path":"a.go"}` {
		t.Fatalf("a tool call: %+v", a)
	}
	f.set(200, claudeToolBody("delete_file", `{}`))
	if a := claudeAsk(t, s, "", 4096, "read_file"); a.Outcome == harness.OutcomeOK || a.ToolID != "" {
		t.Fatalf("a call to an unoffered tool: %+v", a)
	}
	f.set(200, claudeToolBody("clarify", `{"question":"Which branch?"}`))
	if a := claudeAsk(t, s, "", 4096, "clarify"); a.Outcome != harness.OutcomeOK || a.ToolID != "clarify" || a.Text != "Which branch?" || len(a.ToolArguments) != 0 {
		t.Fatalf("clarify: %+v", a)
	}
	f.set(200, claudeToolBody("clarify", `{}`))
	if a := claudeAsk(t, s, "", 4096, "clarify"); a.Outcome == harness.OutcomeOK {
		t.Fatalf("an empty clarify: %+v", a)
	}
	f.set(200, claudeToolBody("read_file", `{"path":"`+strings.Repeat("x", 300)+`"}`))
	if a := claudeAsk(t, s, "", 64, "read_file"); a.Outcome == harness.OutcomeOK || a.ToolID != "" {
		t.Fatalf("a call too big for its envelope: %+v", a)
	}
	f.set(200, claudeBody("cut off", "max_tokens", 10, 64, 0))
	if a := claudeAsk(t, s, "", 4096); a.Outcome != harness.OutcomePartial || a.Text != "cut off" {
		t.Fatalf("max tokens: %+v", a)
	}
	f.set(200, claudeBody(strings.Repeat("y", 100), "end_turn", 10, 5, 0))
	if a := claudeAsk(t, s, "", 10); a.Outcome != harness.OutcomePartial || len(a.Text) > 10 {
		t.Fatalf("a reply over its bound: %+v", a)
	}
	for _, stop := range []string{"refusal", "pause_turn"} {
		f.set(200, claudeBody("some text", stop, 10, 5, 0))
		if a := claudeAsk(t, s, "", 4096); a.Outcome == harness.OutcomeOK || a.Text != "" {
			t.Errorf("%s: %+v", stop, a)
		}
	}
	f.set(200, claudeBody("  ", "end_turn", 10, 5, 0))
	if a := claudeAsk(t, s, "", 4096); a.Outcome == harness.OutcomeOK {
		t.Fatalf("an empty reply: %+v", a)
	}
}

func TestAnthropicMapsEveryFailureToATypedOutcomeWithoutRetryingOrEchoing(t *testing.T) {
	for status, want := range map[int]harness.Outcome{401: harness.OutcomeDenied, 403: harness.OutcomeDenied, 404: harness.OutcomeUnsupported, 408: harness.OutcomeUnavailable,
		429: harness.OutcomeUnavailable, 500: harness.OutcomeUnavailable, 529: harness.OutcomeUnavailable, 400: harness.OutcomeFailed, 422: harness.OutcomeFailed} {
		f := newFakeClaude(t)
		s := claudeSession(t, f, nil)
		before := f.count()
		f.set(status, `{"type":"error","error":{"type":"x","message":"service says `+claudeKey+` PRIVATE-PROMPT"}}`)
		a := claudeAsk(t, s, "", 4096)
		if a.Outcome != want || a.Text != "" {
			t.Errorf("%d: %+v, want %s", status, a, want)
		}
		if f.count()-before != 1 {
			t.Errorf("%d: the call was made %d times; a retry is another egress and charge", status, f.count()-before)
		}
		if text := fmt.Sprintf("%+v", a); strings.Contains(text, claudeKey) || strings.Contains(text, "PRIVATE-PROMPT") || strings.Contains(text, "service says") {
			t.Errorf("%d: an outcome carries a secret or service text: %s", status, text)
		}
	}
	f := newFakeClaude(t)
	s := claudeSession(t, f, nil)
	f.server.Close()
	if a := claudeAsk(t, s, "", 4096); a.Outcome != harness.OutcomeUnavailable {
		t.Fatalf("an unreachable service: %+v", a)
	}
}

func TestAnthropicRefusesRedirectsAndHonorsDeadlinesAndCancellation(t *testing.T) {
	hops := 0
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hops++ }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/models/") {
			_, _ = w.Write([]byte(`{"id":"claude-x","type":"model","display_name":"X","created_at":"2026-01-01T00:00:00Z"}`))
			return
		}
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	p, _ := NewAnthropicProvider(AnthropicConfig{Model: "claude-x", APIKey: func() string { return claudeKey }, BaseURL: redirect.URL, Pricing: openAIPricing, AllowEgress: true})
	registry := harness.NewRegistry()
	_ = p.Register(registry)
	s, _ := registry.OpenSession(context.Background(), AnthropicProviderID, harness.Scope{Workspace: "agent-memory", Run: "r", Generation: 1})
	if a := claudeAsk(t, s, "", 4096); a.Outcome == harness.OutcomeOK || hops != 0 {
		t.Fatalf("a redirect was followed: %+v, %d hops", a, hops)
	}

	f := newFakeClaude(t)
	slow := claudeSession(t, f, nil)
	f.mu.Lock()
	f.delay = 5 * time.Second
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	envelope, _ := slow.Envelope(AnthropicCapability, 4096)
	a, err := slow.Generate(ctx, harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: "hi"})
	if err != nil || a.Outcome != harness.OutcomeTimeout {
		t.Fatalf("a deadline: %+v %v", a, err)
	}
	gone, stop := context.WithCancel(context.Background())
	stop()
	a, err = slow.Generate(gone, harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: "hi"})
	if err != nil || a.Outcome != harness.OutcomeCancelled {
		t.Fatalf("a cancelled caller: %+v %v", a, err)
	}
}

func TestAnthropicEmptyPromptAndMissingKeyMakeNoNetworkCall(t *testing.T) {
	f := newFakeClaude(t)
	s := claudeSession(t, f, nil)
	before := f.count()
	envelope, _ := s.Envelope(AnthropicCapability, 4096)
	if a, _ := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: "  "}); a.Outcome != harness.OutcomeUnsupported {
		t.Fatalf("%+v", a)
	}
	if f.count() != before {
		t.Fatal("an empty prompt reached the service")
	}
	if toolParams2, ok := toolParams([]ToolSpec{{Name: "../x", Parameters: map[string]any{}}}); ok || toolParams2 != nil {
		t.Fatal("a bad tool name was accepted")
	}
	if _, ok := toolParams([]ToolSpec{{Name: "ok"}}); ok {
		t.Fatal("a tool without parameters was accepted")
	}
}

func TestAnthropicRefusesToOfferMoreToolsThanTheCap(t *testing.T) {
	f := newFakeClaude(t)
	many := func(ids []string) []ToolSpec {
		var out []ToolSpec
		for i := 0; i < 65; i++ {
			out = append(out, ToolSpec{Name: fmt.Sprintf("tool_%d", i), Description: "d", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}})
		}
		return out
	}
	s := claudeSession(t, f, many)
	before := f.count()
	if a := claudeAsk(t, s, "", 4096, "tool_1"); a.Outcome == harness.OutcomeOK || f.count() != before {
		t.Fatalf("an over-large tool offer reached the service: %+v", a)
	}
}
