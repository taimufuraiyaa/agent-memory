package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
)

func TestHarnessArtifactsAndEventsAreReadableOnlyByTheRunsOwner(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 100 * time.Millisecond, Script: []harnesstest.Step{{Text: "the bounded result"}}}})
	owner, _ := e.grant("claude-desktop")
	intruder, _ := e.grant("codex-cli")
	id := runID(e.start(owner, "key-00000001", "goal"))
	done := e.waitRun(owner, id, "completed")
	artifactID := done["artifacts"].([]any)[0].(map[string]any)["id"].(string)

	got := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/artifacts/"+artifactID+"?workspace=agent-memory", owner, nil)
	artifact, _ := got.data["artifact"].(map[string]any)
	if got.status != http.StatusOK || artifact["text"] != "the bounded result" || artifact["kind"] != "result" || artifact["sha256"] == "" {
		t.Fatalf("artifact = %d %s", got.status, got.body)
	}
	missing := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/artifacts/a999?workspace=agent-memory", owner, nil)
	foreign := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/artifacts/"+artifactID+"?workspace=agent-memory", intruder, nil)
	if missing.status != http.StatusNotFound || foreign.status != http.StatusNotFound || string(missing.body) != string(foreign.body) {
		t.Fatalf("existence leaks: %d %s vs %d %s", missing.status, missing.body, foreign.status, foreign.body)
	}
	for _, bad := range []string{"b1", "a", "a1x", "a1234567890", "..%2f"} {
		if r := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/artifacts/"+bad+"?workspace=agent-memory", owner, nil); r.status != http.StatusNotFound {
			t.Errorf("artifact id %q = %d", bad, r.status)
		}
	}

	// Events page with a cursor and a limit, and the cursor belongs to the client that got it.
	first := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/events?workspace=agent-memory&limit=2", owner, nil)
	events, _ := first.data["events"].([]any)
	next, _ := first.data["next"].(string)
	if first.status != http.StatusOK || len(events) != 2 || next == "" {
		t.Fatalf("first page = %d %s", first.status, first.body)
	}
	second := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/events?workspace=agent-memory&cursor="+next, owner, nil)
	more, _ := second.data["events"].([]any)
	if second.status != http.StatusOK || len(more) == 0 || more[0].(map[string]any)["seq"] == events[0].(map[string]any)["seq"] {
		t.Fatalf("second page = %d %s", second.status, second.body)
	}
	if r := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/events?workspace=agent-memory&cursor="+next, intruder, nil); r.status != http.StatusNotFound {
		t.Errorf("a foreign client read the events: %d", r.status)
	}
	for name, query := range map[string]string{"a garbage cursor": "&cursor=garbage", "a zero limit": "&limit=0", "a huge limit": "&limit=101", "a word limit": "&limit=many"} {
		if r := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/events?workspace=agent-memory"+query, owner, nil); r.status != http.StatusBadRequest {
			t.Errorf("%s = %d", name, r.status)
		}
	}
	if strings.Contains(string(first.body), "goal") || strings.Contains(string(first.body), "the bounded result") {
		t.Fatalf("events carried content: %s", first.body)
	}
}

func TestHarnessContinueRejectsWhatIsNotAClarificationWait(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{model: harnesstest.Behavior{Delay: 50 * time.Millisecond, Script: []harnesstest.Step{{Text: "done"}}}})
	token, _ := e.grant("claude-desktop")
	id := runID(e.start(token, "key-00000001", "goal"))
	e.waitRun(token, id, "completed")
	body := map[string]any{"workspace": "agent-memory", "input": "an answer", "idempotency_key": "continue-key-1", "expected_generation": 1}
	if r := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/continue", token, body); r.status != http.StatusConflict {
		t.Fatalf("continuing a finished run = %d %s", r.status, r.body)
	}
	for name, mutate := range map[string]map[string]any{
		"no input":     {"workspace": "agent-memory", "input": "", "idempotency_key": "continue-key-2", "expected_generation": 1},
		"no key":       {"workspace": "agent-memory", "input": "x", "expected_generation": 1},
		"an extra":     {"workspace": "agent-memory", "input": "x", "idempotency_key": "continue-key-3", "expected_generation": 1, "approve": true},
		"no workspace": {"input": "x", "idempotency_key": "continue-key-4", "expected_generation": 1},
	} {
		if r := e.do(http.MethodPost, "/api/v1/harness/runs/"+id+"/continue", token, mutate); r.status < 400 || r.status == http.StatusInternalServerError {
			t.Errorf("%s = %d %s", name, r.status, r.body)
		}
	}
	if r := e.do(http.MethodGet, "/api/v1/harness/runs/"+id+"/continue", token, nil); r.status != http.StatusMethodNotAllowed {
		t.Errorf("GET continue = %d", r.status)
	}
}

func TestHarnessReadinessReportsWhatIsComposedWithoutCallingAnyProvider(t *testing.T) {
	e := newHarnessEnv(t, harnessOpts{})
	e.gateway.Tools, e.gateway.Approvals = []string{"read_file", "edit_file"}, true
	e.gateway.Decisions = func() map[string]any { return map[string]any{"available": true} }
	token, _ := e.grant("claude-desktop")
	r := e.do(http.MethodGet, "/api/v1/harness/readiness?workspace=agent-memory", token, nil)
	if r.status != http.StatusOK || r.data["ready"] != true || r.data["hosted"] != false || r.data["approvals"] != true || r.data["fake"] != true {
		t.Fatalf("readiness = %d %s", r.status, r.body)
	}
	if tools, _ := r.data["tools"].([]any); len(tools) != 2 {
		t.Fatalf("tools = %v", r.data["tools"])
	}
	if d, _ := r.data["decisions"].(map[string]any); d["available"] != true {
		t.Fatalf("decisions = %v", r.data["decisions"])
	}
	if bare := e.do(http.MethodGet, "/api/v1/harness/readiness?workspace=agent-memory", "", nil); bare.status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated readiness = %d", bare.status)
	}
	if post := e.do(http.MethodPost, "/api/v1/harness/readiness", token, map[string]any{"workspace": "agent-memory"}); post.status != http.StatusMethodNotAllowed {
		t.Fatalf("POST readiness = %d", post.status)
	}
}
