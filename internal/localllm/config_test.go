package localllm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRejectsNonLoopbackEndpoints(t *testing.T) {
	for _, endpoint := range []string{"https://example.com/v1", "http://192.168.1.20:11434/v1", "file:///tmp/model"} {
		cfg := Config{Enabled: true, BaseURL: endpoint, TextModel: "qwen"}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected %q to be rejected", endpoint)
		}
	}
	if err := (Config{Enabled: true, BaseURL: "http://127.0.0.1:11434/v1", TextModel: "qwen"}).Validate(); err != nil {
		t.Fatalf("loopback endpoint should be valid: %v", err)
	}
}

func TestStorePersistsOwnerOnlyConfigAndPreservesWriteOnlySecret(t *testing.T) {
	store := NewStore(t.TempDir())
	written, err := store.Save(Config{Enabled: true, BaseURL: "http://localhost:11434/v1", TextModel: "qwen", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if written.APIKey != "secret" {
		t.Fatal("saved config should be returned internally with its secret")
	}
	info, err := os.Stat(filepath.Join(store.BaseDir, configFilename))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}

	updated, err := store.Save(Config{Enabled: true, BaseURL: "http://localhost:11434/v1", TextModel: "qwen-2"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.APIKey != "secret" {
		t.Fatal("blank API key should preserve the stored secret")
	}
	if updated.Public().APIKeyConfigured != true {
		t.Fatal("public config should report a configured key")
	}
}

func TestCheckerDiscoversConfiguredOpenAICompatibleModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization header was not forwarded")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
	}))
	defer server.Close()

	status := NewChecker(nil).Check(context.Background(), Config{Enabled: true, BaseURL: server.URL + "/v1", TextModel: "qwen-local", APIKey: "secret", TimeoutSeconds: 2})
	if !status.Configured || !status.Enabled || !status.Reachable || !status.TextModelAvailable {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Config.APIKeyConfigured != true {
		t.Fatal("status should report a configured key without exposing it")
	}
}

func TestCheckerDoesNotFollowProviderRedirects(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/models", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	status := NewChecker(nil).Check(context.Background(), Config{Enabled: true, BaseURL: redirect.URL, TextModel: "qwen-local", TimeoutSeconds: 2})
	if targetCalled || status.Reachable {
		t.Fatalf("checker followed a redirect: target_called=%t status=%+v", targetCalled, status)
	}
}
