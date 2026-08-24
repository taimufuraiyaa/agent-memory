package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
