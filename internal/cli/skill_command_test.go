package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkillLifecycleCommandsAreRegistered(t *testing.T) {
	root := NewRootCommand()
	skill, _, err := root.Find([]string{"skill"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list", "inspect", "propose", "evaluate", "approve", "canary", "promote", "resolve", "acknowledge", "complete", "disable", "pin", "rollback"} {
		child, _, findErr := skill.Find([]string{name})
		if findErr != nil || child.Name() != name {
			t.Fatalf("missing skill %s command", name)
		}
	}
	orchestration, _, err := skill.Find([]string{"orchestration"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"status", "pause", "resume", "cancel", "reconcile", "retry", "replay", "drain"} {
		child, _, findErr := orchestration.Find([]string{name})
		if findErr != nil || child.Name() != name {
			t.Fatalf("missing skill orchestration %s command", name)
		}
	}
}

func TestSkillOrchestrationCLIMapsStatusAndPauseToStableHTTPContracts(t *testing.T) {
	requests := make(chan *http.Request, 2)
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())
		if request.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			bodies <- body
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true,"version":"v1","data":{"accepted":true}}`)
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"skill", "--api", server.URL, "--workspace", "ws", "orchestration", "status", "--actor", "operator", "--workflow-id", "workflow-1"},
		{"skill", "--api", server.URL, "--workspace", "ws", "orchestration", "pause", "--actor", "operator", "--workflow-id", "workflow-1", "--expected-generation", "7"},
	} {
		command := NewRootCommand()
		command.SetArgs(args)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(io.Discard)
		if err := command.Execute(); err != nil {
			t.Fatalf("execute %s: %v", strings.Join(args, " "), err)
		}
	}
	statusRequest, pauseRequest := <-requests, <-requests
	if statusRequest.Method != http.MethodGet || statusRequest.URL.Path != "/api/v1/skills/orchestration/status" || statusRequest.URL.Query().Get("actor") != "operator" {
		t.Fatalf("status request=%s %s", statusRequest.Method, statusRequest.URL.String())
	}
	if pauseRequest.Method != http.MethodPost || pauseRequest.URL.Path != "/api/v1/skills/orchestration/control" {
		t.Fatalf("pause request=%s %s", pauseRequest.Method, pauseRequest.URL.String())
	}
	body := <-bodies
	if body["action"] != "pause" || body["expected_generation"] != float64(7) || body["workflow_id"] != "workflow-1" {
		t.Fatalf("pause body=%#v", body)
	}
}
