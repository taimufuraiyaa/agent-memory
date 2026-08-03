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

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

func TestNotesAPIProvidesCreateUpdateListAndRestoreFlow(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	defer server.Close()

	created := postJSON(t, server.URL+"/api/v1/notes/create", map[string]any{
		"workspace":  "ws",
		"path":       "Projects/Launch.md",
		"title":      "Launch",
		"body":       "# Launch\n\nSee [[Decision]].",
		"properties": map[string]any{"status": "draft"},
	})
	note := created["note"].(map[string]any)
	noteID := note["id"].(string)
	if note["revision"].(float64) != 1 || note["index_state"] != "pending" {
		t.Fatalf("unexpected created note: %+v", note)
	}

	listed := getJSON(t, server.URL+"/api/v1/notes?workspace=ws")
	notes := listed["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %+v", notes)
	}

	updated := postJSON(t, server.URL+"/api/v1/notes/update", map[string]any{
		"workspace":         "ws",
		"note_id":           noteID,
		"expected_revision": 1,
		"path":              "Projects/Launch.md",
		"title":             "Launch",
		"body":              "# Launch\n\nApproved.",
		"properties":        map[string]any{"status": "approved"},
	})
	updatedNote := updated["note"].(map[string]any)
	if updatedNote["revision"].(float64) != 2 {
		t.Fatalf("expected revision 2, got %+v", updatedNote)
	}

	assertNoteAPIError(t, server.URL+"/api/v1/notes/update", http.StatusConflict, "revision_conflict", map[string]any{
		"workspace":         "ws",
		"note_id":           noteID,
		"expected_revision": 1,
		"path":              "Projects/Launch.md",
		"title":             "Launch",
		"body":              "stale",
	})

	postJSON(t, server.URL+"/api/v1/notes/trash", map[string]any{"workspace": "ws", "note_id": noteID})
	active := getJSON(t, server.URL+"/api/v1/notes?workspace=ws")
	if len(active["notes"].([]any)) != 0 {
		t.Fatalf("trashed note should not be active: %+v", active)
	}
	postJSON(t, server.URL+"/api/v1/notes/restore", map[string]any{"workspace": "ws", "note_id": noteID})
	active = getJSON(t, server.URL+"/api/v1/notes?workspace=ws")
	if len(active["notes"].([]any)) != 1 {
		t.Fatalf("restored note should be active: %+v", active)
	}
}

func TestNotesAPIRestoresHistoricalRevisionAsNewRevision(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir()}
	server := httptest.NewServer(NewMux(svc))
	defer server.Close()

	created := postJSON(t, server.URL+"/api/v1/notes/create", map[string]any{
		"workspace": "ws",
		"path":      "Decision.md",
		"title":     "Decision",
		"body":      "version one",
	})
	noteID := created["note"].(map[string]any)["id"].(string)
	postJSON(t, server.URL+"/api/v1/notes/update", map[string]any{
		"workspace":         "ws",
		"note_id":           noteID,
		"expected_revision": 1,
		"path":              "Decision.md",
		"title":             "Decision",
		"body":              "version two",
	})

	restored := postJSON(t, server.URL+"/api/v1/notes/revisions/restore", map[string]any{
		"workspace":         "ws",
		"note_id":           noteID,
		"revision":          1,
		"expected_revision": 2,
	})
	note := restored["note"].(map[string]any)
	if note["revision"].(float64) != 3 || note["body"] != "version one" {
		t.Fatalf("unexpected restored revision: %+v", note)
	}
}

func TestIndexedNoteAppearsInSearchWithNavigableProvenance(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model: %v", err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir(), EmbeddingProvider: provider}
	server := httptest.NewServer(NewMux(svc))
	defer server.Close()

	created := postJSON(t, server.URL+"/api/v1/notes/create", map[string]any{
		"workspace": "ws",
		"path":      "Plans/Green launch.md",
		"title":     "Green launch",
		"body":      "# Green launch\n\nThe approved launch decision uses green packaging.",
	})
	note := created["note"].(map[string]any)
	noteID := note["id"].(string)
	assets, err := svc.resolve(context.Background(), "ws")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if err := assets.Notes.IndexLatest(context.Background(), "ws", noteID); err != nil {
		t.Fatalf("index note: %v", err)
	}

	search := postJSON(t, server.URL+"/api/v1/memories/search", map[string]any{
		"workspace": "ws",
		"query":     "green packaging launch decision",
		"top_k":     10,
		"mode":      "search",
		"explain":   true,
	})
	results := search["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("expected indexed note search result: %+v", search)
	}
	source := results[0].(map[string]any)["source"].(map[string]any)
	if source["note_id"] != noteID || source["note_path"] != "Plans/Green launch.md" {
		t.Fatalf("missing navigable note provenance: %+v", source)
	}

	postJSON(t, server.URL+"/api/v1/notes/trash", map[string]any{"workspace": "ws", "note_id": noteID})
	memories, err := assets.Store.ListMemoriesByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list memories after trash: %v", err)
	}
	for _, memory := range memories {
		if memory.Source.NoteID == noteID {
			t.Fatalf("trashed note remained in retrieval storage: %+v", memory)
		}
	}
}

func assertNoteAPIError(t *testing.T, url string, status int, code string, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	response, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("expected status %d, got %d", status, response.StatusCode)
	}
	var envelope map[string]any
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errorBody := envelope["error"].(map[string]any)
	if errorBody["code"] != code {
		t.Fatalf("expected error code %q, got %+v", code, errorBody)
	}
}
