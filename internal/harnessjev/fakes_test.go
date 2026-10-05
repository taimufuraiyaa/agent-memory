package harnessjev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
)

const testToken = "test-token-SECRET-4711"

type sentQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type sentRequest struct {
	Model     string                  `json:"model"`
	State     string                  `json:"state"`
	Questions map[string]sentQuestion `json:"questions"`
}

type captured struct {
	Method, Path, Auth string
	Body               []byte
	Request            sentRequest
}

// fakeService is a stand-in for TypeSafe's System One API that records every request and
// answers as scripted.
type fakeService struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []captured
	// answer picks the reply for a question. The default chooses the first criterion with a
	// clear margin and gives the rest an even share of what is left.
	answer func(q sentQuestion) (choice string, probabilities map[string]float64, confidence float64)
	// status, when set, is returned with a body that tries to leak text.
	status int
	delay  time.Duration
	// postStatus and postDelay apply to decisions only, so the model listing a session is
	// opened with still succeeds.
	postStatus int
	postDelay  time.Duration
	// models is the body of the model listing.
	models string
	// raw, when set, is returned verbatim with a 200.
	raw string
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	f := &fakeService{t: t, models: `{"models":[{"name":"jev-latest"}]}`}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeService) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c := captured{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: body}
	_ = json.Unmarshal(body, &c.Request)
	f.mu.Lock()
	f.requests = append(f.requests, c)
	status, delay, models, raw, answer := f.status, f.delay, f.models, f.raw, f.answer
	if r.Method == http.MethodPost {
		if f.postStatus != 0 {
			status = f.postStatus
		}
		if f.postDelay != 0 {
			delay = f.postDelay
		}
	}
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("service says: the token " + testToken + " and the prompt were leaked"))
		return
	}
	if r.URL.Path == "/v1/models" {
		_, _ = w.Write([]byte(models))
		return
	}
	if raw != "" {
		_, _ = w.Write([]byte(raw))
		return
	}
	question := c.Request.Questions[questionID]
	choice, probabilities, confidence := defaultAnswer(question)
	if answer != nil {
		choice, probabilities, confidence = answer(question)
	}
	reply := map[string]any{"model": "jev-1.13", "usage": map[string]int{"input_tokens": 10, "output_tokens": 2},
		"answers": map[string]any{questionID: map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probabilities}}}
	_ = json.NewEncoder(w).Encode(reply)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// defaultAnswer chooses the first criterion with 0.9 and shares 0.1 among the others.
func defaultAnswer(q sentQuestion) (string, map[string]float64, float64) {
	keys := sortedKeys(q.Criteria)
	return odds(keys, map[string]float64{keys[0]: 0.9}, 0.9)
}

// odds builds a probability map: the given shares, and the remainder spread evenly.
func odds(keys []string, shares map[string]float64, confidence float64) (string, map[string]float64, float64) {
	total, rest := 0.0, 0
	for _, k := range keys {
		if p, ok := shares[k]; ok {
			total += p
		} else {
			rest++
		}
	}
	probabilities := map[string]float64{}
	best, bestP := "", -1.0
	for _, k := range keys {
		p, ok := shares[k]
		if !ok && rest > 0 {
			p = (1 - total) / float64(rest)
		}
		probabilities[k] = p
		if p > bestP {
			best, bestP = k, p
		}
	}
	return best, probabilities, confidence
}

func (f *fakeService) posts() []captured {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []captured
	for _, c := range f.requests {
		if c.Method == http.MethodPost {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeService) last(t *testing.T) captured {
	t.Helper()
	posts := f.posts()
	if len(posts) == 0 {
		t.Fatal("the service was never asked")
	}
	return posts[len(posts)-1]
}

func (f *fakeService) set(fn func(*fakeService)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func newProvider(t *testing.T, f *fakeService, token func(context.Context) (string, error)) *Provider {
	t.Helper()
	if token == nil {
		token = func(context.Context) (string, error) { return testToken, nil }
	}
	p, err := NewProvider(Config{Client: jev.NewClientWithURL(f.srv.URL), Token: token, AllowEgress: true})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func openSession(t *testing.T, p *Provider) *harness.Session {
	t.Helper()
	registry := harness.NewRegistry()
	if err := p.Register(registry); err != nil {
		t.Fatal(err)
	}
	s, err := registry.OpenSession(context.Background(), ProviderID, harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newService(t *testing.T, f *fakeService, tweak ...func(*harnessdecide.Config)) (*harnessdecide.Service, *harness.Session) {
	t.Helper()
	session := openSession(t, newProvider(t, f, nil))
	cfg := harnessdecide.Config{Asker: session, Capability: Capability, MaxClass: harnessdecide.ClassInternal, Enabled: harnessdecide.Kinds(), RunBudget: 1000, PerMinute: 1000, MaxInFlight: 8}
	for _, fn := range tweak {
		fn(&cfg)
	}
	s, err := harnessdecide.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, session
}

func chunks(prefix string, n int) []harnessdecide.Item {
	out := make([]harnessdecide.Item, n)
	for i := range out {
		out[i] = harnessdecide.Item{ID: prefix + string(rune('a'+i)), Class: "memory", Facts: harnessdecide.Facts{"t": 100 + i, "r": 50 + i}}
	}
	return out
}

func named(ids ...string) []harnessdecide.Item {
	out := make([]harnessdecide.Item, len(ids))
	for i, id := range ids {
		out[i] = harnessdecide.Item{ID: id}
	}
	return out
}

// kindCall is one request of a kind with valid inputs, returning the advice as text.
type kindCall struct {
	kind harnessdecide.Kind
	run  func(s *harnessdecide.Service, secret string) (string, harnessdecide.Outcome)
}

func kindCalls() []kindCall {
	ctx := context.Background()
	join := func(ids []string) string {
		out := ""
		for i, id := range ids {
			if i > 0 {
				out += ","
			}
			out += id
		}
		return out
	}
	return []kindCall{
		{harnessdecide.KindVisibility, func(s *harnessdecide.Service, secret string) (string, harnessdecide.Outcome) {
			got, out := s.Visibility(ctx, chunks(secret+"/", 6))
			return join(got), out
		}},
		{harnessdecide.KindModel, func(s *harnessdecide.Service, _ string) (string, harnessdecide.Outcome) {
			got, out := s.Model(ctx, []harnessdecide.Item{{ID: "alpha-model", Facts: harnessdecide.Facts{"c": 2, "l": 1}}, {ID: "beta-model", Facts: harnessdecide.Facts{"c": 5}}})
			return got, out
		}},
		{harnessdecide.KindTools, func(s *harnessdecide.Service, _ string) (string, harnessdecide.Outcome) {
			got, out := s.Tools(ctx, []harnessdecide.Item{{ID: "read_file", Facts: harnessdecide.Facts{"t": 220, "u": 3}}, {ID: "list_dir"}, {ID: "search"}, {ID: "git_status"}}, 2)
			return join(got), out
		}},
		{harnessdecide.KindCache, func(s *harnessdecide.Service, _ string) (string, harnessdecide.Outcome) {
			got, out := s.Cache(ctx, harnessdecide.Facts{"h": 70, "n": 9, "p": 4000})
			return string(got), out
		}},
		{harnessdecide.KindCommandRisk, func(s *harnessdecide.Service, _ string) (string, harnessdecide.Outcome) {
			got, out := s.CommandRisk(ctx, "run_command", harnessdecide.Facts{"np": 1, "n": 2})
			return string(got), out
		}},
		{harnessdecide.KindFileSensitivity, func(s *harnessdecide.Service, _ string) (string, harnessdecide.Outcome) {
			got, out := s.FileSensitivity(ctx, "config/settings.yaml", harnessdecide.Facts{"d": 2, "z": 3})
			return string(got), out
		}},
		{harnessdecide.KindSubgoals, func(s *harnessdecide.Service, secret string) (string, harnessdecide.Outcome) {
			got, out := s.Subgoals(ctx, []harnessdecide.Item{{ID: secret + "-sg-1", Note: "tidy the parser"}, {ID: secret + "-sg-2", Note: "add tests"}}, harnessdecide.Item{ID: "new", Note: "clean up the parser"})
			return got, out
		}},
	}
}
