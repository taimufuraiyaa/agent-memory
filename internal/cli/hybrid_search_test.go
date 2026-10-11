package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchCLIForwardsHybridGraphControls(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"data":{"results":[]}}`))
	}))
	defer server.Close()
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"search", "--workspace", "ws", "--api", server.URL, "--query", "How does pricing depend on runtime?", "--graph-mode", "local_graph", "--graph-required", "--graph-allow-stale"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if payload["graph_mode"] != "local_graph" || payload["graph_required"] != true || payload["graph_allow_stale"] != true {
		t.Fatalf("graph controls lost in transport: %+v", payload)
	}
}
