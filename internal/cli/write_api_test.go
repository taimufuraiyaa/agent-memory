package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The multi-workspace daemon routes by the request's workspace, so the CLI must send it
// in API mode. A body without it made every `write --api` fail with "workspace is required".
func TestWriteViaAPISendsTheWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_MEMORY_ENABLED", "1")
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		bodies <- body
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true,"version":"v1","data":{"id":"m1"}}`)
	}))
	defer server.Close()

	command := NewRootCommand()
	command.SetArgs([]string{"write", "--api", server.URL, "--workspace", "ws", "--type", "semantic", "--content", "api mode keeps the workspace", "--keyword", "api"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatalf("write --api: %v", err)
	}
	body := <-bodies
	if body["workspace"] != "ws" {
		t.Fatalf("workspace missing from the API body: %+v", body)
	}
	if body["content"] != "api mode keeps the workspace" || body["type"] != "semantic" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

// session-end's handler reads the workspace from the request's query, not its body.
func TestSessionEndViaAPISendsTheWorkspaceInTheQuery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_MEMORY_ENABLED", "1")
	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		paths <- request.URL.Path + "?" + request.URL.RawQuery
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true,"version":"v1","data":{"stored":true}}`)
	}))
	defer server.Close()

	command := NewRootCommand()
	command.SetArgs([]string{"session-end", "--api", server.URL, "--workspace", "ws", "--transcript", "summary"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatalf("session-end --api: %v", err)
	}
	if got, want := <-paths, "/api/v1/memories/session-end?workspace=ws"; got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
}

// capturedRequest is what the stub daemon saw.
type capturedRequest struct {
	path string
	body map[string]any
}

// stubDaemon answers every request with a generic envelope and records it.
func stubDaemon(t *testing.T) (string, <-chan capturedRequest) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_MEMORY_ENABLED", "1")
	seen := make(chan capturedRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		seen <- capturedRequest{path: request.URL.RequestURI(), body: body}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"ok":true,"version":"v1","data":{}}`)
	}))
	t.Cleanup(server.Close)
	return server.URL, seen
}

func runCLI(t *testing.T, args ...string) {
	t.Helper()
	command := NewRootCommand()
	command.SetArgs(args)
	command.SetOut(&bytes.Buffer{})
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

// The write handler reads one "outcome" object; the flat outcome_result/outcome_approach
// keys the CLI used to send were ignored, so --outcome-result never reached the daemon.
func TestWriteViaAPISendsOutcomeAsOneObject(t *testing.T) {
	api, seen := stubDaemon(t)
	runCLI(t, "write", "--api", api, "--workspace", "ws", "--type", "outcome", "--content", "tried it",
		"--outcome-result", "SUCCESS", "--outcome-approach", "go run .")
	got := <-seen
	outcome, _ := got.body["outcome"].(map[string]any)
	if outcome["result"] != "success" || outcome["approach"] != "go run ." {
		t.Fatalf("outcome object = %+v (body %+v)", got.body["outcome"], got.body)
	}
	for _, flat := range []string{"outcome_result", "outcome_approach"} {
		if _, ok := got.body[flat]; ok {
			t.Fatalf("flat key %q must not be sent: %+v", flat, got.body)
		}
	}
}

func TestWriteViaAPIIgnoresOutcomeFlagsForOtherTypes(t *testing.T) {
	api, seen := stubDaemon(t)
	runCLI(t, "write", "--api", api, "--workspace", "ws", "--type", "semantic", "--content", "a fact",
		"--outcome-result", "success")
	if got := <-seen; got.body["outcome"] != nil {
		t.Fatalf("a semantic memory must not carry an outcome: %+v", got.body)
	}
}

func TestReconstructViaAPISendsTheWorkspace(t *testing.T) {
	api, seen := stubDaemon(t)
	runCLI(t, "reconstruct", "--api", api, "--workspace", "ws", "--query", "what did we decide")
	if got := <-seen; got.body["workspace"] != "ws" || got.body["query"] != "what did we decide" {
		t.Fatalf("reconstruct body = %+v", got.body)
	}
}

// import's handler takes the target workspace from the query, like session-end.
func TestImportViaAPISendsTheWorkspaceInTheQuery(t *testing.T) {
	api, seen := stubDaemon(t)
	bundle := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(bundle, []byte(`{"version":"v1","workspace":"other","memories":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runCLI(t, "import", "--api", api, "--workspace", "ws", "--in", bundle)
	if got, want := (<-seen).path, "/api/v1/memories/import?workspace=ws"; got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
}
