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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessmodel"
)

const anthropicTestKey = "sk-ant-test-SECRET-KEY-0123456789"

func anthropicEnv(t *testing.T) {
	t.Helper()
	t.Setenv(harnessProvidersEnv, "anthropic")
	t.Setenv(harnessEgressEnv, "anthropic")
	t.Setenv("ANTHROPIC_API_KEY", anthropicTestKey)
	t.Setenv(harnessAnthropicModelEnv, "claude-x")
	t.Setenv(harnessAnthropicPriceEnv, "3000000,300000,15000000")
	t.Setenv(harnessAnthropicClassEnv, "")
	t.Setenv(harnessAnthropicWindowEnv, "")
}

func TestAnthropicCompositionDemandsEveryExplicitOptInAndFailsClosed(t *testing.T) {
	svc, dir := harnessService(t)
	for name, mutate := range map[string]func(*testing.T){
		"no egress confirmation": func(t *testing.T) { t.Setenv(harnessEgressEnv, "") },
		"egress for openai":      func(t *testing.T) { t.Setenv(harnessEgressEnv, "openai") },
		"wrong egress value":     func(t *testing.T) { t.Setenv(harnessEgressEnv, "yes") },
		"no key":                 func(t *testing.T) { t.Setenv("ANTHROPIC_API_KEY", "  ") },
		"no model":               func(t *testing.T) { t.Setenv(harnessAnthropicModelEnv, "") },
		"no price":               func(t *testing.T) { t.Setenv(harnessAnthropicPriceEnv, "") },
		"two prices":             func(t *testing.T) { t.Setenv(harnessAnthropicPriceEnv, "1,1") },
		"cached above input":     func(t *testing.T) { t.Setenv(harnessAnthropicPriceEnv, "100,200,300") },
		"sensitive class":        func(t *testing.T) { t.Setenv(harnessAnthropicClassEnv, "sensitive") },
		"window too small":       func(t *testing.T) { t.Setenv(harnessAnthropicWindowEnv, "10") },
		"window too large":       func(t *testing.T) { t.Setenv(harnessAnthropicWindowEnv, "5000000") },
		"model with whitespace":  func(t *testing.T) { t.Setenv(harnessAnthropicModelEnv, "claude x") },
	} {
		t.Run(name, func(t *testing.T) {
			anthropicEnv(t)
			mutate(t)
			var stderr bytes.Buffer
			gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{})
			if err == nil || gateway != nil || closeHarness != nil {
				t.Fatalf("composed anyway: gateway=%v err=%v", gateway, err)
			}
			if strings.Contains(err.Error(), anthropicTestKey) || strings.Contains(stderr.String(), anthropicTestKey) {
				t.Fatalf("the API key leaked: %v %q", err, stderr.String())
			}
		})
	}
	if _, statErr := os.Stat(filepath.Join(dir, "harness")); !os.IsNotExist(statErr) {
		t.Fatalf("a rejected composition still created runtime state: %v", statErr)
	}
}

func TestAnthropicCompositionRunsEndToEndWithOnlyARedactedPromptAndTheKeyInTheHeader(t *testing.T) {
	svc, _, root := harnessServiceRoot(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_PASSWORD=hunter2hunter2"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var messages []recordedCall
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/models/") {
			_, _ = w.Write([]byte(`{"id":"claude-x","type":"model","display_name":"X","created_at":"2026-01-01T00:00:00Z"}`))
			return
		}
		mu.Lock()
		messages = append(messages, recordedCall{r.Method, r.URL.Path, r.Header.Get("x-api-key"), raw})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"the real claude answered"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1500,"output_tokens":40}}`))
	}))
	defer upstream.Close()

	anthropicEnv(t)
	var stderr bytes.Buffer
	gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{anthropicBaseURL: upstream.URL})
	if err != nil || gateway == nil {
		t.Fatalf("gateway=%v err=%v", gateway, err)
	}
	t.Cleanup(closeHarness)
	if gateway.Fake || len(gateway.Providers) != 1 || gateway.Providers[0].ID != harnessmodel.AnthropicProviderID {
		t.Fatalf("fake=%v providers=%+v", gateway.Fake, gateway.Providers)
	}
	if n := stderr.String(); !strings.Contains(n, "claude-x") || !strings.Contains(n, "sent to Anthropic") || strings.Contains(n, anthropicTestKey) {
		t.Fatalf("notice = %q", n)
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
	code, started := call(http.MethodPost, "/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "claude-key-01", "goal": "fix the bug; the key is " + anthropicTestKey + " mail someone@example.com"})
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
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 1 || messages[0].auth != anthropicTestKey || messages[0].path != "/v1/messages" {
		t.Fatalf("upstream calls = %+v", messages)
	}
	body := string(messages[0].body)
	for _, leaked := range []string{anthropicTestKey, "someone@example.com", "hunter2", "DATABASE_PASSWORD"} {
		if strings.Contains(body, leaked) {
			t.Errorf("%q left the machine", leaked)
		}
	}
	if !strings.Contains(body, "fix the bug") {
		t.Fatalf("the redacted goal is missing: %.300s", body)
	}
	want := harnessmodel.Pricing{InputPerMTok: 3_000_000, CachedInputPerMTok: 300_000, OutputPerMTok: 15_000_000}.Cost(harness.Usage{InputTokens: 1500, OutputTokens: 40})
	spent := run["usage"].(map[string]any)
	if spent["spend_micros"] != float64(want) || want <= 0 {
		t.Fatalf("usage = %v, want spend %d", spent, want)
	}
	if strings.Contains(string(started), anthropicTestKey) || strings.Contains(stderr.String(), anthropicTestKey) {
		t.Fatal("the key leaked")
	}
}
