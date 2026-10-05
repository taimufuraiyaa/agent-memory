package api

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

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

var bg = context.Background()

type harnessAllowAll struct{}

func (harnessAllowAll) Decide(context.Context, harnessrun.Owner, harness.PreparedAction) harnessrun.Decision {
	return harnessrun.DecisionAllow
}

type harnessDirs struct{ roots map[string]string }

func (d harnessDirs) Root(name string) (string, error) {
	if root, ok := d.roots[name]; ok {
		return root, nil
	}
	return "", io.EOF
}

type harnessClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *harnessClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *harnessClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harnessEnv struct {
	t       *testing.T
	dir     string
	auth    *harnessauth.Authority
	runs    *harnessrun.Manager
	server  *httptest.Server
	clients *clientprofile.Store
	clock   *harnessClock
	model   *harnesstest.Counters
}

type harnessOpts struct {
	model harnesstest.Behavior
	tool  harnesstest.Behavior
	edit  func(*harnessrun.Config)
	noGW  bool
}

func newHarnessEnv(t *testing.T, o harnessOpts) *harnessEnv {
	t.Helper()
	dir := t.TempDir()
	clk := &harnessClock{t: time.Now().UTC()}
	clients, err := clientprofile.Open(dir, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude-desktop", "codex-cli"} {
		if _, err := clients.Create(clientprofile.Input{ID: id, DisplayName: id, ClientKind: clientprofile.KindOther, ToolProfile: clientprofile.ProfileDefault}); err != nil {
			t.Fatal(err)
		}
	}
	dirs := harnessDirs{roots: map[string]string{"agent-memory": t.TempDir(), "other-project": t.TempDir()}}
	auth, err := harnessauth.New(dir, harnessauth.Options{Clients: clients, Workspaces: dirs, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	registry := harness.NewRegistry()
	model, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), o.model)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-tool", harness.KindTool, "read"), o.tool); err != nil {
		t.Fatal(err)
	}
	cfg := harnessrun.Config{DataDir: dir, Registry: registry, Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"},
		Tool:            &harnessrun.Binding{Provider: "fake-tool", Capability: "read"},
		StillAuthorized: func(owner harnessrun.Owner) bool { return auth.Active(owner.GrantID, owner.Workspace) }}
	if o.edit != nil {
		o.edit(&cfg)
	}
	runs, err := harnessrun.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runs.Close() })
	svc := &Service{}
	if !o.noGW {
		svc.Harness = &HarnessGateway{Authority: auth, Runs: runs, Providers: registry.Manifests(), Fake: true}
	}
	server := httptest.NewServer(LocalRequestBoundary(NewMux(svc)))
	t.Cleanup(server.Close)
	return &harnessEnv{t: t, dir: dir, auth: auth, runs: runs, server: server, clients: clients, clock: clk, model: model}
}

func (e *harnessEnv) grant(client string, ops ...harnessauth.Operation) (string, harnessauth.GrantInfo) {
	e.t.Helper()
	if err := e.auth.SetEnabled(bg, true); err != nil {
		e.t.Fatal(err)
	}
	if len(ops) == 0 {
		ops = harnessauth.AllOperations()
	}
	token, info, err := e.auth.Mint(bg, harnessauth.MintRequest{ClientID: client, Workspaces: []string{"agent-memory"}, Operations: ops})
	if err != nil {
		e.t.Fatal(err)
	}
	return token, info
}

type harnessResponse struct {
	status  int
	header  http.Header
	body    []byte
	ok      bool
	errCode string
	errMsg  string
	data    map[string]any
}

func (e *harnessEnv) do(method, path, token string, body any, headers ...string) harnessResponse {
	e.t.Helper()
	var reader io.Reader
	switch typed := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(typed)
	case string:
		reader = strings.NewReader(typed)
	default:
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Add(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		e.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	out := harnessResponse{status: response.StatusCode, header: response.Header, body: raw}
	var envelope struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		out.ok, out.data = envelope.OK, envelope.Data
		if envelope.Error != nil {
			out.errCode, out.errMsg = envelope.Error.Code, envelope.Error.Message
		}
	}
	return out
}

func (e *harnessEnv) start(token, key, goal string) harnessResponse {
	return e.do(http.MethodPost, "/api/v1/harness/runs", token, map[string]any{"workspace": "agent-memory", "idempotency_key": key, "goal": goal})
}

func (e *harnessEnv) run(token, id string) map[string]any {
	e.t.Helper()
	response := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", token, nil)
	if response.status != http.StatusOK {
		e.t.Fatalf("status = %d %s", response.status, response.body)
	}
	return response.data["run"].(map[string]any)
}

func (e *harnessEnv) waitRun(token, id string, states ...string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		run := e.run(token, id)
		for _, state := range states {
			if run["state"] == state {
				return run
			}
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out; last run %v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runID(r harnessResponse) string { return r.data["run"].(map[string]any)["id"].(string) }

func TestHarnessRoutesAreAbsentWithoutAGateway(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{noGW: true})
	for _, path := range []string{"/api/v1/harness/capabilities?workspace=agent-memory", "/api/v1/harness/runs", "/api/v1/harness/"} {
		if response := e.do(http.MethodGet, path, "hcap1.x.y", nil); response.status != http.StatusNotFound {
			t.Errorf("%s = %d", path, response.status)
		}
	}
	// The existing API is unaffected by the new package.
	if response := e.do(http.MethodGet, "/api/v1/capabilities", "", nil); response.status != http.StatusOK {
		t.Fatalf("existing capabilities = %d", response.status)
	}
}

func TestHarnessEndToEndRunThroughHTTP(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 150 * time.Millisecond, CostMicros: 250, Script: []harnesstest.Step{{Text: "fake provider result"}}}})
	token, _ := e.grant("claude-desktop")

	caps := e.do(http.MethodGet, "/api/v1/harness/capabilities?workspace=agent-memory", token, nil)
	if caps.status != http.StatusOK || !caps.ok {
		t.Fatalf("capabilities = %d %s", caps.status, caps.body)
	}
	ops, _ := caps.data["operations"].([]any)
	providers, _ := caps.data["providers"].([]any)
	features, _ := caps.data["features"].(map[string]any)
	if len(ops) != 6 || len(providers) != 2 || features["approval_over_mcp"] != false || caps.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("capabilities = %s", caps.body)
	}
	for _, provider := range providers {
		if provider.(map[string]any)["fake"] != true {
			t.Fatalf("discovery overstates a test double as real: %s", caps.body)
		}
	}

	began := time.Now()
	started := e.start(token, "key-00000001", "private goal text")
	if started.status != http.StatusAccepted || time.Since(began) > 120*time.Millisecond {
		t.Fatalf("start = %d after %v: %s", started.status, time.Since(began), started.body)
	}
	id := runID(started)
	done := e.waitRun(token, id, "completed", "failed")
	if done["state"] != "completed" || done["usage"].(map[string]any)["spend_micros"] != float64(250) {
		t.Fatalf("done = %v", done)
	}
	artifacts := done["artifacts"].([]any)
	if len(artifacts) != 1 || artifacts[0].(map[string]any)["kind"] != "result" {
		t.Fatalf("artifacts = %v", artifacts)
	}
	for _, body := range [][]byte{caps.body, started.body} {
		if bytes.Contains(body, []byte("private goal text")) || bytes.Contains(body, []byte(token)) {
			t.Fatalf("a response echoed the goal or the token: %s", body)
		}
	}
	finalBody, _ := json.Marshal(done)
	if bytes.Contains(finalBody, []byte("fake provider result")) || bytes.Contains(finalBody, []byte("action_digest")) {
		t.Fatalf("status carried provider output or an approval digest: %s", finalBody)
	}

	again := e.start(token, "key-00000001", "private goal text")
	if again.status != http.StatusOK || runID(again) != id || again.data["run"].(map[string]any)["deduplicated"] != true {
		t.Fatalf("retry = %d %s", again.status, again.body)
	}
}

func TestHarnessEveryAuthenticationFailureIsTheSameDenial(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "ok"}}}})
	token, info := e.grant("claude-desktop", harnessauth.OpStart, harnessauth.OpStatus)
	statusOnly, _ := e.grant("codex-cli", harnessauth.OpStatus)
	startPath := "/api/v1/harness/runs"
	goodBody := map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000001", "goal": "g"}

	cases := map[string]harnessResponse{
		"no header":         e.do(http.MethodPost, startPath, "", goodBody),
		"wrong scheme":      e.do(http.MethodPost, startPath, "", goodBody, "Authorization", "Basic "+token),
		"two headers":       e.do(http.MethodPost, startPath, token, goodBody, "Authorization", "Bearer "+token),
		"token in query":    e.do(http.MethodPost, startPath+"?token="+token+"&access_token="+token, "", goodBody),
		"wrong token":       e.do(http.MethodPost, startPath, "hcap1."+strings.Repeat("0", 32)+"."+strings.Repeat("A", 43), goodBody),
		"garbage":           e.do(http.MethodPost, startPath, "garbage", goodBody),
		"oversized token":   e.do(http.MethodPost, startPath, strings.Repeat("a", 400), goodBody),
		"ungranted op":      e.do(http.MethodPost, startPath, statusOnly, goodBody),
		"other workspace":   e.do(http.MethodPost, startPath, token, map[string]any{"workspace": "other-project", "idempotency_key": "key-00000002", "goal": "g"}),
		"unknown workspace": e.do(http.MethodPost, startPath, token, map[string]any{"workspace": "nowhere", "idempotency_key": "key-00000003", "goal": "g"}),
		"client mismatch":   e.do(http.MethodPost, startPath, token, goodBody, harnessClientHeader, "codex-cli"),
		"status no token":   e.do(http.MethodGet, "/api/v1/harness/runs/run_"+strings.Repeat("a", 32)+"?workspace=agent-memory", "", nil),
		"caps ungranted":    e.do(http.MethodGet, "/api/v1/harness/capabilities?workspace=agent-memory", token, nil),
	}
	var baseline harnessResponse
	for name, response := range cases {
		if response.status != http.StatusUnauthorized || response.errCode != "unauthorized" || response.header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s = %d %s", name, response.status, response.body)
			continue
		}
		if baseline.body == nil {
			baseline = response
		}
		if !bytes.Equal(response.body, baseline.body) {
			t.Errorf("%s is distinguishable: %s vs %s", name, response.body, baseline.body)
		}
	}
	// Disabled, revoked and expired grants are the same denial too.
	good := e.start(token, "key-00000009", "g")
	if good.status != http.StatusAccepted {
		t.Fatalf("control request = %d %s", good.status, good.body)
	}
	if err := e.auth.SetEnabled(bg, false); err != nil {
		t.Fatal(err)
	}
	disabled := e.start(token, "key-00000010", "g")
	if err := e.auth.SetEnabled(bg, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Revoke(bg, info.ID); err != nil {
		t.Fatal(err)
	}
	revoked := e.start(token, "key-00000011", "g")
	fresh, _ := e.grant("claude-desktop", harnessauth.OpStart)
	e.clock.advance(harnessauth.DefaultTTL + time.Second)
	expired := e.start(fresh, "key-00000012", "g")
	for name, response := range map[string]harnessResponse{"disabled": disabled, "revoked": revoked, "expired": expired} {
		if response.status != http.StatusUnauthorized || !bytes.Equal(response.body, baseline.body) {
			t.Errorf("%s = %d %s", name, response.status, response.body)
		}
	}
}

func TestHarnessCrossClientRunsAreNotDisclosed(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 500 * time.Millisecond, Script: []harnesstest.Step{{Text: "ok"}}}})
	owner, _ := e.grant("claude-desktop")
	intruder, _ := e.grant("codex-cli")
	id := runID(e.start(owner, "key-00000001", "mine"))
	missing := "run_" + strings.Repeat("0", 32)

	statusPath := func(run string) string { return "/api/v1/harness/runs/" + run + "?workspace=agent-memory" }
	cancelBody := map[string]any{"workspace": "agent-memory", "idempotency_key": "key-intrusion", "expected_generation": 1}
	for name, pair := range map[string][2]harnessResponse{
		"status": {e.do(http.MethodGet, statusPath(id), intruder, nil), e.do(http.MethodGet, statusPath(missing), intruder, nil)},
		"cancel": {e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/cancel", intruder, cancelBody), e.do(http.MethodPost, "/api/v1/harness/runs/"+missing+"/cancel", intruder, cancelBody)},
	} {
		if pair[0].status != http.StatusNotFound || pair[1].status != http.StatusNotFound || !bytes.Equal(pair[0].body, pair[1].body) {
			t.Errorf("%s discloses existence: %d %s vs %d %s", name, pair[0].status, pair[0].body, pair[1].status, pair[1].body)
		}
	}
	if run := e.run(owner, id); run["state"] == "cancelled" || run["state"] == "cancelling" {
		t.Fatal("another client cancelled the run")
	}
	// The same key from two clients starts two independent runs.
	other := e.start(intruder, "key-00000001", "mine")
	if other.status != http.StatusAccepted || runID(other) == id {
		t.Fatalf("keys collided across clients: %d %s", other.status, other.body)
	}
}

func TestHarnessRequestValidationAndErrorMapping(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 5 * time.Second, Script: []harnesstest.Step{{Text: "ok"}}}})
	token, _ := e.grant("claude-desktop")
	post := func(path string, body any) harnessResponse { return e.do(http.MethodPost, path, token, body) }
	good := map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000001", "goal": "g"}

	for name, tc := range map[string]struct {
		response harnessResponse
		status   int
		code     string
	}{
		"malformed JSON":   {post("/api/v1/harness/runs", "{not json"), 400, "invalid_request"},
		"unknown field":    {post("/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000001", "goal": "g", "admin": true}), 400, "invalid_request"},
		"two objects":      {post("/api/v1/harness/runs", `{"workspace":"agent-memory","idempotency_key":"key-00000001","goal":"g"}{"x":1}`), 400, "invalid_request"},
		"oversized body":   {post("/api/v1/harness/runs", `{"workspace":"agent-memory","idempotency_key":"key-00000001","goal":"`+strings.Repeat("a", 70000)+`"}`), 413, "request_too_large"},
		"short key":        {post("/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "short", "goal": "g"}), 400, "invalid_request"},
		"empty goal":       {post("/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000002", "goal": ""}), 400, "invalid_request"},
		"budget ceiling":   {post("/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000003", "goal": "g", "budget": map[string]any{"max_turns": 9999}}), 422, "budget_out_of_range"},
		"negative seconds": {post("/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000004", "goal": "g", "budget": map[string]any{"max_active_seconds": -5}}), 422, "budget_out_of_range"},
		"bad run path":     {e.do(http.MethodGet, "/api/v1/harness/runs/not-a-run?workspace=agent-memory", token, nil), 404, "not_found"},
		"unknown route":    {e.do(http.MethodGet, "/api/v1/harness/admin", token, nil), 404, "not_found"},
		"deeper route":     {e.do(http.MethodPost, "/api/v1/harness/runs/run_"+strings.Repeat("a", 32)+"/approve", token, good), 404, "not_found"},
		"wrong method":     {e.do(http.MethodDelete, "/api/v1/harness/runs", token, nil), 405, "method_not_allowed"},
		"get on start":     {e.do(http.MethodGet, "/api/v1/harness/runs", token, nil), 405, "method_not_allowed"},
		"missing ws":       {e.do(http.MethodGet, "/api/v1/harness/capabilities", token, nil), 400, "invalid_request"},
		"bad ws":           {e.do(http.MethodGet, "/api/v1/harness/capabilities?workspace=../etc", token, nil), 400, "invalid_request"},
	} {
		if tc.response.status != tc.status || tc.response.errCode != tc.code {
			t.Errorf("%s = %d %s (%s)", name, tc.response.status, tc.response.errCode, tc.response.body)
		}
	}
	if response := e.do(http.MethodDelete, "/api/v1/harness/runs", token, nil); response.header.Get("Allow") != http.MethodPost {
		t.Errorf("Allow = %q", response.header.Get("Allow"))
	}

	id := runID(post("/api/v1/harness/runs", good))
	conflict := post("/api/v1/harness/runs", map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000001", "goal": "different"})
	if conflict.status != http.StatusConflict || conflict.errCode != "idempotency_conflict" {
		t.Errorf("conflict = %d %s", conflict.status, conflict.body)
	}
	running := e.waitRun(token, id, "running")
	stale := post("/api/v1/harness/runs/"+id+"/cancel", map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000005", "expected_generation": running["generation"].(float64) - 1})
	if stale.status != http.StatusConflict || stale.errCode != "stale_generation" {
		t.Errorf("stale = %d %s", stale.status, stale.body)
	}
	if response := post("/api/v1/harness/runs/"+id+"/cancel", map[string]any{"workspace": "agent-memory", "idempotency_key": "x", "expected_generation": 1}); response.status != 400 {
		t.Errorf("bad cancel key = %d", response.status)
	}
	for _, response := range []harnessResponse{conflict, stale} {
		if bytes.Contains(response.body, []byte("harnessrun")) || bytes.Contains(response.body, []byte(".go")) {
			t.Errorf("error leaked internals: %s", response.body)
		}
	}
}

func TestHarnessCancelRunningRunAndReplay(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 10 * time.Second, Script: []harnesstest.Step{{Text: "never"}}}})
	token, _ := e.grant("claude-desktop")
	id := runID(e.start(token, "key-00000001", "slow"))
	running := e.waitRun(token, id, "running")
	body := map[string]any{"workspace": "agent-memory", "idempotency_key": "canc-00000001", "expected_generation": running["generation"]}
	began := time.Now()
	cancelled := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/cancel", token, body)
	if cancelled.status != http.StatusOK {
		t.Fatalf("cancel = %d %s", cancelled.status, cancelled.body)
	}
	done := e.waitRun(token, id, "cancelled")
	if time.Since(began) > 2*time.Second || done["code"] != "cancelled" || e.model.Closes.Load() < 1 {
		t.Fatalf("cancellation slow or incomplete: %v closes=%d", time.Since(began), e.model.Closes.Load())
	}
	replay := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/cancel", token, body)
	if replay.status != http.StatusOK {
		t.Fatalf("replay = %d %s", replay.status, replay.body)
	}
	again := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/cancel", token, map[string]any{"workspace": "agent-memory", "idempotency_key": "canc-00000002", "expected_generation": done["generation"]})
	if again.status != http.StatusConflict || again.errCode != "invalid_state" {
		t.Fatalf("cancel of a finished run = %d %s", again.status, again.body)
	}
}

func TestHarnessRevokingOrDisablingStopsRunningWork(t *testing.T) {
	for name, stop := range map[string]func(*harnessEnv, harnessauth.GrantInfo){
		"revoke": func(e *harnessEnv, info harnessauth.GrantInfo) {
			if _, err := e.auth.Revoke(bg, info.ID); err != nil {
				e.t.Fatal(err)
			}
		},
		"disable": func(e *harnessEnv, _ harnessauth.GrantInfo) {
			if err := e.auth.SetEnabled(bg, false); err != nil {
				e.t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newHarnessEnv(t, harnessOpts{
				model: harnesstest.Behavior{Delay: 40 * time.Millisecond, Script: []harnesstest.Step{{ToolID: "read", Text: "tick"}}},
				edit:  func(c *harnessrun.Config) { c.Policy = harnessAllowAll{} },
			})
			token, info := e.grant("claude-desktop")
			started := e.do(http.MethodPost, "/api/v1/harness/runs", token, map[string]any{
				"workspace": "agent-memory", "idempotency_key": "key-00000001", "goal": "long running work",
				"budget": map[string]any{"max_turns": 60, "max_active_seconds": 60}})
			if started.status != http.StatusAccepted {
				t.Fatalf("start = %d %s", started.status, started.body)
			}
			id := runID(started)
			e.waitRun(token, id, "running")
			deadline := time.Now().Add(5 * time.Second)
			for e.run(token, id)["usage"].(map[string]any)["turns"].(float64) < 2 {
				if time.Now().After(deadline) {
					t.Fatal("the run never made progress")
				}
				time.Sleep(10 * time.Millisecond)
			}
			stop(e, info)
			// The credential no longer works, so observe through the runtime.
			who := harnessrun.Owner{ClientID: "claude-desktop", Workspace: "agent-memory", GrantID: info.ID, GrantRevision: 1}
			for {
				status, err := e.runs.Status(bg, who, id)
				if err != nil {
					t.Fatal(err)
				}
				if status.State == harnessrun.StateCancelled {
					if status.Usage.Turns >= 60 {
						t.Fatal("the run was not stopped early")
					}
					break
				}
				if status.State.Terminal() || time.Now().After(deadline) {
					t.Fatalf("status = %+v", status)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if response := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", token, nil); response.status != http.StatusUnauthorized {
				t.Fatalf("a revoked or disabled grant still reads status: %d", response.status)
			}
		})
	}
}

type harnessAskAll struct{}

func (harnessAskAll) Decide(context.Context, harnessrun.Owner, harness.PreparedAction) harnessrun.Decision {
	return harnessrun.DecisionAsk
}

func TestHarnessErrorMessagesAreFixedAndNeverCarryRuntimeDetail(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "ok"}}}})
	token, _ := e.grant("claude-desktop")
	id := runID(e.start(token, "key-00000001", "g"))
	e.waitRun(token, id, "completed")
	finished := e.run(token, id)

	post := func(path string, body any) harnessResponse { return e.do(http.MethodPost, path, token, body) }
	cancel := func(key string, generation any) harnessResponse {
		return post("/api/v1/harness/runs/"+id+"/cancel", map[string]any{"workspace": "agent-memory", "idempotency_key": key, "expected_generation": generation})
	}
	for name, tc := range map[string]struct {
		response harnessResponse
		code     string
		message  string
	}{
		"invalid request":  {e.start(token, "short", "g"), "invalid_request", "invalid request"},
		"empty goal":       {e.start(token, "key-00000002", ""), "invalid_request", "invalid request"},
		"invalid state":    {cancel("canc-00000001", finished["generation"]), "invalid_state", "the run state does not allow this operation"},
		"stale generation": {cancel("canc-00000002", 1), "stale_generation", "the run changed since you last read it"},
		"conflict":         {e.start(token, "key-00000001", "different goal"), "idempotency_conflict", "idempotency key was used for a different request"},
		"not found":        {e.do(http.MethodGet, "/api/v1/harness/runs/run_"+strings.Repeat("0", 32)+"?workspace=agent-memory", token, nil), "not_found", "run not found"},
		"budget":           {e.do(http.MethodPost, "/api/v1/harness/runs", token, map[string]any{"workspace": "agent-memory", "idempotency_key": "key-00000003", "goal": "g", "budget": map[string]any{"max_turns": 9999}}), "budget_out_of_range", "budget is outside the allowed range"},
		"unauthorized":     {e.do(http.MethodGet, "/api/v1/harness/capabilities?workspace=agent-memory", "", nil), "unauthorized", "access denied"},
		"unknown route":    {e.do(http.MethodGet, "/api/v1/harness/nope", token, nil), "not_found", "not found"},
		"method":           {e.do(http.MethodPut, "/api/v1/harness/runs", token, nil), "method_not_allowed", "method not allowed"},
		"missing ws":       {e.do(http.MethodGet, "/api/v1/harness/capabilities", token, nil), "invalid_request", "workspace is required"},
	} {
		if tc.response.errCode != tc.code || tc.response.errMsg != tc.message {
			t.Errorf("%s = %d code=%q message=%q (want %q / %q)", name, tc.response.status, tc.response.errCode, tc.response.errMsg, tc.code, tc.message)
		}
	}
}

func TestHarnessEachOperationIsScopedToItsOwnEndpoint(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 5 * time.Second, Script: []harnesstest.Step{{Text: "slow"}}}})
	full, _ := e.grant("claude-desktop")
	id := runID(e.start(full, "key-00000001", "target run"))
	e.waitRun(full, id, "running")

	endpoints := map[harnessauth.Operation]func(token string) harnessResponse{
		harnessauth.OpCapabilities: func(token string) harnessResponse {
			return e.do(http.MethodGet, "/api/v1/harness/capabilities?workspace=agent-memory", token, nil)
		},
		harnessauth.OpStart: func(token string) harnessResponse {
			return e.start(token, "key-scope-"+time.Now().Format("150405.000000"), "scoped")
		},
		harnessauth.OpStatus: func(token string) harnessResponse {
			return e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", token, nil)
		},
		harnessauth.OpCancel: func(token string) harnessResponse {
			return e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/cancel", token, map[string]any{"workspace": "agent-memory", "idempotency_key": "cancel-scope-001", "expected_generation": 999})
		},
	}
	endpoints[harnessauth.OpContinue] = func(token string) harnessResponse {
		return e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/continue", token, map[string]any{"workspace": "agent-memory", "input": "an answer", "idempotency_key": "continue-scope-1", "expected_generation": 999})
	}
	endpoints[harnessauth.OpArtifact] = func(token string) harnessResponse {
		return e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/artifacts/a1?workspace=agent-memory", token, nil)
	}
	for granted := range endpoints {
		token, _ := e.grant("claude-desktop", granted)
		for operation, call := range endpoints {
			response := call(token)
			if operation == granted && response.status == http.StatusUnauthorized {
				t.Errorf("a grant for %s was refused at its own endpoint", granted)
			}
			if operation != granted && response.status != http.StatusUnauthorized {
				t.Errorf("a grant for %s reached the %s endpoint: %d", granted, operation, response.status)
			}
		}
	}
	// The event log is read with the status grant and nothing else; approval can never be granted.
	statusToken, _ := e.grant("claude-desktop", harnessauth.OpStatus)
	if response := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/events?workspace=agent-memory", statusToken, nil); response.status != http.StatusOK {
		t.Errorf("events with a status grant = %d", response.status)
	}
	if response := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/events?workspace=agent-memory", full, nil); response.status == http.StatusUnauthorized {
		t.Error("events were refused to a full grant")
	}
	for _, path := range []string{"/artifact", "/approve"} {
		if response := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+path, full, map[string]any{"workspace": "agent-memory"}); response.status != http.StatusNotFound {
			t.Errorf("%s = %d", path, response.status)
		}
	}
}

func TestHarnessApprovalWaitsAreVisibleButNeverCarryTheActionDigest(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{
		model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Arguments: "{}"}}},
		edit:  func(c *harnessrun.Config) { c.Policy = harnessAskAll{} },
	})
	token, _ := e.grant("claude-desktop")
	id := runID(e.start(token, "key-00000001", "needs approval"))
	parked := e.waitRun(token, id, "needs_attention")
	attention := parked["attention"].(map[string]any)
	if attention["kind"] != "approval" || attention["prompt"] != nil {
		t.Fatalf("attention = %v", attention)
	}
	encoded, _ := json.Marshal(parked)
	if bytes.Contains(encoded, []byte("digest")) {
		t.Fatalf("the approval action digest reached the MCP-facing view: %s", encoded)
	}
	if parked["code"] != "approval_required" {
		t.Fatalf("code = %v", parked["code"])
	}
}

func TestHarnessStorageFaultsAreAnOpaqueInternalError(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Script: []harnesstest.Step{{Text: "ok"}}}})
	token, _ := e.grant("claude-desktop")
	id := runID(e.start(token, "key-00000001", "g"))
	e.waitRun(token, id, "completed")

	path := filepath.Join(e.dir, "harness", "runs", id+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"id":`), 0o600); err != nil {
		t.Fatal(err)
	}
	response := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"?workspace=agent-memory", token, nil)
	if response.status != http.StatusInternalServerError || response.errCode != "internal_error" || response.errMsg != "internal error" {
		t.Fatalf("storage fault = %d %s", response.status, response.body)
	}
	for _, leak := range []string{e.dir, ".json", "storage", "harness/runs", id} {
		if bytes.Contains(response.body, []byte(leak)) {
			t.Fatalf("an internal error leaked %q: %s", leak, response.body)
		}
	}
}

// A person approves at a terminal, through the approval store. There is no route on the
// gateway that approves, lists or reads an approval, whatever the credential; the client
// sees only that an approval exists and its opaque identifier.
func TestHarnessApprovalsAreNotReachableThroughTheGatewayAndShowOnlyAnIdentifier(t *testing.T) {
	var approvals *harnessapproval.Store
	e := newHarnessEnv(t, harnessOpts{
		model: harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Arguments: "{}"}}},
		tool:  harnesstest.Behavior{Digest: "sha256:" + strings.Repeat("c", 64), Paths: []string{"secret/path.txt"}, Preview: "+PREVIEW-CONTENT\n"},
		edit: func(c *harnessrun.Config) {
			c.Policy = harnessAskAll{}
			approvals = harnessapproval.Open(c.DataDir)
			c.Approvals = approvals
			c.ApprovalPoll = time.Hour
		},
	})
	token, _ := e.grant("claude-desktop")
	id := runID(e.start(token, "key-00000001", "needs approval"))
	parked := e.waitRun(token, id, "needs_attention")
	attention := parked["attention"].(map[string]any)
	approvalID, _ := attention["approval_id"].(string)
	if attention["kind"] != "approval" || !strings.HasPrefix(approvalID, "apr_") || len(approvalID) != 36 {
		t.Fatalf("attention = %v", attention)
	}
	encoded, _ := json.Marshal(parked)
	for _, leak := range []string{"digest", "PREVIEW-CONTENT", "secret/path.txt", "arguments", "code"} {
		if leak == "code" {
			continue // "code" is the run's own reason code field
		}
		if bytes.Contains(encoded, []byte(leak)) {
			t.Fatalf("the client view carries %q: %s", leak, encoded)
		}
	}
	record, err := approvals.Get(context.Background(), approvalID)
	if err != nil || record.State != harnessapproval.StatePending || !strings.Contains(record.Preview, "PREVIEW-CONTENT") {
		t.Fatalf("the approval store has %+v (%v)", record, err)
	}

	// Nothing on the gateway can approve, with any method, path or body, and none of these
	// attempts changes the approval.
	for _, path := range []string{
		"/api/v1/harness/approvals", "/api/v1/harness/approvals/" + approvalID, "/api/v1/harness/approvals/" + approvalID + "/approve",
		"/api/v1/harness/runs/" + id + "/approve", "/api/v1/harness/runs/" + id + "/approvals", "/api/v1/harness/runs/" + id + "/approvals/" + approvalID,
		"/api/v1/harness/runs/" + id + "/approve?workspace=agent-memory", "/api/v1/harness/runs/" + id + "/decision",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			response := e.do(method, path, token, map[string]any{"workspace": "agent-memory", "approve": true, "approved": true, "code": harnessapproval.Code(record), "approval_id": approvalID})
			if response.status != http.StatusNotFound && response.status != http.StatusMethodNotAllowed {
				t.Errorf("%s %s answered %d: %s", method, path, response.status, response.body)
			}
		}
	}
	// Continue answers clarifications only: whatever it is sent, it cannot touch an approval.
	for _, body := range []map[string]any{{"workspace": "agent-memory", "approve": true, "code": harnessapproval.Code(record)}, {"workspace": "agent-memory", "input": harnessapproval.Code(record), "expected_generation": 1, "idempotency_key": "continue-attempt-1"}} {
		if response := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/continue", token, body); response.status == http.StatusOK {
			t.Errorf("continue answered an approval wait: %s", response.body)
		}
	}
	if got, _ := approvals.Get(context.Background(), approvalID); got.State != harnessapproval.StatePending {
		t.Fatalf("a gateway request changed the approval: %+v", got)
	}
	if status := e.run(token, id); status["state"] != "needs_attention" {
		t.Fatalf("the run moved: %v", status)
	}
}
