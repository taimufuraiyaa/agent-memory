package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

func TestSolutionContinuationHTTPContract(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })

	started := postJSON(t, server.URL+"/api/v1/solutions/start", map[string]any{
		"workspace": "ws", "session_id": "session-1", "principal_id": "agent-1", "client_id": "codex",
		"goal_summary": "Continue the upgrade safely.", "capture_policy": "structured", "retention_class": "standard", "idempotency_key": "start-1",
	})
	episode := started["episode"].(map[string]any)
	episodeID := episode["id"].(string)
	if episode["status"] != "active" || started["deduplicated"] != false {
		t.Fatalf("unexpected start response: %#v", started)
	}

	step := postJSON(t, server.URL+"/api/v1/solutions/steps", map[string]any{
		"workspace": "ws", "principal_id": "agent-1", "episode_id": episodeID, "kind": "action", "status": "completed",
		"summary": "Added continuation contracts.", "source": "agent", "confidence": 0.9, "sensitivity": "internal", "idempotency_key": "step-1",
	})
	if step["step"].(map[string]any)["ordinal"] != float64(1) {
		t.Fatalf("unexpected step response: %#v", step)
	}

	checkpoint := postJSON(t, server.URL+"/api/v1/solutions/checkpoint", map[string]any{
		"workspace": "ws", "principal_id": "agent-1", "episode_id": episodeID, "expected_generation": 0,
		"goal_summary": "Continue the upgrade safely.", "constraints": []string{"Preserve compatibility"},
		"next_action": "Run focused tests.", "sensitivity": "internal", "ttl_seconds": 3600,
	})
	if checkpoint["working_state"].(map[string]any)["generation"] != float64(1) {
		t.Fatalf("unexpected checkpoint response: %#v", checkpoint)
	}

	state := getJSON(t, server.URL+"/api/v1/solutions/state?workspace=ws&principal_id=agent-1&episode_id="+episodeID)
	if state["working_state"].(map[string]any)["next_action"] != "Run focused tests." {
		t.Fatalf("unexpected state response: %#v", state)
	}

	handoff := postJSON(t, server.URL+"/api/v1/solutions/handoff", map[string]any{
		"workspace": "ws", "principal_id": "agent-1", "episode_id": episodeID, "expected_version": 2,
		"target_principal_id": "agent-2", "target_session_id": "session-2", "idempotency_key": "handoff-1",
	})
	if handoff["episode"].(map[string]any)["principal_id"] != "agent-2" {
		t.Fatalf("unexpected handoff response: %#v", handoff)
	}

	ended := postJSON(t, server.URL+"/api/v1/solutions/transition", map[string]any{
		"workspace": "ws", "principal_id": "agent-2", "episode_id": episodeID, "expected_version": 3,
		"status": "completed", "idempotency_key": "end-1",
	})
	if ended["episode"].(map[string]any)["status"] != "completed" {
		t.Fatalf("unexpected transition response: %#v", ended)
	}
}

func TestSolutionStateRejectsPriorPrincipalAfterHandoff(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })
	started := postJSON(t, server.URL+"/api/v1/solutions/start", map[string]any{
		"session_id": "s1", "principal_id": "p1", "client_id": "codex", "goal_summary": "Resume safely.",
		"capture_policy": "structured", "retention_class": "standard", "idempotency_key": "s1",
	})
	id := started["episode"].(map[string]any)["id"].(string)
	postJSON(t, server.URL+"/api/v1/solutions/checkpoint", map[string]any{
		"principal_id": "p1", "episode_id": id, "goal_summary": "Resume safely.", "sensitivity": "internal",
	})
	postJSON(t, server.URL+"/api/v1/solutions/handoff", map[string]any{
		"principal_id": "p1", "episode_id": id, "expected_version": 1, "target_principal_id": "p2", "target_session_id": "s2", "idempotency_key": "h1",
	})
	response, err := http.Get(server.URL + "/api/v1/solutions/state?principal_id=p1&episode_id=" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		var body any
		_ = json.NewDecoder(response.Body).Decode(&body)
		t.Fatalf("expected forbidden, got %d: %#v", response.StatusCode, body)
	}
}

func TestStructuredSessionEndFinalizesEpisodeThroughHTTPWithoutTranscript(t *testing.T) {
	provider, err := embeddings.NewLocalProvider(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir(), EmbeddingProvider: provider}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })

	started := postJSON(t, server.URL+"/api/v1/solutions/start", map[string]any{
		"session_id": "session-end-http", "principal_id": "agent-http", "client_id": "codex",
		"goal_summary": "Finalize the structured HTTP episode.", "capture_policy": "structured",
		"retention_class": "standard", "idempotency_key": "http-start",
	})
	episodeID := started["episode"].(map[string]any)["id"].(string)
	postJSON(t, server.URL+"/api/v1/solutions/steps", map[string]any{
		"principal_id": "agent-http", "episode_id": episodeID, "kind": "result", "status": "completed",
		"summary": "The HTTP integration passed.", "source": "agent", "confidence": .9,
		"sensitivity": "internal", "idempotency_key": "http-step",
	})

	ended := postJSON(t, server.URL+"/api/v1/memories/session-end", map[string]any{
		"session_id": "session-end-http", "principal_id": "agent-http", "terminal_status": "completed",
		"idempotency_key": "http-finish",
	})
	if ended["mode"] != "structured_episode" || ended["partial"] != false || ended["total_extracted"] != float64(0) {
		t.Fatalf("unexpected structured session-end response: %#v", ended)
	}
	if ended["episode"].(map[string]any)["status"] != "completed" || ended["summary"].(map[string]any)["episode_id"] != episodeID {
		t.Fatalf("structured episode was not finalized: %#v", ended)
	}
	recalled := postJSON(t, server.URL+"/api/v1/solutions/recall", map[string]any{
		"workspace": "ws", "task": "How was the structured HTTP episode finalized?", "token_budget": 800,
	})
	if len(recalled["paths"].([]any)) != 1 || recalled["request_id"] == "" {
		t.Fatalf("finalized solution path was not publicly recallable: %#v", recalled)
	}
	naturalRecall := postJSON(t, server.URL+"/api/v1/memories/recall", map[string]any{
		"workspace": "ws", "task": "How was the structured HTTP episode finalized?", "budget": 800,
	})
	if naturalRecall["how_request_id"] == "" || naturalRecall["how_recall"] == nil {
		t.Fatalf("normal recall did not naturally compose How context: %#v", naturalRecall)
	}
}

func TestSolutionActivityReviewHTTPContractAndWorkspaceIsolation(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })
	started := postJSON(t, server.URL+"/api/v1/solutions/start", map[string]any{
		"workspace": "ws", "session_id": "activity-session", "principal_id": "reviewer", "client_id": "codex",
		"goal_summary": "Inspect this safe episode.", "capture_policy": "structured", "retention_class": "standard", "idempotency_key": "activity-start",
	})
	episodeID := started["episode"].(map[string]any)["id"].(string)
	step := postJSON(t, server.URL+"/api/v1/solutions/steps", map[string]any{
		"workspace": "ws", "principal_id": "reviewer", "episode_id": episodeID, "kind": "decision", "status": "completed",
		"summary": "Use the inspected approach.", "source": "agent", "confidence": .8, "sensitivity": "internal", "idempotency_key": "activity-step",
	})
	stepID := step["step"].(map[string]any)["id"].(string)

	list := getJSON(t, server.URL+"/api/v1/solutions/activity?workspace=ws&limit=20")
	if len(list["episodes"].([]any)) != 1 {
		t.Fatalf("episode missing from activity: %#v", list)
	}
	detail := getJSON(t, server.URL+"/api/v1/solutions/activity?workspace=ws&episode_id="+episodeID)
	if len(detail["detail"].(map[string]any)["steps"].([]any)) != 1 {
		t.Fatalf("safe path missing from detail: %#v", detail)
	}
	postJSON(t, server.URL+"/api/v1/solutions/review", map[string]any{"workspace": "ws", "principal_id": "reviewer", "episode_id": episodeID, "action": "pin", "pinned": true})
	postJSON(t, server.URL+"/api/v1/solutions/review", map[string]any{"workspace": "ws", "principal_id": "reviewer", "episode_id": episodeID, "step_id": stepID, "action": "misleading", "reason": "This requires correction."})
	postJSON(t, server.URL+"/api/v1/solutions/review", map[string]any{"workspace": "ws", "principal_id": "reviewer", "episode_id": episodeID, "step_id": stepID, "action": "redact", "reason_class": "incorrect"})
	detail = getJSON(t, server.URL+"/api/v1/solutions/activity?workspace=ws&episode_id="+episodeID)
	if detail["detail"].(map[string]any)["pinned"] != true || detail["detail"].(map[string]any)["steps"].([]any)[0].(map[string]any)["summary"] != "[REDACTED: incorrect]" {
		t.Fatalf("review state missing: %#v", detail)
	}

	response, err := http.Get(server.URL + "/api/v1/solutions/activity?workspace=other&episode_id=" + episodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		t.Fatalf("expected cross-workspace denial, got %d", response.StatusCode)
	}
}
