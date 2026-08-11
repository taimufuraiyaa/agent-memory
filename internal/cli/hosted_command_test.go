package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/clientauth"
)

type hostedTokenFixture struct {
	values  map[string]string
	deleted []string
}

func (s *hostedTokenFixture) Set(profile, token string) error    { s.values[profile] = token; return nil }
func (s *hostedTokenFixture) Get(profile string) (string, error) { return s.values[profile], nil }
func (s *hostedTokenFixture) Delete(profile string) error {
	delete(s.values, profile)
	s.deleted = append(s.deleted, profile)
	return nil
}

func TestHostedCLIUsesOSKeyringAndRevokesWithoutPersistingToken(t *testing.T) {
	store := &hostedTokenFixture{values: map[string]string{}}
	previous := newHostedTokenStore
	newHostedTokenStore = func() clientauth.Store { return store }
	t.Cleanup(func() { newHostedTokenStore = previous })
	t.Setenv("AGENT_MEMORY_CONFIG_DIR", t.TempDir())
	tenant := "11111111-1111-4111-8111-111111111111"
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer am_sk_secret" || r.Header.Get("X-Agent-Memory-Tenant") != tenant {
			t.Errorf("hosted authorization headers were incomplete")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"version":"v1","request_id":"request","data":{"revoked":true}}`)
	}))
	defer server.Close()

	login := newHostedCommand()
	login.SetIn(strings.NewReader("am_sk_secret\n"))
	login.SetOut(io.Discard)
	login.SetArgs([]string{"login", "--profile", "test", "--api", server.URL, "--tenant", tenant, "--token-stdin"})
	if err := login.Execute(); err != nil {
		t.Fatal(err)
	}
	profileBytes, err := os.ReadFile(hostedProfilePath("test"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(profileBytes, []byte("am_sk_secret")) || store.values["test"] != "am_sk_secret" {
		t.Fatalf("profile leaked token or keyring missed it: profile=%s keyring=%q", profileBytes, store.values["test"])
	}
	var profile map[string]any
	if json.Unmarshal(profileBytes, &profile) != nil || profile["api_url"] != server.URL {
		t.Fatalf("profile metadata=%s", profileBytes)
	}

	logout := newHostedCommand()
	logout.SetOut(io.Discard)
	logout.SetArgs([]string{"logout", "--profile", "test"})
	if err := logout.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || len(requests) != 1 || requests[0] != "DELETE /v1/current-credential" {
		t.Fatalf("deleted=%v requests=%v", store.deleted, requests)
	}
	if _, err := os.Stat(hostedProfilePath("test")); !os.IsNotExist(err) {
		t.Fatalf("profile metadata remains after logout: %v", err)
	}
}

func TestHostedImportRequiresExplicitPortableFileAndPassphrase(t *testing.T) {
	command := newHostedCommand()
	command.SetOut(io.Discard)
	command.SetArgs([]string{"import", "--workspace", "workspace", "--bundle", "memory.db"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "passphrase-stdin") {
		t.Fatalf("implicit database import error=%v", err)
	}
}

func TestHostedMCPInjectsKeyringTokenOnlyIntoChildEnvironment(t *testing.T) {
	store := &hostedTokenFixture{values: map[string]string{"test": "am_sk_child_only"}}
	previousStore, previousRun := newHostedTokenStore, runHostedMCP
	newHostedTokenStore = func() clientauth.Store { return store }
	t.Cleanup(func() { newHostedTokenStore, runHostedMCP = previousStore, previousRun })
	t.Setenv("AGENT_MEMORY_CONFIG_DIR", t.TempDir())
	profile := hostedProfile{Name: "test", APIURL: "https://memory.example.test", TenantID: "11111111-1111-4111-8111-111111111111"}
	if err := saveHostedProfile(profile); err != nil {
		t.Fatal(err)
	}
	var executable string
	var environment []string
	runHostedMCP = func(_ context.Context, gotExecutable string, gotEnvironment []string, _ io.Reader, _, _ io.Writer) error {
		executable, environment = gotExecutable, gotEnvironment
		return nil
	}
	t.Setenv("AGENT_MEMORY_TOKEN", "stale-token")
	command := newHostedCommand()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"mcp", "--profile", "test", "--server", "custom-mcp"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if executable != "custom-mcp" || !strings.Contains(joined, "AGENT_MEMORY_TOKEN=am_sk_child_only") || strings.Contains(joined, "stale-token") || !strings.Contains(joined, "AGENT_MEMORY_MODE=hosted") || !strings.Contains(joined, "AGENT_MEMORY_API_URL=https://memory.example.test") || !strings.Contains(joined, "AGENT_MEMORY_TENANT_ID=11111111-1111-4111-8111-111111111111") {
		t.Fatalf("executable=%q environment=%q", executable, joined)
	}
}
