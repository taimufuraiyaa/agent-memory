package agentmemory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/clientauth"
	keyring "github.com/zalando/go-keyring"
)

func TestOSKeyringTokenProviderReadsHostedProfileCredential(t *testing.T) {
	keyring.MockInit()
	if err := (clientauth.OSKeyring{}).Set("sdk-test", "am_sk_sdk_secret"); err != nil {
		t.Fatal(err)
	}
	token, err := (OSKeyringTokenProvider{Profile: "sdk-test"}).Token(context.Background())
	if err != nil || token != "am_sk_sdk_secret" {
		t.Fatalf("token=%q err=%v", token, err)
	}
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func TestClientRequiresExplicitModeAndNeverImplicitlyUploads(t *testing.T) {
	requests := []*http.Request{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"id":"ok"}}`)
	}))
	defer server.Close()
	if _, err := New(Config{BaseURL: server.URL}); err == nil {
		t.Fatal("implicit mode was accepted")
	}
	local, err := New(Config{Mode: ModeLocal, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatal("constructing a client performed network or upload work")
	}
	if _, err := local.ImportPortable(context.Background(), "workspace", "correct horse battery", "idempotency-key-1", []byte("bundle")); err == nil {
		t.Fatal("local mode silently accepted a portable upload")
	}
	hosted, err := New(Config{Mode: ModeHosted, BaseURL: server.URL, TenantID: "11111111-1111-4111-8111-111111111111", TokenProvider: staticToken("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hosted.ImportPortable(context.Background(), "22222222-2222-4222-8222-222222222222", "correct horse battery", "idempotency-key-1", []byte("AMPB2")); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].URL.Path != "/v1/imports" || requests[0].Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("explicit upload requests=%v", requests)
	}
}

func TestClientMapsWriteByExplicitMode(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = io.WriteString(w, `{"ok":true,"data":{}}`)
	}))
	defer server.Close()
	local, _ := New(Config{Mode: ModeLocal, BaseURL: server.URL})
	hosted, _ := New(Config{Mode: ModeHosted, BaseURL: server.URL, TenantID: "11111111-1111-4111-8111-111111111111", TokenProvider: staticToken("secret")})
	value := MemoryWrite{WorkspaceID: "workspace", Type: "semantic", Content: "fact"}
	_, _ = local.WriteMemory(context.Background(), value, "idempotency-key-1")
	_, _ = hosted.WriteMemory(context.Background(), value, "idempotency-key-2")
	if len(paths) != 2 || paths[0] != "/api/v1/memories/write" || paths[1] != "/v1/memories" {
		t.Fatalf("mode paths=%v", paths)
	}
}

func TestHostedSearchUsesTenantAuthenticatedMemoryEndpoint(t *testing.T) {
	var request *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = r.Clone(r.Context())
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"ok":true,"data":{"items":[]}}`)
	}))
	defer server.Close()
	hosted, err := New(Config{Mode: ModeHosted, BaseURL: server.URL, TenantID: "11111111-1111-4111-8111-111111111111", TokenProvider: staticToken("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hosted.SearchHosted(context.Background(), "22222222-2222-4222-8222-222222222222", "durable fact", 25, "cursor-1"); err != nil {
		t.Fatal(err)
	}
	if request == nil || request.URL.Path != "/v1/search" || request.Header.Get("Authorization") != "Bearer secret" || request.Header.Get("X-Agent-Memory-Tenant") != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("hosted search request=%v", request)
	}
	want := `{"cursor":"cursor-1","limit":25,"query":"durable fact","workspace_id":"22222222-2222-4222-8222-222222222222"}`
	if string(body) != want {
		t.Fatalf("hosted search body=%s want=%s", body, want)
	}
}
