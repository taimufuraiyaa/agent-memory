package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestMultiWorkspaceResolveUsesRegisteredCustomDBPaths(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ENABLED", "true")
	baseDir := t.TempDir()
	customDir := t.TempDir()
	alphaDB := filepath.Join(customDir, "alpha-custom.db")
	betaDB := filepath.Join(baseDir, "beta.db")
	registry := workspace.Registry{Projects: []workspace.Project{
		{Name: "alpha", DBPath: alphaDB, CreatedAt: time.Now()},
		{Name: "beta", DBPath: betaDB, CreatedAt: time.Now()},
	}}
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{BaseDir: baseDir, EmbeddingProvider: provider}
	t.Cleanup(func() { _ = svc.Close() })

	alpha, err := svc.resolve(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("resolve alpha: %v", err)
	}
	beta, err := svc.resolve(context.Background(), "beta")
	if err != nil {
		t.Fatalf("resolve beta: %v", err)
	}
	if alpha == beta {
		t.Fatal("workspaces must have isolated runtime assets")
	}
	if _, err := os.Stat(alphaDB); err != nil {
		t.Fatalf("custom alpha DB not opened: %v", err)
	}
	if _, err := os.Stat(betaDB); err != nil {
		t.Fatalf("beta DB not opened: %v", err)
	}

	server := httptest.NewServer(NewMux(svc))
	defer server.Close()
	postJSON(t, server.URL+"/api/v1/memories/write", map[string]any{"workspace": "alpha", "type": "semantic", "content": "alpha-only memory"})
	postJSON(t, server.URL+"/api/v1/memories/write", map[string]any{"workspace": "beta", "type": "semantic", "content": "beta-only memory"})
	alphaRecent := getJSON(t, server.URL+"/api/v1/memories/recent?workspace=alpha&limit=10")
	betaRecent := getJSON(t, server.URL+"/api/v1/memories/recent?workspace=beta&limit=10")
	if len(alphaRecent["results"].([]any)) != 1 || len(betaRecent["results"].([]any)) != 1 {
		t.Fatalf("workspace writes were not isolated: alpha=%+v beta=%+v", alphaRecent, betaRecent)
	}
}

func TestMultiWorkspaceResolveRejectsUnknownWithoutCreatingDB(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{BaseDir: baseDir, EmbeddingProvider: provider}
	if _, err := svc.resolve(context.Background(), "../../escape"); err == nil {
		t.Fatal("expected unknown workspace rejection")
	}
	if _, err := os.Stat(filepath.Join(baseDir, "escape.db")); !os.IsNotExist(err) {
		t.Fatalf("unexpected DB creation: %v", err)
	}
}

func TestMultiWorkspaceResolveRejectsWorkspaceRemovedFromRegistry(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "alpha.db")
	registry := workspace.Registry{Projects: []workspace.Project{{Name: "alpha", DBPath: dbPath}}}
	data, _ := json.Marshal(registry)
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{BaseDir: baseDir, EmbeddingProvider: provider}
	t.Cleanup(func() { _ = svc.Close() })
	if _, err := svc.resolve(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.resolve(context.Background(), "alpha"); err == nil {
		t.Fatal("removed workspace remained routable from cache")
	}
}

func TestMultiWorkspaceHTTPRejectsUnknownWorkspaceAsClientError(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ENABLED", "true")
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{BaseDir: baseDir, EmbeddingProvider: provider}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/memories/write", bytes.NewBufferString(`{"workspace":"unknown","type":"semantic","content":"must not create"}`))
	response := httptest.NewRecorder()
	NewMux(svc).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMultiWorkspaceHealthAdvertisesDaemonMode(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "workspaces.json"), []byte(`{"projects":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewMux(&Service{BaseDir: baseDir, EmbeddingProvider: provider}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data["service_mode"] != "multi_workspace" {
		t.Fatalf("health=%+v", envelope.Data)
	}
}
