package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleTransportBindsWorkspaceAndUnwrapsEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("workspace") != "selected" {
			t.Errorf("query workspace: %s", r.URL)
		}
		if r.Method == "POST" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["workspace"] != "selected" {
				t.Errorf("body workspace: %v", body)
			}
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"notes":[]}}`))
	}))
	defer server.Close()
	backend, err := NewHTTPBackend(server.URL, "selected", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		result, err := backend.Execute(context.Background(), Request{Method: method, Path: "/api/v1/notes", Values: map[string]any{"workspace": "other"}})
		if err != nil || string(result) != `{"notes":[]}` {
			t.Fatalf("result=%s err=%v", result, err)
		}
	}
}

func TestConsoleTransportRejectsErrorsAndUnsafeRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"conflict","message":"revision changed"}}`))
	}))
	defer server.Close()
	backend, _ := NewHTTPBackend(server.URL, "selected", server.Client())
	_, err := backend.Execute(context.Background(), Request{Method: "POST", Path: "/api/v1/notes/update"})
	if err == nil || !strings.Contains(err.Error(), "revision changed") {
		t.Fatalf("error=%v", err)
	}
	for _, path := range []string{"https://evil.test/api/v1/notes", "/api/v1/../ops", "/dashboard/", "/api/v1/notes?workspace=other"} {
		if _, err := backend.Execute(context.Background(), Request{Method: "GET", Path: path}); err == nil {
			t.Errorf("accepted unsafe path %s", path)
		}
	}
}

func TestConsoleTransportUploadAndCreateOnlyExport(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	_ = os.WriteFile(source, []byte("# Source"), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/library/imports" {
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Error(err)
			}
			if r.FormValue("workspace") != "selected" {
				t.Error("upload workspace missing")
			}
			file, _, err := r.FormFile("source")
			if err != nil {
				t.Error(err)
			} else {
				_ = file.Close()
			}
			_, _ = w.Write([]byte(`{"ok":true,"data":{"state":"completed"}}`))
		} else {
			_, _ = w.Write([]byte("portable-data"))
		}
	}))
	defer server.Close()
	backend, _ := NewHTTPBackend(server.URL, "selected", server.Client())
	if _, err := backend.Execute(context.Background(), Request{Method: "POST", Path: "/api/v1/library/imports", UploadPath: source}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "export.ampb")
	request := Request{Method: "POST", Path: "/api/v1/migrations/portable-export", OutputPath: output}
	if _, err := backend.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Execute(context.Background(), request); err == nil {
		t.Fatal("overwrote existing export")
	}
	bytes, _ := os.ReadFile(output)
	if string(bytes) != "portable-data" {
		t.Fatalf("export=%s", bytes)
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
}

func TestConsoleRecentFiltersPinnedAndType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"data":{"results":[{"id":"a","type":"semantic","pinned":true},{"id":"b","type":"episodic","pinned":true},{"id":"c","type":"semantic","pinned":false}]}}`))
	}))
	defer server.Close()
	backend, _ := NewHTTPBackend(server.URL, "selected", server.Client())
	raw, err := backend.Execute(context.Background(), Request{Method: "GET", Path: "/api/v1/memories/recent", Values: map[string]any{"pinned_only": true, "memory_type": "semantic"}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	_ = json.Unmarshal(raw, &result)
	if len(result.Results) != 1 || result.Results[0].ID != "a" {
		t.Fatalf("filtered=%s", raw)
	}
}
