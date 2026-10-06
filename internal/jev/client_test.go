package jev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestEveryFailureHasAClassAndNoServiceTextOrCredential(t *testing.T) {
	question := map[string]ChoiceQuestion{"decision": {Instructions: "pick", Criteria: map[string]string{"a0": "first", "a1": "second"}}}
	const token = "token-SECRET-12345"
	respond := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			if status != http.StatusOK { // an error body tries to leak; a good one must stay valid JSON
				body += " " + token
			}
			_, _ = w.Write([]byte(body))
		}))
	}
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"unauthorized":        {http.StatusUnauthorized, "bad key", ErrDenied},
		"forbidden":           {http.StatusForbidden, "no", ErrDenied},
		"too many requests":   {http.StatusTooManyRequests, "slow down", ErrUnavailable},
		"internal error":      {http.StatusInternalServerError, "oops", ErrUnavailable},
		"bad gateway":         {http.StatusBadGateway, "oops", ErrUnavailable},
		"service unavailable": {http.StatusServiceUnavailable, "oops", ErrUnavailable},
		"bad request":         {http.StatusBadRequest, "nope", ErrRejected},
		"not found":           {http.StatusNotFound, "nope", ErrRejected},
		"unprocessable":       {http.StatusUnprocessableEntity, "nope", ErrRejected},
		"not json":            {http.StatusOK, "<html>", ErrMalformed},
		"an empty object":     {http.StatusOK, "{}", ErrMalformed},
		"no usage":            {http.StatusOK, `{"model":"jev-1","answers":{"decision":{"type":"choice","choice":"a0","confidence":0.9,"probabilities":{"a0":0.9,"a1":0.1}}}}`, ErrMalformed},
		"a foreign model":     {http.StatusOK, `{"model":"other-1","answers":{"decision":{"type":"choice","choice":"a0","confidence":0.9,"probabilities":{"a0":0.9,"a1":0.1}}},"usage":{"input_tokens":1,"output_tokens":1}}`, ErrMalformed},
		"an unknown choice":   {http.StatusOK, `{"model":"jev-1","answers":{"decision":{"type":"choice","choice":"zz","confidence":0.9,"probabilities":{"a0":0.9,"a1":0.1}}},"usage":{"input_tokens":1,"output_tokens":1}}`, ErrMalformed},
		"unbalanced odds":     {http.StatusOK, `{"model":"jev-1","answers":{"decision":{"type":"choice","choice":"a0","confidence":0.9,"probabilities":{"a0":0.9,"a1":0.9}}},"usage":{"input_tokens":1,"output_tokens":1}}`, ErrMalformed},
		"an odd probability":  {http.StatusOK, `{"model":"jev-1","answers":{"decision":{"type":"choice","choice":"a0","confidence":0.9,"probabilities":{"a0":1.5,"a1":-0.5}}},"usage":{"input_tokens":1,"output_tokens":1}}`, ErrMalformed},
	} {
		server := respond(tc.status, tc.body)
		_, err := NewClientWithURL(server.URL).Choose(context.Background(), token, "state", question)
		server.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v is not %v", name, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "bad key") || strings.Contains(err.Error(), "slow down") || strings.Contains(err.Error(), "oops") || strings.Contains(err.Error(), "<html>")) {
			t.Errorf("%s: the error leaks: %v", name, err)
		}
	}
	// A probe fails in the same classes.
	for status, want := range map[int]error{http.StatusUnauthorized: ErrDenied, http.StatusBadGateway: ErrUnavailable, http.StatusBadRequest: ErrRejected} {
		server := respond(status, "x")
		err := NewClientWithURL(server.URL).Probe(context.Background(), token)
		server.Close()
		if !errors.Is(err, want) {
			t.Errorf("probe %d: %v", status, err)
		}
	}
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"models":[{"name":"other"}]}`)) }))
	defer missing.Close()
	if err := NewClientWithURL(missing.URL).Probe(context.Background(), token); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a service without the model: %v", err)
	}
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`not json`)) }))
	defer garbled.Close()
	if err := NewClientWithURL(garbled.URL).Probe(context.Background(), token); !errors.Is(err, ErrMalformed) {
		t.Errorf("a garbled model list: %v", err)
	}
}

func TestTransportFailuresAreClassifiedByCause(t *testing.T) {
	question := map[string]ChoiceQuestion{"decision": {Criteria: map[string]string{"a0": "first", "a1": "second"}}}
	// Nothing is listening.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := closed.URL
	closed.Close()
	if _, err := NewClientWithURL(url).Choose(context.Background(), "k", "s", question); !errors.Is(err, ErrUnavailable) {
		t.Errorf("connection refused: %v", err)
	}
	// A deadline passes while the service is silent.
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server only notices a closed connection once the body is read
		<-r.Context().Done()
	}))
	defer silent.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := NewClientWithURL(silent.URL).Choose(ctx, "k", "s", question); !errors.Is(err, ErrTimeout) {
		t.Errorf("a deadline: %v", err)
	}
	// The caller goes away.
	gone, stop := context.WithCancel(context.Background())
	stop()
	if _, err := NewClientWithURL(silent.URL).Choose(gone, "k", "s", question); !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Errorf("a cancelled caller: %v", err)
	}
	// A redirect is never followed.
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
	}))
	defer hop.Close()
	if _, err := NewClientWithURL(hop.URL).Choose(context.Background(), "k", "s", question); !errors.Is(err, ErrRejected) {
		t.Errorf("a redirect: %v", err)
	}
	// An enormous reply is malformed, not read.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 70000)) }))
	defer big.Close()
	if _, err := NewClientWithURL(big.URL).Choose(context.Background(), "k", "s", question); !errors.Is(err, ErrMalformed) {
		t.Errorf("an oversize reply: %v", err)
	}
}

func TestRequestsThatBreakALimitAreNeverSent(t *testing.T) {
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { sent++ }))
	defer server.Close()
	client := NewClientWithURL(server.URL)
	ok := map[string]ChoiceQuestion{"decision": {Criteria: map[string]string{"a0": "x", "a1": "y"}}}
	many := map[string]string{}
	for i := 0; i < 33; i++ {
		many[fmt.Sprintf("a%d", i)] = "x"
	}
	for name, call := range map[string]func() error{
		"no state": func() error { _, err := client.Choose(context.Background(), "k", "", ok); return err },
		"a huge state": func() error {
			_, err := client.Choose(context.Background(), "k", strings.Repeat("s", 4001), ok)
			return err
		},
		"no questions": func() error { _, err := client.Choose(context.Background(), "k", "s", nil); return err },
		"too many questions": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"a": ok["decision"], "b": ok["decision"], "c": ok["decision"]})
			return err
		},
		"one choice": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"d": {Criteria: map[string]string{"a": "x"}}})
			return err
		},
		"too many choices": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"d": {Criteria: many}})
			return err
		},
		"a bad question id": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"bad id": ok["decision"]})
			return err
		},
		"a bad choice id": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"d": {Criteria: map[string]string{"a.b": "x", "c": "y"}}})
			return err
		},
		"long instructions": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"d": {Instructions: strings.Repeat("i", 301), Criteria: ok["decision"].Criteria}})
			return err
		},
		"a long description": func() error {
			_, err := client.Choose(context.Background(), "k", "s", map[string]ChoiceQuestion{"d": {Criteria: map[string]string{"a": strings.Repeat("d", 251), "b": "y"}}})
			return err
		},
		"no credential":               func() error { _, err := client.Choose(context.Background(), "", "s", ok); return err },
		"a credential with a newline": func() error { _, err := client.Choose(context.Background(), "k\nX-Evil: 1", "s", ok); return err },
		"a request over its size": func() error {
			big := map[string]string{}
			for i := 0; i < 32; i++ {
				big[fmt.Sprintf("a%d", i)] = strings.Repeat("d", 250)
			}
			_, err := client.Choose(context.Background(), "k", strings.Repeat("s", 3000), map[string]ChoiceQuestion{"d": {Instructions: strings.Repeat("i", 300), Criteria: big}})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if sent != 0 {
		t.Fatalf("%d requests that broke a limit were sent", sent)
	}
}
