package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
)

func TestPortableMigrationExportIsEncryptedCopyWithoutSourceOriginals(t *testing.T) {
	const passphrase = "correct horse battery staple"
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	defer server.Close()

	postJSON(t, server.URL+"/api/v1/memories/write", map[string]any{
		"workspace": "ws", "type": "semantic", "content": "portable private fact",
	})
	postJSON(t, server.URL+"/api/v1/notes/create", map[string]any{
		"workspace": "ws", "path": "Migration.md", "body": "# Migration\n\nportable private note",
	})
	beforeMemories := getJSON(t, server.URL+"/api/v1/memories/recent?workspace=ws&limit=10")
	beforeNotes := getJSON(t, server.URL+"/api/v1/notes?workspace=ws")

	body, _ := json.Marshal(map[string]string{"workspace": "ws", "passphrase": passphrase})
	response, err := http.Post(server.URL+"/api/v1/migrations/portable-export", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("portable export: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure, _ := io.ReadAll(response.Body)
		t.Fatalf("portable export status=%d body=%s", response.StatusCode, failure)
	}
	if !strings.Contains(response.Header.Get("Content-Disposition"), ".ampb2") {
		t.Fatalf("missing portable attachment name: %q", response.Header.Get("Content-Disposition"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("portable response must not be cached: %q", response.Header.Get("Cache-Control"))
	}
	encrypted, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read portable export: %v", err)
	}
	if bytes.Contains(encrypted, []byte("portable private")) || bytes.Contains(encrypted, []byte(passphrase)) {
		t.Fatal("encrypted response exposed content or passphrase")
	}
	plain, err := exportservice.DecryptPortable(passphrase, encrypted)
	if err != nil {
		t.Fatalf("decrypt portable export: %v", err)
	}
	var bundle exportservice.Bundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		t.Fatalf("decode portable export: %v", err)
	}
	if err := bundle.VerifyManifest(); err != nil {
		t.Fatalf("verify portable manifest: %v", err)
	}
	if len(bundle.Memories) != 1 || len(bundle.Notes) != 1 {
		t.Fatalf("unexpected bundle counts: memories=%d notes=%d", len(bundle.Memories), len(bundle.Notes))
	}
	if bundle.SourceBytesIncluded || len(bundle.SourceObjects) != 0 {
		t.Fatal("browser migration must exclude source originals")
	}

	afterMemories := getJSON(t, server.URL+"/api/v1/memories/recent?workspace=ws&limit=10")
	afterNotes := getJSON(t, server.URL+"/api/v1/notes?workspace=ws")
	if len(afterMemories["results"].([]any)) != len(beforeMemories["results"].([]any)) || len(afterNotes["notes"].([]any)) != len(beforeNotes["notes"].([]any)) {
		t.Fatal("copy-first export changed local data")
	}
}

func TestPortableMigrationExportRejectsWeakPassphrase(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/migrations/portable-export", strings.NewReader(`{"workspace":"ws","passphrase":"too-short"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	NewMux(svc).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected weak passphrase rejection, got status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "too-short") {
		t.Fatal("error response exposed the submitted passphrase")
	}
}
