package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

const jevTestToken = "jev-test-token-SECRET-9"

// fakeTypeSafe answers model listings and decisions, recording every decision body.
type fakeTypeSafe struct {
	mu     sync.Mutex
	bodies []string
	server *httptest.Server
}

func newFakeTypeSafe(t *testing.T) *fakeTypeSafe {
	t.Helper()
	f := &fakeTypeSafe{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"models":[{"name":"jev-latest"}]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, string(raw))
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"model":"jev-1.13","usage":{"input_tokens":1,"output_tokens":1},"answers":{"decision":{"type":"choice","choice":"a0","confidence":0.9,"probabilities":{"a0":0.9,"a1":0.1}}}}`))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func jevEnv(t *testing.T, kinds string) {
	t.Helper()
	openAIEnv(t)
	t.Setenv(harnessJevEnv, kinds)
	t.Setenv(harnessJevEgressEnv, "typesafe")
	t.Setenv(harnessJevBudgetEnv, "")
	t.Setenv(harnessJevRateEnv, "")
}

func TestJevDecisionsDemandEveryOptInAndFailClosed(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T){
		"an unknown decision":         func(t *testing.T) { t.Setenv(harnessJevEnv, "model,telepathy") },
		"a decision with no consumer": func(t *testing.T) { t.Setenv(harnessJevEnv, "command_risk") },
		"a duplicate":                 func(t *testing.T) { t.Setenv(harnessJevEnv, "model,model") },
		"no egress confirmation":      func(t *testing.T) { t.Setenv(harnessJevEgressEnv, "") },
		"the wrong confirmation":      func(t *testing.T) { t.Setenv(harnessJevEgressEnv, "openai") },
		"a bad budget":                func(t *testing.T) { t.Setenv(harnessJevBudgetEnv, "many") },
		"a budget of zero":            func(t *testing.T) { t.Setenv(harnessJevBudgetEnv, "0") },
		"a rate over the bound":       func(t *testing.T) { t.Setenv(harnessJevRateEnv, "99999") },
	} {
		t.Run(name, func(t *testing.T) {
			svc, dir := harnessService(t)
			if err := jevconfig.NewTokenStore(dir).Save(context.Background(), jevTestToken); err != nil {
				t.Fatal(err)
			}
			jevEnv(t, "model")
			mutate(t)
			var stderr bytes.Buffer
			gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{})
			if err == nil || gateway != nil || closeHarness != nil {
				t.Fatalf("composed anyway: %v %v", gateway, err)
			}
			if strings.Contains(err.Error(), jevTestToken) || strings.Contains(stderr.String(), jevTestToken) {
				t.Fatal("the credential leaked")
			}
		})
	}
	t.Run("no stored credential", func(t *testing.T) {
		svc, _ := harnessService(t)
		jevEnv(t, "model")
		if gateway, _, err := buildHarnessGatewayWith(context.Background(), svc, &bytes.Buffer{}, harnessBuildOptions{}); err == nil || gateway != nil {
			t.Fatalf("composed without a credential: %v %v", gateway, err)
		}
	})
	t.Run("fake mode has nothing to advise", func(t *testing.T) {
		svc, dir := harnessService(t)
		_ = jevconfig.NewTokenStore(dir).Save(context.Background(), jevTestToken)
		jevEnv(t, "model")
		t.Setenv(harnessProvidersEnv, "fake")
		gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &bytes.Buffer{}, harnessBuildOptions{})
		if err != nil || gateway.Decisions != nil {
			t.Fatalf("%v %v", gateway, err)
		}
		closeHarness()
	})
}

func TestWithoutTheOptInNothingIsComposedOrSent(t *testing.T) {
	svc, dir := harnessService(t)
	_ = jevconfig.NewTokenStore(dir).Save(context.Background(), jevTestToken)
	typesafe := newFakeTypeSafe(t)
	openAIEnv(t)
	upstream := newRecordingOpenAI(t)
	gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &bytes.Buffer{}, harnessBuildOptions{openAIBaseURL: upstream.server.URL, jevBaseURL: typesafe.server.URL})
	if err != nil || gateway.Decisions != nil || len(gateway.Providers) != 1 {
		t.Fatalf("%v %v", gateway, err)
	}
	closeHarness()
	if len(typesafe.bodies) != 0 {
		t.Fatal("TypeSafe was contacted without the opt-in")
	}
}

func TestJevDecisionsAreComposedAndTheirHealthIsReportedWithoutContent(t *testing.T) {
	svc, dir := harnessService(t)
	if err := jevconfig.NewTokenStore(dir).Save(context.Background(), jevTestToken); err != nil {
		t.Fatal(err)
	}
	typesafe := newFakeTypeSafe(t)
	upstream := newRecordingOpenAI(t)
	jevEnv(t, "model, visibility, cache")
	var stderr bytes.Buffer
	gateway, closeHarness, err := buildHarnessGatewayWith(context.Background(), svc, &stderr, harnessBuildOptions{openAIBaseURL: upstream.server.URL, jevBaseURL: typesafe.server.URL})
	if err != nil || gateway == nil || gateway.Decisions == nil {
		t.Fatalf("%v %v", gateway, err)
	}
	t.Cleanup(closeHarness)
	notice := stderr.String()
	if !strings.Contains(notice, "Jev decisions enabled") || !strings.Contains(notice, "reachable") || strings.Contains(notice, jevTestToken) {
		t.Fatalf("notice = %q", notice)
	}
	ids := map[string]bool{}
	for _, p := range gateway.Providers {
		ids[string(p.ID)] = true
	}
	if !ids["typesafe-jev"] || !ids["openai-text"] && len(ids) != 2 {
		t.Fatalf("providers = %v", ids)
	}
	status := gateway.Decisions()
	raw, _ := json.Marshal(status)
	if status["available"] != true || status["provider_paused"] != false || strings.Contains(string(raw), jevTestToken) {
		t.Fatalf("status = %s", raw)
	}
}
