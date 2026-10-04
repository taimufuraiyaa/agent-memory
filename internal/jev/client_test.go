package jev

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

func TestChoiceRequestAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"model":"jev-1.13","answers":{"complexity":{"type":"choice","choice":"deep","confidence":0.91,"probabilities":{"light":0.02,"deep":0.98}}},"usage":{"input_tokens":20,"output_tokens":2}}`))
	}))
	defer server.Close()
	client := NewClientWithURL(server.URL)
	answers, err := client.Choose(context.Background(), "test-key", "Fix a concurrency bug", map[string]ChoiceQuestion{
		"complexity": {Instructions: "Choose task complexity", Criteria: map[string]string{"light": "small", "deep": "complex"}},
	})
	if err != nil || answers["complexity"].Choice != "deep" {
		t.Fatalf("invalid choice: %+v %v", answers, err)
	}
}

func TestChoiceRejectsMalformedAndUnknownResponses(t *testing.T) {
	for _, response := range []string{
		`{"model":"jev-1.13","answers":{"complexity":{"type":"choice","choice":"unknown","confidence":0.9,"probabilities":{"light":0.1,"deep":0.9}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"model":"jev-1.13","answers":{"complexity":{"type":"choice","choice":"deep","confidence":1.2,"probabilities":{"light":0.1,"deep":0.9}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"model":"jev-1.13","answers":{"complexity":{"type":"choice","choice":"deep","confidence":0.9,"probabilities":{"light":0.1,"deep":0.2}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"model":"jev-1.13","answers":{"complexity":{"type":"noul","noul":0.9}},"usage":{"input_tokens":1,"output_tokens":1}}`,
	} {
		t.Run(response[:45], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, response) }))
			defer server.Close()
			_, err := NewClientWithURL(server.URL).Choose(context.Background(), "secret", "task", map[string]ChoiceQuestion{"complexity": {Criteria: map[string]string{"light": "small", "deep": "complex"}}})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("malformed reply accepted or key exposed: %v", err)
			}
		})
	}
}

func TestProbeRequiresModelAndNeverLeaksKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("probe request malformed")
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"jev-latest"}]}`))
	}))
	defer server.Close()
	if err := NewClientWithURL(server.URL).Probe(context.Background(), "test-key"); err != nil {
		t.Fatal(err)
	}
}

func TestChoiceFailsClosedOnDeniedOrSlowProvider(t *testing.T) {
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("secret-key rejected"))
	}))
	defer denied.Close()
	question := map[string]ChoiceQuestion{"complexity": {Criteria: map[string]string{"light": "small", "deep": "complex"}}}
	if _, err := NewClientWithURL(denied.URL).Choose(context.Background(), "secret-key", "task", question); err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("denied response leaked or succeeded: %v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewClientWithURL(slow.URL).Choose(ctx, "secret-key", "task", question); err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("cancelled response leaked or succeeded: %v", err)
	}
}

func TestLiveTypeSafeModelProbe(t *testing.T) {
	if os.Getenv("AGENT_MEMORY_LIVE_JEV_TEST") != "1" {
		t.Skip("set AGENT_MEMORY_LIVE_JEV_TEST=1 for a no-prompt TypeSafe model probe")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	token, configured, err := jevconfig.NewTokenStore(filepath.Join(home, ".agent-memory")).Load(t.Context())
	if err != nil || !configured {
		t.Fatalf("TypeSafe credential unavailable: configured=%v err=%v", configured, err)
	}
	if err := NewClient().Probe(t.Context(), token); err != nil {
		t.Fatalf("TypeSafe model probe failed: %v", err)
	}
}

func TestLiveTypeSafeChoice(t *testing.T) {
	if os.Getenv("AGENT_MEMORY_LIVE_JEV_TEST") != "1" {
		t.Skip("set AGENT_MEMORY_LIVE_JEV_TEST=1 for one synthetic Jev decision")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	token, configured, err := jevconfig.NewTokenStore(filepath.Join(home, ".agent-memory")).Load(t.Context())
	if err != nil || !configured {
		t.Fatalf("TypeSafe credential unavailable: configured=%v err=%v", configured, err)
	}
	answers, err := NewClient().Choose(t.Context(), token, "Find the path of one source file in a local repository.", map[string]ChoiceQuestion{
		"complexity": {Instructions: "Choose a reasoning effort tier for this coding task.", Criteria: map[string]string{
			"light": "Simple lookup with a clear target.", "standard": "Several related steps.", "deep": "Difficult architecture or debugging.",
		}},
	})
	if err != nil || answers["complexity"].Choice == "" {
		t.Fatalf("TypeSafe decision failed: %v", err)
	}
}
