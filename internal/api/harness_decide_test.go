package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// firstChoice is a decision session that always picks the first candidate and records what it
// was asked, so a test can see exactly what would leave the machine.
type firstChoice struct {
	mu        sync.Mutex
	questions []harness.DecisionQuestion
}

func (f *firstChoice) Envelope(capability harness.CapabilityID, maxBytes int) (harness.Envelope, error) {
	return harness.Envelope{Version: harness.ContractVersion, Provider: "fake-decision", Scope: harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1},
		AccessRevision: 1, Capability: capability, MaxBytes: maxBytes}, nil
}

func (f *firstChoice) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	f.mu.Lock()
	f.questions = append(f.questions, q)
	f.mu.Unlock()
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Selected: []string{q.Candidates[0]}, Confidence: 0.9}, nil
}

func (f *firstChoice) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.questions) }

func decideEnv(t *testing.T, kinds ...harnessdecide.Kind) (*harnessEnv, *firstChoice) {
	t.Helper()
	e := newHarnessEnv(t, harnessOpts{})
	asker := &firstChoice{}
	hub, err := harnessdecide.NewHub(harnessdecide.Config{Asker: asker, Capability: "decision", Enabled: kinds, RunBudget: 3, PerMinute: 100}, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.gateway.Decide = hub.Service
	return e, asker
}

func TestHarnessDecideReturnsBoundedAdviceAndNothingThatIsPermission(t *testing.T) {
	e, asker := decideEnv(t, harnessdecide.KindModel, harnessdecide.KindCommandRisk, harnessdecide.KindTools)
	token, _ := e.grant("claude-desktop")
	post := func(body map[string]any) harnessResponse {
		body["workspace"] = "agent-memory"
		return e.do(http.MethodPost, "/api/v1/harness/decide", token, body)
	}
	r := post(map[string]any{"kind": "model", "items": []map[string]any{{"id": "fast"}, {"id": "deep"}}})
	if r.status != http.StatusOK || r.data["status"] != "applied" || r.data["advisory"] != true || r.data["selected"].([]any)[0] != "fast" {
		t.Fatalf("model = %d %s", r.status, r.body)
	}
	r = post(map[string]any{"kind": "command_risk", "capability": "command", "facts": map[string]int{"n": 2}})
	if r.status != http.StatusOK || r.data["status"] != "applied" || r.data["label"] != "routine" {
		t.Fatalf("command risk = %d %s", r.status, r.body)
	}
	if q := asker.questions[len(asker.questions)-1]; !strings.Contains(strings.Join(evidenceText(q), " "), "n=2") || !strings.Contains(strings.Join(evidenceText(q), " "), "command") {
		t.Fatalf("the command's class and facts were not sent: %+v", q.Evidence)
	}
	r = post(map[string]any{"kind": "tools", "keep": 1, "items": []map[string]any{{"id": "read_file"}, {"id": "edit_file"}, {"id": "git_commit"}}})
	if r.status != http.StatusOK || len(r.data["selected"].([]any)) != 1 {
		t.Fatalf("tools = %d %s", r.status, r.body)
	}
	// Nothing in the reply can be read as an allow, a deny or an approval.
	for _, forbidden := range []string{"allow", "approve", "grant", "permit"} {
		if strings.Contains(strings.ToLower(string(r.body)), `"`+forbidden) {
			t.Errorf("the reply carries %q: %s", forbidden, r.body)
		}
	}
	// A kind that was not enabled is reported, not asked.
	before := asker.count()
	r = post(map[string]any{"kind": "visibility", "items": []map[string]any{{"id": "a"}, {"id": "b"}}})
	if r.status != http.StatusOK || r.data["status"] != "disabled" || r.data["selected"] != nil || asker.count() != before {
		t.Fatalf("a disabled kind = %d %s (asked %d more)", r.status, r.body, asker.count()-before)
	}
}

func TestHarnessDecideRejectsWhatCouldCarryContentOrWidenEgress(t *testing.T) {
	e, asker := decideEnv(t, harnessdecide.KindModel, harnessdecide.KindFileSensitivity, harnessdecide.KindSubgoals)
	token, _ := e.grant("claude-desktop")
	post := func(body map[string]any) harnessResponse {
		body["workspace"] = "agent-memory"
		return e.do(http.MethodPost, "/api/v1/harness/decide", token, body)
	}
	for name, body := range map[string]map[string]any{
		"an internal kind":      {"kind": "file_sensitivity", "capability": "x"},
		"another internal kind": {"kind": "subgoal_dedup", "items": []map[string]any{{"id": "a"}}},
		"an unknown kind":       {"kind": "approve"},
		"a versioned name":      {"kind": "model.v1"},
		"a note field":          {"kind": "model", "items": []map[string]any{{"id": "a", "note": "/etc/passwd"}, {"id": "b"}}},
		"an unknown field":      {"kind": "model", "approve": true},
		"keep too large":        {"kind": "tools", "keep": 99},
		"negative keep":         {"kind": "tools", "keep": -1},
	} {
		if r := post(body); r.status != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, r.status, r.body)
		}
	}
	// Text in an identifier is the service's to refuse, and it never reaches the provider.
	if r := post(map[string]any{"kind": "model", "items": []map[string]any{{"id": "../../etc/passwd"}, {"id": "ok"}}}); r.status != http.StatusOK || r.data["status"] != "invalid" {
		t.Errorf("a path as an identifier = %d %s", r.status, r.body)
	}
	if r := post(map[string]any{"kind": "model", "items": []map[string]any{{"id": "a", "facts": map[string]int{"secret": 1}}, {"id": "b"}}}); r.data["status"] != "invalid" {
		t.Errorf("a fact outside the vocabulary = %s", r.body)
	}
	if asker.count() != 0 {
		t.Fatalf("a rejected question reached the provider %d times", asker.count())
	}
}

func TestHarnessDecideNeedsItsOwnGrantAndKeepsABudgetPerClient(t *testing.T) {
	e, asker := decideEnv(t, harnessdecide.KindModel)
	withoutDecide, _ := e.grant("codex-cli", harnessauth.OpCapabilities, harnessauth.OpStart)
	body := map[string]any{"workspace": "agent-memory", "kind": "model", "items": []map[string]any{{"id": "fast"}, {"id": "deep"}}}
	if r := e.do(http.MethodPost, "/api/v1/harness/decide", withoutDecide, body); r.status != http.StatusUnauthorized {
		t.Fatalf("a grant without decide = %d", r.status)
	}
	if r := e.do(http.MethodPost, "/api/v1/harness/decide", "", body); r.status != http.StatusUnauthorized {
		t.Fatalf("no token = %d", r.status)
	}
	if r := e.do(http.MethodGet, "/api/v1/harness/decide", withoutDecide, nil); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", r.status)
	}
	if asker.count() != 0 {
		t.Fatal("an unauthorized call reached the provider")
	}
	alice, _ := e.grant("claude-desktop", harnessauth.OpDecide)
	bob, _ := e.grant("codex-cli", harnessauth.OpDecide)
	for i := 0; i < 3; i++ {
		if r := e.do(http.MethodPost, "/api/v1/harness/decide", alice, body); r.data["status"] != "applied" {
			t.Fatalf("call %d = %s", i, r.body)
		}
	}
	if r := e.do(http.MethodPost, "/api/v1/harness/decide", alice, body); r.data["status"] != "exhausted" {
		t.Fatalf("a spent budget = %s", r.body)
	}
	if r := e.do(http.MethodPost, "/api/v1/harness/decide", bob, body); r.data["status"] != "applied" {
		t.Fatalf("another client shares the budget: %s", r.body)
	}
}

func TestHarnessDecideIsUnavailableWhenNoDecisionsAreComposed(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{})
	token, _ := e.grant("claude-desktop")
	r := e.do(http.MethodPost, "/api/v1/harness/decide", token, map[string]any{"workspace": "agent-memory", "kind": "model"})
	if r.status != http.StatusServiceUnavailable {
		t.Fatalf("= %d %s", r.status, r.body)
	}
}

func TestHarnessDecideSendsTheClassesAndFactsTheClientSupplied(t *testing.T) {
	e, asker := decideEnv(t, harnessdecide.KindVisibility, harnessdecide.KindCache)
	token, _ := e.grant("claude-desktop")
	r := e.do(http.MethodPost, "/api/v1/harness/decide", token, map[string]any{"workspace": "agent-memory", "kind": "visibility",
		"items": []map[string]any{{"id": "chunk-a", "class": "notes", "facts": map[string]int{"k": 3}}, {"id": "chunk-b", "class": "code", "facts": map[string]int{"k": 4}}}})
	if r.status != http.StatusOK || r.data["status"] != "applied" {
		t.Fatalf("visibility = %d %s", r.status, r.body)
	}
	q := asker.questions[len(asker.questions)-1]
	text := strings.Join(q.Candidates, " ") + " " + strings.Join(evidenceText(q), " ")
	for _, want := range []string{"notes", "code", "k=3", "k=4"} {
		if !strings.Contains(text, want) {
			t.Errorf("the question lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "chunk-a") || strings.Contains(text, "chunk-b") {
		t.Errorf("a caller identifier left unaliased: %s", text)
	}
	r = e.do(http.MethodPost, "/api/v1/harness/decide", token, map[string]any{"workspace": "agent-memory", "kind": "cache", "facts": map[string]int{"h": 40, "n": 9, "p": 2}})
	if r.status != http.StatusOK || r.data["status"] != "applied" || r.data["label"] != "stable" {
		t.Fatalf("cache = %d %s", r.status, r.body)
	}
	if r = e.do(http.MethodPost, "/api/v1/harness/decide", token, map[string]any{"workspace": "agent-memory", "kind": "cache"}); r.data["status"] != "not_needed" {
		t.Fatalf("cache without measurements = %s", r.body)
	}
}

func evidenceText(q harness.DecisionQuestion) []string {
	out := []string{}
	for _, ref := range q.Evidence {
		out = append(out, ref.ID, ref.Revision, ref.Class, ref.Note)
	}
	return out
}
