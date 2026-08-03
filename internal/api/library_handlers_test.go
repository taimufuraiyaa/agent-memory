package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

func TestLibraryImportIsDisabledByDefault(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "false")
	recorder := httptest.NewRecorder()
	NewMux(&Service{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/library/imports", bytes.NewBufferString(`{}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected disabled route to be hidden, got %d", recorder.Code)
	}
}

func TestLibraryImportQueryMemoryReview(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(&Service{Workspace: "books", BaseDir: t.TempDir(), EmbeddingProvider: provider}))
	defer server.Close()

	imported := libraryPost(t, server.URL+"/api/v1/library/imports", map[string]any{
		"workspace": "books", "library_id": "personal-1", "principal_id": "reader-1",
		"title": "Astronomy", "edition_label": "First", "language": "en",
		"markdown": "# Motion\nThe earth always spins around the sun.\n\n## Paths\nAll roads lead to Rome.",
	}, http.StatusAccepted)
	jobID := imported["id"].(string)
	result := imported["result"].(map[string]any)
	editionID := result["edition_id"].(string)

	job := libraryGet(t, server.URL+"/api/v1/library/jobs?id="+jobID, http.StatusOK)
	if job["state"] != "completed" {
		t.Fatalf("unexpected job: %+v", job)
	}
	structure := libraryGet(t, server.URL+"/api/v1/library/structure?workspace=books&principal_id=reader-1&edition_id="+editionID, http.StatusOK)
	if len(structure["nodes"].([]any)) != 2 {
		t.Fatalf("unexpected structure: %+v", structure)
	}

	queried := libraryPost(t, server.URL+"/api/v1/library/query", map[string]any{
		"workspace": "books", "principal_id": "reader-1", "question": "earth sun",
		"propose_memory": true, "memory_content": "The reader connected the roads proverb to invariant scientific facts.",
	}, http.StatusOK)
	if len(queried["results"].([]any)) == 0 {
		t.Fatal("expected grounded results")
	}
	proposal := queried["proposal"].(map[string]any)
	if proposal["status"] != "suggested" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}

	reviewed := libraryPost(t, server.URL+"/api/v1/library/memory-review", map[string]any{
		"workspace": "books", "proposal_id": proposal["id"], "principal_id": "reader-1", "decision": "accept",
	}, http.StatusOK)
	if reviewed["status"] != "accepted" || reviewed["memory_id"] == "" {
		t.Fatalf("unexpected review: %+v", reviewed)
	}

	unauthorized := libraryPostRaw(t, server.URL+"/api/v1/library/query", map[string]any{"workspace": "books", "principal_id": "stranger", "question": "earth"})
	if unauthorized.Code != http.StatusOK {
		t.Fatalf("query should conceal access with empty results, got %d", unauthorized.Code)
	}
	if len(unauthorized.Data["results"].([]any)) != 0 {
		t.Fatalf("unauthorized evidence leaked: %+v", unauthorized.Data)
	}
	unanswerable := libraryPostRaw(t, server.URL+"/api/v1/library/query", map[string]any{"workspace": "books", "principal_id": "reader-1", "question": "quantum", "propose_memory": true})
	if unanswerable.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected explicit unanswerable response, got %d", unanswerable.Code)
	}
}

func TestLibraryImportOrganizationScope(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(&Service{Workspace: "org-books", BaseDir: t.TempDir(), EmbeddingProvider: provider}))
	defer server.Close()
	libraryPost(t, server.URL+"/api/v1/library/imports", map[string]any{"workspace": "org-books", "library_id": "org-library", "library_kind": "organization", "organization_id": "org-1", "principal_id": "member-1", "title": "Shared", "edition_label": "v1", "language": "en", "markdown": "# Shared idea\nInstitutional knowledge remains attributed."}, http.StatusAccepted)
	visible := libraryPost(t, server.URL+"/api/v1/library/query", map[string]any{"workspace": "org-books", "principal_id": "member-1", "organization_ids": []string{"org-1"}, "question": "institutional"}, http.StatusOK)
	if len(visible["results"].([]any)) != 1 {
		t.Fatalf("organization member could not query source: %+v", visible)
	}
}

type libraryResponse struct {
	Code int
	Data map[string]any
}

func libraryPost(t *testing.T, url string, body any, status int) map[string]any {
	t.Helper()
	response := libraryPostRaw(t, url, body)
	if response.Code != status {
		t.Fatalf("POST %s: got %d data=%+v", url, response.Code, response.Data)
	}
	return response.Data
}

func libraryPostRaw(t *testing.T, url string, body any) libraryResponse {
	t.Helper()
	payload, _ := json.Marshal(body)
	response, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return decodeLibraryResponse(t, response)
}

func libraryGet(t *testing.T, url string, status int) map[string]any {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	decoded := decodeLibraryResponse(t, response)
	if decoded.Code != status {
		t.Fatalf("GET %s: got %d data=%+v", url, decoded.Code, decoded.Data)
	}
	return decoded.Data
}

func decodeLibraryResponse(t *testing.T, response *http.Response) libraryResponse {
	t.Helper()
	var value struct {
		Data  map[string]any `json:"data"`
		Error any            `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return libraryResponse{Code: response.StatusCode, Data: value.Data}
}
