package harnessmodel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
)

const testKey = "sk-test-SECRET-KEY-0123456789"

var openAIPricing = Pricing{InputPerMTok: 2_500_000, CachedInputPerMTok: 250_000, OutputPerMTok: 10_000_000}

// fakeOpenAI is a local stand-in for the documented Responses API shapes.
type fakeOpenAI struct {
	mu       sync.Mutex
	status   int
	body     string
	delay    time.Duration
	probe    int
	requests []recorded
	calls    atomic.Int32
	server   *httptest.Server
}

type recorded struct {
	method, path, auth, contentType string
	body                            []byte
}

func newFakeOpenAI(t *testing.T) *fakeOpenAI {
	t.Helper()
	f := &fakeOpenAI{status: http.StatusOK, probe: http.StatusOK}
	f.body = completedBody("gpt-x", "OK", 120, 3, 0)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), raw})
		status, body, delay, probe := f.status, f.body, f.delay, f.probe
		f.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/v1/models/") {
			if probe != http.StatusOK {
				w.WriteHeader(probe)
				_, _ = w.Write([]byte(`{"error":{"message":"echo ` + string(raw) + testKey + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"` + strings.TrimPrefix(r.URL.Path, "/v1/models/") + `"}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func completedBody(model, text string, in, out, cached int) string {
	return `{"status":"completed","model":"` + model + `","output_text":"` + text + `","usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) +
		`,"input_tokens_details":{"cached_tokens":` + itoa(cached) + `}}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (f *fakeOpenAI) set(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
}

func (f *fakeOpenAI) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func newOpenAI(t *testing.T, f *fakeOpenAI) *OpenAIProvider {
	t.Helper()
	p, err := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x", APIKey: func() string { return testKey }, BaseURL: f.server.URL, Pricing: openAIPricing, AllowEgress: true, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func sessionFor(t *testing.T, p *OpenAIProvider) *harness.Session {
	t.Helper()
	registry := harness.NewRegistry()
	if err := p.Register(registry); err != nil {
		t.Fatal(err)
	}
	s, err := registry.OpenSession(context.Background(), OpenAIProviderID, harness.Scope{Workspace: "agent-memory", Run: "run-x", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAIProviderRefusesToExistWithoutExplicitEgressOptIn(t *testing.T) {
	ok := OpenAIConfig{Model: "gpt-x", APIKey: func() string { return testKey }, Pricing: openAIPricing, AllowEgress: true}
	if _, err := NewOpenAIProvider(ok); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*OpenAIConfig){
		"no egress opt-in": func(c *OpenAIConfig) { c.AllowEgress = false },
		"no model":         func(c *OpenAIConfig) { c.Model = "" },
		"model with space": func(c *OpenAIConfig) { c.Model = "gpt x" },
		"oversized model":  func(c *OpenAIConfig) { c.Model = strings.Repeat("m", 101) },
		"no key supplier":  func(c *OpenAIConfig) { c.APIKey = nil },
		"invalid pricing":  func(c *OpenAIConfig) { c.Pricing.CachedInputPerMTok = c.Pricing.InputPerMTok + 1 },
	} {
		cfg := ok
		mutate(&cfg)
		if _, err := NewOpenAIProvider(cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestOpenAIProbeMapsServiceStatusToLiveAccess(t *testing.T) {
	f := newFakeOpenAI(t)
	for status, want := range map[int]harness.AccessState{
		http.StatusOK: harness.AccessAvailable, http.StatusUnauthorized: harness.AccessDenied, http.StatusForbidden: harness.AccessDenied,
		http.StatusNotFound: harness.AccessUnsupported, http.StatusTooManyRequests: harness.AccessUnavailable,
		http.StatusInternalServerError: harness.AccessUnavailable, http.StatusServiceUnavailable: harness.AccessUnavailable, http.StatusBadRequest: harness.AccessUnavailable,
	} {
		f.mu.Lock()
		f.probe = status
		f.mu.Unlock()
		session := sessionFor(t, newOpenAI(t, f))
		if got := session.State(OpenAICapability); got != want {
			t.Errorf("status %d = %s, want %s", status, got, want)
		}
	}
	probe := f.requests[0]
	if probe.method != http.MethodGet || probe.path != "/v1/models/gpt-x" || probe.auth != "Bearer "+testKey || len(probe.body) != 0 {
		t.Fatalf("probe = %+v", probe)
	}
	// With no key there is no network call at all.
	before := f.calls.Load()
	noKey, _ := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x", APIKey: func() string { return "" }, BaseURL: f.server.URL, Pricing: openAIPricing, AllowEgress: true})
	if got := sessionFor(t, noKey).State(OpenAICapability); got != harness.AccessDenied || f.calls.Load() != before {
		t.Fatalf("no key = %s, calls %d -> %d", got, before, f.calls.Load())
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	down, _ := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x", APIKey: func() string { return testKey }, BaseURL: dead.URL, Pricing: openAIPricing, AllowEgress: true, Timeout: time.Second})
	if got := sessionFor(t, down).State(OpenAICapability); got != harness.AccessUnavailable {
		t.Fatalf("unreachable service = %s", got)
	}
}

func generate(t *testing.T, s *harness.Session, prompt string, maxBytes int) harness.ModelAnswer {
	t.Helper()
	envelope, err := s.Envelope(OpenAICapability, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: prompt})
	if err != nil {
		t.Fatalf("a provider problem must be a typed outcome, not an error: %v", err)
	}
	return answer
}

func TestOpenAISendsExactlyAStatelessTextRequest(t *testing.T) {
	f := newFakeOpenAI(t)
	s := sessionFor(t, newOpenAI(t, f))
	answer := generate(t, s, "the assembled prompt", 4096)
	if answer.Outcome != harness.OutcomeOK || answer.Text != "OK" {
		t.Fatalf("answer = %+v", answer)
	}
	sent := f.last()
	if sent.method != http.MethodPost || sent.path != "/v1/responses" || sent.auth != "Bearer "+testKey || sent.contentType != "application/json" {
		t.Fatalf("request = %+v", sent)
	}
	var body map[string]any
	if err := json.Unmarshal(sent.body, &body); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"input", "max_output_tokens", "model", "store", "stream"}) {
		t.Fatalf("the upstream request carries unexpected fields: %v", keys)
	}
	if body["input"] != "the assembled prompt" || body["model"] != "gpt-x" || body["store"] != false || body["stream"] != false || body["max_output_tokens"] != float64(64) {
		t.Fatalf("body = %v", body)
	}
	// The output cap is clamped to the client's bound.
	envelope, _ := s.Envelope(OpenAICapability, 4096)
	if _, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: harness.MaxOutputTokenLimit, Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	var clamped map[string]any
	_ = json.Unmarshal(f.last().body, &clamped)
	if clamped["max_output_tokens"] != float64(4096) {
		t.Fatalf("max_output_tokens = %v", clamped["max_output_tokens"])
	}
}

func TestOpenAIMapsEveryResponseToATypedOutcome(t *testing.T) {
	f := newFakeOpenAI(t)
	s := sessionFor(t, newOpenAI(t, f))
	good := completedBody("gpt-x", "fine", 100, 4, 0)
	for name, tc := range map[string]struct {
		status int
		body   string
		want   harness.Outcome
	}{
		"completed":             {200, good, harness.OutcomeOK},
		"snapshot of the alias": {200, completedBody("gpt-x-2025-01-01", "fine", 100, 4, 0), harness.OutcomeOK},
		"incomplete":            {200, `{"status":"incomplete","model":"gpt-x","output_text":"cut off","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":64}}`, harness.OutcomePartial},
		"wrong model":           {200, completedBody("other", "fine", 1, 1, 0), harness.OutcomeFailed},
		"prefix lookalike":      {200, completedBody("gpt-xy", "fine", 1, 1, 0), harness.OutcomeFailed},
		"failed status":         {200, `{"status":"failed","model":"gpt-x","output_text":"x"}`, harness.OutcomeFailed},
		"empty text":            {200, completedBody("gpt-x", "   ", 1, 1, 0), harness.OutcomeFailed},
		"malformed JSON":        {200, `{"status":`, harness.OutcomeFailed},
		"negative usage":        {200, completedBody("gpt-x", "x", -5, 1, 0), harness.OutcomeFailed},
		"cached over input":     {200, completedBody("gpt-x", "x", 10, 1, 11), harness.OutcomeFailed},
		"unauthorized":          {401, `{"error":{"message":"bad key ` + testKey + `"}}`, harness.OutcomeDenied},
		"forbidden":             {403, `{}`, harness.OutcomeDenied},
		"model not found":       {404, `{}`, harness.OutcomeUnsupported},
		"rate limited":          {429, `{}`, harness.OutcomeUnavailable},
		"server error":          {500, `{}`, harness.OutcomeUnavailable},
		"bad gateway":           {502, `{}`, harness.OutcomeUnavailable},
		"unavailable":           {503, `{}`, harness.OutcomeUnavailable},
		"request timeout":       {408, `{}`, harness.OutcomeUnavailable},
		"bad request":           {400, `{}`, harness.OutcomeFailed},
		"unprocessable":         {422, `{}`, harness.OutcomeFailed},
	} {
		f.set(tc.status, tc.body)
		answer := generate(t, s, "prompt", 4096)
		if answer.Outcome != tc.want {
			t.Errorf("%s: outcome %s, want %s", name, answer.Outcome, tc.want)
		}
		if tc.want != harness.OutcomeOK && tc.want != harness.OutcomePartial && (answer.Text != "" || strings.Contains(answer.Model, "SECRET")) {
			t.Errorf("%s: a non-result outcome carried content: %+v", name, answer)
		}
	}
}

func TestOpenAIReportsUsageCostAndCacheAndEstimatesWhenUsageIsMissing(t *testing.T) {
	f := newFakeOpenAI(t)
	s := sessionFor(t, newOpenAI(t, f))
	f.set(200, completedBody("gpt-x-2025-01-01", "answer", 2000, 50, 1500))
	answer := generate(t, s, "prompt", 4096)
	usage := harness.Usage{InputTokens: 2000, OutputTokens: 50, CachedInputTokens: 1500}
	if answer.Usage != usage || answer.CostMicros != openAIPricing.Cost(usage) || answer.CostMicros <= 0 || answer.Model != "gpt-x-2025-01-01" {
		t.Fatalf("answer = %+v", answer)
	}
	// No usage object: cost is estimated from sizes, never zero.
	f.set(200, `{"status":"completed","model":"gpt-x","output_text":"twelve chars"}`)
	prompt := strings.Repeat("p", 4000)
	answer = generate(t, s, prompt, 4096)
	want := harness.Usage{InputTokens: 1000, OutputTokens: 3}
	if answer.Usage != want || answer.CostMicros != openAIPricing.Cost(want) || answer.CostMicros <= 0 {
		t.Fatalf("estimated answer = %+v", answer)
	}
}

func TestOpenAINeverEchoesTheKeyThePromptOrServiceTextInAnOutcome(t *testing.T) {
	f := newFakeOpenAI(t)
	s := sessionFor(t, newOpenAI(t, f))
	const secretPrompt = "PRIVATE-PROMPT-CONTENT"
	f.set(401, `{"error":{"message":"invalid key `+testKey+` for prompt `+secretPrompt+`"}}`)
	answer := generate(t, s, secretPrompt, 4096)
	wire := strings.Join([]string{string(answer.Outcome), answer.Text, answer.Model, string(answer.ToolID)}, "|")
	if answer.Outcome != harness.OutcomeDenied || strings.Contains(wire, testKey) || strings.Contains(wire, secretPrompt) || strings.Contains(wire, "invalid key") {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestOpenAIEmptyPromptAndMissingKeyMakeNoNetworkCall(t *testing.T) {
	f := newFakeOpenAI(t)
	s := sessionFor(t, newOpenAI(t, f))
	before := f.calls.Load()
	if got := generate(t, s, "   ", 4096); got.Outcome != harness.OutcomeUnsupported {
		t.Fatalf("empty prompt = %s", got.Outcome)
	}
	if f.calls.Load() != before {
		t.Fatal("an empty prompt reached the service")
	}
	var key atomic.Value
	key.Store(testKey)
	p, _ := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x", APIKey: func() string { return key.Load().(string) }, BaseURL: f.server.URL, Pricing: openAIPricing, AllowEgress: true})
	session := sessionFor(t, p)
	key.Store("") // the key disappears after the session opened
	before = f.calls.Load()
	if got := generate(t, session, "prompt", 4096); got.Outcome != harness.OutcomeDenied || f.calls.Load() != before {
		t.Fatalf("missing key = %s, calls %d -> %d", got.Outcome, before, f.calls.Load())
	}
}

func TestOpenAIDeadlinesCancellationAndTransportFailuresAreTyped(t *testing.T) {
	f := newFakeOpenAI(t)
	p := newOpenAI(t, f)
	registry := harness.NewRegistry()
	_ = p.Register(registry)
	s, err := registry.OpenSession(context.Background(), OpenAIProviderID, harness.Scope{Workspace: "agent-memory", Run: "run-t", Generation: 1}, harness.WithCallTimeout(150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f.mu.Lock()
	f.delay = 5 * time.Second
	f.mu.Unlock()
	started := time.Now()
	if got := generate(t, s, "prompt", 4096); got.Outcome != harness.OutcomeTimeout || time.Since(started) > 2*time.Second {
		t.Fatalf("a slow service = %s after %v", got.Outcome, time.Since(started))
	}
	f.mu.Lock()
	f.delay = 0
	f.mu.Unlock()
	f.server.CloseClientConnections()
	f.server.Close()
	if got := generate(t, s, "prompt", 4096); got.Outcome != harness.OutcomeUnavailable {
		t.Fatalf("a dead service = %s", got.Outcome)
	}
}

func TestOpenAIRefusesRedirectsSoTheKeyCannotBeForwarded(t *testing.T) {
	elsewhere := newFakeOpenAI(t)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/models/") {
			_, _ = w.Write([]byte(`{"id":"gpt-x"}`))
			return
		}
		http.Redirect(w, r, elsewhere.server.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	p, _ := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x", APIKey: func() string { return testKey }, BaseURL: redirector.URL, Pricing: openAIPricing, AllowEgress: true})
	s := sessionFor(t, p)
	if got := generate(t, s, "prompt", 4096); got.Outcome == harness.OutcomeOK || elsewhere.calls.Load() != 0 {
		t.Fatalf("a redirect was followed: %s, other server calls=%d", got.Outcome, elsewhere.calls.Load())
	}
}

func TestOpenAIBoundsAHostileOrOversizedResponse(t *testing.T) {
	f := newFakeOpenAI(t)
	s := sessionFor(t, newOpenAI(t, f))
	f.set(200, `{"status":"completed","model":"gpt-x","output_text":"`+strings.Repeat("x", 2<<20)+`"}`)
	if got := generate(t, s, "prompt", 4096); got.Outcome != harness.OutcomeFailed {
		t.Fatalf("a 2 MB body = %s", got.Outcome)
	}
	// Text longer than the envelope is cut on a rune boundary and reported partial.
	f.set(200, completedBody("gpt-x", strings.Repeat("é", 100), 10, 100, 0))
	answer := generate(t, s, "prompt", 51)
	if answer.Outcome != harness.OutcomePartial || len(answer.Text) > 51 || len(answer.Text)%2 != 0 || !strings.HasPrefix(answer.Text, "é") {
		t.Fatalf("answer = %d bytes %s", len(answer.Text), answer.Outcome)
	}
	if got, cut := boundText("héllo", 2); got != "h" || !cut {
		t.Fatalf("boundText = %q %v", got, cut)
	}
	if got, cut := boundText("abc", 3); got != "abc" || cut {
		t.Fatalf("boundText = %q %v", got, cut)
	}
}

// ---- conformance across families ----

// altFamily is a differently shaped fake: it answers by streaming chunks into a bounded
// buffer, reports Partial when it has to cut, and ignores any prefix hint.
type altFamily struct{ closed atomic.Bool }

func (a *altFamily) Probe(_ context.Context, sc harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "alt-model", Scope: sc, Revision: 9,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"generation": harness.AccessAvailable}}, nil
}
func (a *altFamily) Close() error { a.closed.Store(true); return nil }
func (a *altFamily) Generate(ctx context.Context, q harness.ModelRequest) (harness.ModelAnswer, error) {
	if err := ctx.Err(); err != nil {
		return harness.ModelAnswer{Envelope: q.Envelope, Outcome: harness.OutcomeCancelled}, nil
	}
	text, outcome := "streamed", harness.OutcomeOK
	if len(text) > q.MaxBytes {
		text, outcome = text[:q.MaxBytes], harness.OutcomePartial
	}
	return harness.ModelAnswer{Envelope: q.Envelope, Outcome: outcome, Text: text, Model: "alt-1", CostMicros: 3,
		Usage: harness.Usage{InputTokens: len(q.Prompt)/4 + 1, OutputTokens: len(text)/4 + 1}}, nil
}

func TestEveryModelFamilyPassesTheSameConformanceSuite(t *testing.T) {
	spec := harnesstest.ModelSpec{Capability: "generation", Prompt: "a prompt every family can answer"}
	families := map[string]func(*testing.T) *harness.Session{
		"scripted fake": func(t *testing.T) *harness.Session {
			registry := harness.NewRegistry()
			if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-a", harness.KindModel, "generation"),
				harnesstest.Behavior{Script: []harnesstest.Step{{Text: "ok"}}, Usage: harness.Usage{InputTokens: 12, OutputTokens: 1}, CostMicros: 2, ModelName: "fake-1"}); err != nil {
				t.Fatal(err)
			}
			return mustOpen(t, registry, "fake-a")
		},
		"streaming-style fake": func(t *testing.T) *harness.Session {
			registry := harness.NewRegistry()
			manifest := harness.Manifest{Version: harness.ContractVersion, ID: "alt-model", Kind: harness.KindModel, Capabilities: []harness.CapabilityID{"generation"}}
			if err := registry.Register(manifest, func() (harness.Provider, error) { return &altFamily{}, nil }); err != nil {
				t.Fatal(err)
			}
			return mustOpen(t, registry, "alt-model")
		},
		"openai adapter": func(t *testing.T) *harness.Session {
			f := newFakeOpenAI(t)
			f.set(200, completedBody("gpt-x", "ok", 40, 2, 0))
			registry := harness.NewRegistry()
			if err := newOpenAI(t, f).Register(registry); err != nil {
				t.Fatal(err)
			}
			return mustOpen(t, registry, OpenAIProviderID)
		},
	}
	for name, open := range families {
		t.Run(name, func(t *testing.T) { harnesstest.ModelConformance(t, open, spec) })
	}
}

func mustOpen(t *testing.T, registry *harness.Registry, id harness.ProviderID) *harness.Session {
	t.Helper()
	s, err := registry.OpenSession(context.Background(), id, harness.Scope{Workspace: "agent-memory", Run: "run-c", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestOpenAILive makes one tiny real call. It is skipped unless the operator opts in with
// an explicit switch, a key and a model, so a test run can never spend money by accident.
func TestOpenAILive(t *testing.T) {
	model, key := os.Getenv("AGENT_MEMORY_LIVE_OPENAI_MODEL"), os.Getenv("OPENAI_API_KEY")
	if os.Getenv("AGENT_MEMORY_LIVE_OPENAI") != "1" || model == "" || key == "" {
		t.Skip("set AGENT_MEMORY_LIVE_OPENAI=1, AGENT_MEMORY_LIVE_OPENAI_MODEL and OPENAI_API_KEY to run the live check; it costs a few tokens")
	}
	p, err := NewOpenAIProvider(OpenAIConfig{Model: model, APIKey: func() string { return key }, Pricing: Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 1_000_000, OutputPerMTok: 1_000_000}, AllowEgress: true, Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s := sessionFor(t, p)
	if s.State(OpenAICapability) != harness.AccessAvailable {
		t.Fatalf("the account cannot use %s: %s", model, s.State(OpenAICapability))
	}
	envelope, _ := s.Envelope(OpenAICapability, 4096)
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 16, Prompt: "Reply with the single word OK."})
	if err != nil || (answer.Outcome != harness.OutcomeOK && answer.Outcome != harness.OutcomePartial) || answer.Text == "" || answer.Usage.InputTokens <= 0 {
		t.Fatalf("live answer = %+v, %v", answer, err)
	}
	t.Logf("live call ok: model=%s tokens in/out=%d/%d", answer.Model, answer.Usage.InputTokens, answer.Usage.OutputTokens)
}
