package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdvisorCommandJSONEnvelope(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"advisor",
		"--workspace", "client",
		"--db", filepath.Join(t.TempDir(), "client.db"),
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute advisor json: %v", err)
	}

	var payload struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Workspace string `json:"workspace"`
			Grade     string `json:"grade"`
			Neutral   bool   `json:"neutral"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid advisor json: %v raw=%q", err, out.String())
	}
	if !payload.OK || payload.Command != "advisor" || payload.Data.Workspace != "client" || payload.Data.Grade != "N/A" || !payload.Data.Neutral {
		t.Fatalf("unexpected advisor envelope: %+v", payload)
	}
}

func TestAdvisorCommandDefaultsToText(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"advisor",
		"--workspace", "client",
		"--db", filepath.Join(t.TempDir(), "client.db"),
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute advisor text: %v", err)
	}
	for _, want := range []string{"Memory Advisor", "workspace: client", "grade: N/A", "recommendations:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected %q in advisor output, got %q", want, out.String())
		}
	}
}

func TestAdvisorCommandUsesAPITransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/advisor" || r.URL.Query().Get("workspace") != "remote" {
			t.Errorf("unexpected advisor request: %s", r.URL.String())
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"data":{"workspace":"remote","score":91,"grade":"A","neutral":false,"dimensions":[],"recommendations":[],"evidence":{}}}`)
	}))
	defer server.Close()

	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"advisor", "--workspace", "remote", "--api", server.URL, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute remote advisor: %v", err)
	}
	if !strings.Contains(out.String(), `"workspace":"remote"`) || !strings.Contains(out.String(), `"grade":"A"`) {
		t.Fatalf("unexpected remote advisor output: %s", out.String())
	}
}
