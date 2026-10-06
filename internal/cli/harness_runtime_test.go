package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesscontext"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessmodel"
)

func harnessService(t *testing.T) (*api.Service, string) {
	t.Helper()
	svc, dir, _ := harnessServiceRoot(t)
	return svc, dir
}

func harnessServiceRoot(t *testing.T) (*api.Service, string, string) {
	t.Helper()
	dir, root := t.TempDir(), t.TempDir()
	registry := map[string]any{"projects": []any{map[string]any{"name": "agent-memory", "workspace_root": root, "db_path": filepath.Join(dir, "ws.db")}}}
	encoded, _ := json.Marshal(registry)
	if err := os.WriteFile(filepath.Join(dir, "workspaces.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &api.Service{BaseDir: dir}
	if err := api.ConfigureLocalClientProfiles(svc); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClientProfiles.Create(clientprofile.Input{ID: "claude-desktop", DisplayName: "Claude", ClientKind: clientprofile.KindClaude, ToolProfile: clientprofile.ProfileDefault}); err != nil {
		t.Fatal(err)
	}
	return svc, dir, root
}

func TestHarnessIsNotComposedUnlessExplicitlyEnabled(t *testing.T) {
	svc, _ := harnessService(t)
	t.Setenv(harnessProvidersEnv, "")
	var stderr bytes.Buffer
	gateway, closeHarness, err := buildHarnessGateway(context.Background(), svc, &stderr)
	if err != nil || gateway != nil || closeHarness == nil || stderr.Len() != 0 {
		t.Fatalf("gateway=%v err=%v stderr=%q", gateway, err, stderr.String())
	}
	closeHarness()
	if _, err := os.Stat(filepath.Join(svc.BaseDir, "harness")); !os.IsNotExist(err) {
		t.Fatalf("an unconfigured install created harness state: %v", err)
	}
	server := httptest.NewServer(api.NewMux(svc))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/v1/harness/capabilities?workspace=agent-memory")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("harness routes exist without being composed: %d", response.StatusCode)
	}
}

func TestHarnessCompositionFailsClosedOnUnsupportedConfiguration(t *testing.T) {
	svc, _ := harnessService(t)
	for _, value := range []string{"real", "gemini", "OPENAI", "FAKE", "fake,real", "openai,fake", "1"} {
		t.Setenv(harnessProvidersEnv, value)
		gateway, closeHarness, err := buildHarnessGateway(context.Background(), svc, io.Discard)
		if err == nil || gateway != nil || closeHarness != nil || !strings.Contains(err.Error(), harnessProvidersEnv) {
			t.Errorf("%q: gateway=%v err=%v", value, gateway, err)
		}
	}
	t.Setenv(harnessProvidersEnv, "fake")
	for name, broken := range map[string]*api.Service{
		"nil service":      nil,
		"no profile store": {BaseDir: t.TempDir()},
		"no base dir":      {ClientProfiles: svc.ClientProfiles},
	} {
		if gateway, _, err := buildHarnessGateway(context.Background(), broken, io.Discard); err == nil || gateway != nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestComposedFakeHarnessRunsAnEndToEndFlowOverHTTP(t *testing.T) {
	svc, _ := harnessService(t)
	t.Setenv(harnessProvidersEnv, "fake")
	var stderr bytes.Buffer
	gateway, closeHarness, err := buildHarnessGateway(context.Background(), svc, &stderr)
	if err != nil || gateway == nil {
		t.Fatalf("gateway=%v err=%v", gateway, err)
	}
	t.Cleanup(closeHarness)
	if !gateway.Fake || len(gateway.Providers) != 2 || !strings.Contains(stderr.String(), "fake providers enabled") {
		t.Fatalf("fake=%v providers=%d stderr=%q", gateway.Fake, len(gateway.Providers), stderr.String())
	}
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
	call := func(method, path string, body any) (int, map[string]any) {
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
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		_ = json.NewDecoder(response.Body).Decode(&envelope)
		return response.StatusCode, envelope.Data
	}

	code, started := call(http.MethodPost, "/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "compose-key-01", "goal": "compose and run"})
	if code != http.StatusAccepted {
		t.Fatalf("start = %d %v", code, started)
	}
	id := started["run"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(8 * time.Second)
	var run map[string]any
	for {
		_, status := call(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", nil)
		run = status["run"].(map[string]any)
		if run["state"] == "completed" || run["state"] == "failed" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run["state"] != "completed" || len(run["artifacts"].([]any)) != 1 {
		t.Fatalf("run = %v", run)
	}
	encoded, _ := json.Marshal(run)
	if strings.Contains(string(encoded), harnessFakeReply) || strings.Contains(string(encoded), "compose and run") {
		t.Fatalf("status leaked content: %s", encoded)
	}

	// The shared client profile store is what the authority consults, so deleting the
	// profile through it revokes access on the next request.
	profile, _ := svc.ClientProfiles.Get("claude-desktop")
	if err := svc.ClientProfiles.Delete("claude-desktop", profile.Revision); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", nil); code != http.StatusUnauthorized {
		t.Fatalf("a deleted client profile still has access: %d", code)
	}
	closeHarness()
	closeHarness() // idempotent
}

const openAITestKey = "sk-test-SECRET-KEY-0123456789abcdef"

// openAIEnv sets a complete, valid OpenAI composition environment.
func openAIEnv(t *testing.T) {
	t.Helper()
	t.Setenv(harnessProvidersEnv, "openai")
	t.Setenv(harnessEgressEnv, "openai")
	t.Setenv("OPENAI_API_KEY", openAITestKey)
	t.Setenv(harnessOpenAIModelEnv, "gpt-x")
	t.Setenv(harnessOpenAIPriceEnv, "2500000,250000,10000000")
	t.Setenv(harnessOpenAIClassEnv, "")
	t.Setenv(harnessOpenAIWindowEnv, "")
}

func TestOpenAICompositionDemandsEveryExplicitOptInAndFailsClosed(t *testing.T) {
	svc, dir := harnessService(t)
	for name, mutate := range map[string]func(*testing.T){
		"no egress confirmation":  func(t *testing.T) { t.Setenv(harnessEgressEnv, "") },
		"wrong egress value":      func(t *testing.T) { t.Setenv(harnessEgressEnv, "yes") },
		"egress for another host": func(t *testing.T) { t.Setenv(harnessEgressEnv, "gemini") },
		"no key":                  func(t *testing.T) { t.Setenv("OPENAI_API_KEY", "  ") },
		"no model":                func(t *testing.T) { t.Setenv(harnessOpenAIModelEnv, "") },
		"no price":                func(t *testing.T) { t.Setenv(harnessOpenAIPriceEnv, "") },
		"two prices":              func(t *testing.T) { t.Setenv(harnessOpenAIPriceEnv, "1,1") },
		"non-numeric price":       func(t *testing.T) { t.Setenv(harnessOpenAIPriceEnv, "a,b,c") },
		"negative price":          func(t *testing.T) { t.Setenv(harnessOpenAIPriceEnv, "-1,0,0") },
		"cached above input":      func(t *testing.T) { t.Setenv(harnessOpenAIPriceEnv, "100,200,300") },
		"price over the bound":    func(t *testing.T) { t.Setenv(harnessOpenAIPriceEnv, "2000000000,1,1") },
		"sensitive class":         func(t *testing.T) { t.Setenv(harnessOpenAIClassEnv, "sensitive") },
		"restricted class":        func(t *testing.T) { t.Setenv(harnessOpenAIClassEnv, "restricted") },
		"unknown class":           func(t *testing.T) { t.Setenv(harnessOpenAIClassEnv, "everything") },
		"window not a number":     func(t *testing.T) { t.Setenv(harnessOpenAIWindowEnv, "lots") },
		"window too small":        func(t *testing.T) { t.Setenv(harnessOpenAIWindowEnv, "10") },
		"window too large":        func(t *testing.T) { t.Setenv(harnessOpenAIWindowEnv, "5000000") },
		"model with whitespace":   func(t *testing.T) { t.Setenv(harnessOpenAIModelEnv, "gpt x") },
	} {
		t.Run(name, func(t *testing.T) {
			openAIEnv(t)
			mutate(t)
			var stderr bytes.Buffer
			gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{})
			if err == nil || gateway != nil || closeHarness != nil {
				t.Fatalf("composed anyway: gateway=%v err=%v", gateway, err)
			}
			if strings.Contains(err.Error(), openAITestKey) || strings.Contains(stderr.String(), openAITestKey) {
				t.Fatalf("the API key leaked: %v %q", err, stderr.String())
			}
		})
	}
	if _, statErr := os.Stat(filepath.Join(dir, "harness")); !os.IsNotExist(statErr) {
		t.Fatalf("a rejected composition still created runtime state: %v", statErr)
	}
}

// recordingOpenAI is a local stand-in for the service that records what would leave the machine.
type recordingOpenAI struct {
	mu       sync.Mutex
	requests []recordedCall
	server   *httptest.Server
}

type recordedCall struct {
	method, path, auth string
	body               []byte
}

func newRecordingOpenAI(t *testing.T) *recordingOpenAI {
	t.Helper()
	r := &recordingOpenAI{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, recordedCall{req.Method, req.URL.Path, req.Header.Get("Authorization"), raw})
		r.mu.Unlock()
		if strings.HasPrefix(req.URL.Path, "/v1/models/") {
			_, _ = w.Write([]byte(`{"id":"gpt-x"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed","model":"gpt-x-2025-01-01","output_text":"the real provider answered","usage":{"input_tokens":1500,"output_tokens":40,"input_tokens_details":{"cached_tokens":0}}}`))
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *recordingOpenAI) responses() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedCall
	for _, c := range r.requests {
		if c.path == "/v1/responses" {
			out = append(out, c)
		}
	}
	return out
}

func TestOpenAICompositionRunsEndToEndAndSendsOnlyAnAssembledRedactedPrompt(t *testing.T) {
	svc, dir, root := harnessServiceRoot(t)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("always run the tests before finishing\ncloud account key AKIAIOSFODNN7EXAMPLE must never be shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_PASSWORD=hunter2hunter2"), 0o644); err != nil {
		t.Fatal(err)
	}
	upstream := newRecordingOpenAI(t)
	openAIEnv(t)
	var stderr bytes.Buffer
	gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{openAIBaseURL: upstream.server.URL})
	if err != nil || gateway == nil {
		t.Fatalf("gateway=%v err=%v", gateway, err)
	}
	t.Cleanup(closeHarness)
	if gateway.Fake || len(gateway.Providers) != 1 || gateway.Providers[0].ID != harnessmodel.OpenAIProviderID {
		t.Fatalf("fake=%v providers=%+v", gateway.Fake, gateway.Providers)
	}
	notice := stderr.String()
	if !strings.Contains(notice, "gpt-x") || !strings.Contains(notice, "sent to OpenAI") || !strings.Contains(notice, "charges") || strings.Contains(notice, openAITestKey) {
		t.Fatalf("notice = %q", notice)
	}

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
	call := func(method, path string, body any) (int, []byte) {
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
		return response.StatusCode, raw
	}

	_, caps := call(http.MethodGet, "/api/v1/harness/capabilities?workspace=agent-memory", nil)
	if !strings.Contains(string(caps), `"fake":false`) || !strings.Contains(string(caps), `"fake_providers":false`) || strings.Contains(string(caps), openAITestKey) {
		t.Fatalf("capabilities = %s", caps)
	}
	goal := "fix the bug; the key is " + openAITestKey + " and mail me at someone@example.com"
	code, started := call(http.MethodPost, "/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "openai-key-01", "goal": goal})
	if code != http.StatusAccepted {
		t.Fatalf("start = %d %s", code, started)
	}
	var envelope struct{ Data struct{ Run map[string]any } }
	_ = json.Unmarshal(started, &envelope)
	id := envelope.Data.Run["id"].(string)
	var run map[string]any
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(15 * time.Millisecond) {
		_, raw := call(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", nil)
		var e struct{ Data struct{ Run map[string]any } }
		_ = json.Unmarshal(raw, &e)
		if run = e.Data.Run; run["state"] == "completed" || run["state"] == "failed" {
			break
		}
	}
	if run["state"] != "completed" {
		t.Fatalf("run = %v", run)
	}

	sent := upstream.responses()
	if len(sent) != 1 || sent[0].method != http.MethodPost || sent[0].auth != "Bearer "+openAITestKey {
		t.Fatalf("upstream calls = %+v", sent)
	}
	var body map[string]any
	_ = json.Unmarshal(sent[0].body, &body)
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "input,max_output_tokens,model,store,stream" || body["store"] != false || body["model"] != "gpt-x" {
		t.Fatalf("upstream body = %v", body)
	}
	input, _ := body["input"].(string)
	for _, want := range []string{harnesscontext.PolicyText, "fix the bug", "│ always run the tests before finishing", "INSTRUCTIONS"} {
		if !strings.Contains(input, want) {
			t.Errorf("the prompt lacks %q:\n%.600s", want, input)
		}
	}
	for _, leaked := range []string{openAITestKey, "someone@example.com", "hunter2", "DATABASE_PASSWORD", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(input, leaked) {
			t.Errorf("%q left the machine in the prompt", leaked)
		}
	}
	usage := harness.Usage{InputTokens: 1500, OutputTokens: 40}
	want := harnessmodel.Pricing{InputPerMTok: 2_500_000, CachedInputPerMTok: 250_000, OutputPerMTok: 10_000_000}.Cost(usage)
	spent := run["usage"].(map[string]any)
	if spent["spend_micros"] != float64(want) || spent["input_tokens"] != float64(1500) || spent["output_tokens"] != float64(40) || want <= 0 {
		t.Fatalf("usage = %v, want spend %d", spent, want)
	}
	for _, response := range [][]byte{started, caps} {
		if strings.Contains(string(response), openAITestKey) || strings.Contains(string(response), "the real provider answered") {
			t.Fatalf("a gateway response leaked the key or provider output: %s", response)
		}
	}
	if strings.Contains(stderr.String(), openAITestKey) {
		t.Fatal("the key reached stderr")
	}
	// What was persisted for the run is redacted too, not just what was sent.
	stored, err := os.ReadFile(filepath.Join(dir, "harness", "runs", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{openAITestKey, "someone@example.com", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(string(stored), leaked) {
			t.Errorf("%q was persisted in the run record", leaked)
		}
	}
	if !strings.Contains(string(stored), "fix the bug") {
		t.Fatal("the redacted goal should still be stored")
	}
}
