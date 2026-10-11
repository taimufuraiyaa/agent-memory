package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
)

func harnessFixture(t *testing.T) (dataDir, root string, run func(args ...string) (map[string]any, error)) {
	t.Helper()
	dataDir, root = t.TempDir(), t.TempDir()
	registry := map[string]any{"projects": []any{map[string]any{"name": "ws", "workspace_root": root, "db_path": filepath.Join(dataDir, "ws.db")}}}
	encoded, _ := json.Marshal(registry)
	if err := os.WriteFile(filepath.Join(dataDir, "workspaces.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	clients, err := clientprofile.Open(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clients.Create(clientprofile.Input{ID: "claude-desktop", DisplayName: "Claude", ClientKind: clientprofile.KindClaude, ToolProfile: clientprofile.ProfileDefault}); err != nil {
		t.Fatal(err)
	}
	run = func(args ...string) (map[string]any, error) {
		cmd := NewRootCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append([]string{"harness"}, append(args, "--data-dir", dataDir)...))
		err := cmd.Execute()
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(out.Bytes(), &envelope)
		return envelope.Data, err
	}
	return dataDir, root, run
}

func TestHarnessGrantLifecycleThroughTheCLI(t *testing.T) {
	dataDir, root, run := harnessFixture(t)

	status, err := run("authority", "status")
	if err != nil || status["enabled"] != false || status["grants"] != float64(0) {
		t.Fatalf("fresh status = %v, %v", status, err)
	}
	create := []string{"grant", "create", "--client", "claude-desktop", "--workspace", "ws", "--operation", "start,status"}
	if _, err := run(create...); err == nil {
		t.Fatal("grant minted before the capability pack was enabled")
	}
	if status, err := run("authority", "enable"); err != nil || status["enabled"] != true {
		t.Fatalf("enable = %v, %v", status, err)
	}

	for name, args := range map[string][]string{
		"no explicit workspace": {"grant", "create", "--client", "claude-desktop", "--operation", "start"},
		"approval":              {"grant", "create", "--client", "claude-desktop", "--workspace", "ws", "--operation", "approve"},
		"unknown client":        {"grant", "create", "--client", "ghost", "--workspace", "ws", "--operation", "start"},
		"unknown workspace":     {"grant", "create", "--client", "claude-desktop", "--workspace", "ghost", "--operation", "start"},
		"no operation":          {"grant", "create", "--client", "claude-desktop", "--workspace", "ws"},
	} {
		if _, err := run(args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	created, err := run(create...)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := created["token"].(string)
	grant, _ := created["grant"].(map[string]any)
	if !strings.HasPrefix(token, "hcap1.") || created["token_shown_once"] != true || grant["status"] != "active" {
		t.Fatalf("created = %v", created)
	}
	authority, err := openHarnessAuthority(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	principal, err := authority.Verify(token, harnessauth.Request{ClientID: "claude-desktop", Workspace: "ws", Operation: harnessauth.OpStart})
	if err != nil || principal.Root != canonical {
		t.Fatalf("verify = %+v, %v", principal, err)
	}

	listed, err := run("grant", "list")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(listed)
	if strings.Contains(string(encoded), strings.Split(token, ".")[2]) || strings.Contains(string(encoded), "secret") {
		t.Fatalf("list leaked credential material: %s", encoded)
	}
	if grants, _ := listed["grants"].([]any); len(grants) != 1 {
		t.Fatalf("list = %v", listed)
	}

	rotated, err := run("grant", "rotate", grant["id"].(string))
	if err != nil || rotated["token"] == token {
		t.Fatalf("rotate = %v, %v", rotated, err)
	}
	if _, err := authority.Verify(token, harnessauth.Request{Workspace: "ws", Operation: harnessauth.OpStart}); err == nil {
		t.Fatal("old token survived rotation")
	}
	if _, err := authority.Verify(rotated["token"].(string), harnessauth.Request{Workspace: "ws", Operation: harnessauth.OpStart}); err != nil {
		t.Fatalf("rotated token = %v", err)
	}

	revoked, err := run("grant", "revoke", grant["id"].(string))
	if err != nil || revoked["grant"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("revoke = %v, %v", revoked, err)
	}
	if _, err := authority.Verify(rotated["token"].(string), harnessauth.Request{Workspace: "ws", Operation: harnessauth.OpStart}); err == nil {
		t.Fatal("revoked grant still verifies")
	}
	if status, err := run("authority", "disable"); err != nil || status["enabled"] != false {
		t.Fatalf("disable = %v, %v", status, err)
	}
	if _, err := run("authority", "bogus"); err == nil {
		t.Fatal("unknown authority action accepted")
	}
}
