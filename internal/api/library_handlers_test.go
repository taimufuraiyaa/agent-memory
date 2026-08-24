package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

func TestGeneratedLibraryIdentityIsStableAndScoped(t *testing.T) {
	personal := LibraryImportRequest{Workspace: "books"}
	applyLibraryImportIdentity(&personal)
	if personal.PrincipalID == "" || personal.LibraryID == "" {
		t.Fatalf("expected generated personal identity, got %+v", personal)
	}

	again := LibraryImportRequest{Workspace: "books", LibraryKind: "personal"}
	applyLibraryImportIdentity(&again)
	if personal.PrincipalID != again.PrincipalID || personal.LibraryID != again.LibraryID {
		t.Fatalf("generated identity is not stable: first=%+v again=%+v", personal, again)
	}

	organization := LibraryImportRequest{Workspace: "books", LibraryKind: "organization", OrganizationID: "org-1"}
	applyLibraryImportIdentity(&organization)
	if organization.PrincipalID != personal.PrincipalID {
		t.Fatalf("reader identity changed with library kind: personal=%q organization=%q", personal.PrincipalID, organization.PrincipalID)
	}
	if organization.LibraryID == personal.LibraryID {
		t.Fatalf("organization and personal libraries share identity %q", organization.LibraryID)
	}

	otherOrganization := LibraryImportRequest{Workspace: "books", LibraryKind: "organization", OrganizationID: "org-2"}
	applyLibraryImportIdentity(&otherOrganization)
	if otherOrganization.LibraryID == organization.LibraryID {
		t.Fatalf("organization library identity ignored organization scope: %q", organization.LibraryID)
	}
}

func TestGeneratedLibraryIdentityPreservesExplicitClientValues(t *testing.T) {
	req := LibraryImportRequest{
		Workspace:   "books",
		LibraryID:   "client-library",
		PrincipalID: "client-reader",
	}
	applyLibraryImportIdentity(&req)
	if req.PrincipalID != "client-reader" || req.LibraryID != "client-library" {
		t.Fatalf("explicit client identity was replaced: %+v", req)
	}
}

func TestEffectiveLibraryPrincipalUsesGeneratedWorkspaceIdentity(t *testing.T) {
	generated := effectiveLibraryPrincipalID("books", "")
	if generated == "" || generated != effectiveLibraryPrincipalID("books", "") {
		t.Fatalf("generated reader identity is empty or unstable: %q", generated)
	}
	if generated == effectiveLibraryPrincipalID("other-books", "") {
		t.Fatalf("generated reader identity is not workspace scoped: %q", generated)
	}
	if got := effectiveLibraryPrincipalID("books", "client-reader"); got != "client-reader" {
		t.Fatalf("explicit reader identity changed to %q", got)
	}
}

func TestLibraryFlowDerivesOmittedReaderAndLibraryIDs(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	mux := NewMux(&Service{Workspace: "generated-books", BaseDir: t.TempDir(), EmbeddingProvider: provider})

	imported := libraryMuxRequest(t, mux, http.MethodPost, "/api/v1/library/imports", map[string]any{
		"workspace": "generated-books", "title": "Generated Scope", "edition_label": "First", "language": "en",
		"markdown": "# Identity\nGenerated scope remains stable across the complete flow.",
	}, http.StatusAccepted)
	result := imported["result"].(map[string]any)
	editionID := result["edition_id"].(string)

	structure := libraryMuxRequest(t, mux, http.MethodGet, "/api/v1/library/structure?workspace=generated-books&edition_id="+editionID, nil, http.StatusOK)
	if len(structure["nodes"].([]any)) != 1 {
		t.Fatalf("generated reader could not access imported structure: %+v", structure)
	}

	queried := libraryMuxRequest(t, mux, http.MethodPost, "/api/v1/library/query", map[string]any{
		"workspace": "generated-books", "question": "stable complete flow", "propose_memory": true,
	}, http.StatusOK)
	if len(queried["results"].([]any)) != 1 {
		t.Fatalf("generated reader could not query imported source: %+v", queried)
	}
	proposal := queried["proposal"].(map[string]any)

	reviewed := libraryMuxRequest(t, mux, http.MethodPost, "/api/v1/library/memory-review", map[string]any{
		"workspace": "generated-books", "proposal_id": proposal["id"], "decision": "accept",
	}, http.StatusOK)
	if reviewed["status"] != "accepted" {
		t.Fatalf("generated reader could not review proposal: %+v", reviewed)
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
	if result["passage_count"] != float64(2) {
		t.Fatalf("expected complete passage count in import result, got %+v", result)
	}

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

func TestLibraryImportEPUBMultipart(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	server := newLibraryTestServer(t, "epub-books")

	imported := libraryMultipartPost(t, server.URL+"/api/v1/library/imports", map[string]string{
		"workspace": "epub-books", "library_id": "personal-epub", "principal_id": "reader-1",
		"title": "EPUB Book", "edition_label": "First", "language": "en", "format": "epub",
	}, "source", "book.epub", apiSyntheticEPUB(t), http.StatusAccepted)
	result := imported["result"].(map[string]any)
	if result["format"] != "epub" || result["passage_count"] != float64(2) || result["node_count"] != float64(2) {
		t.Fatalf("unexpected EPUB import: %+v", result)
	}
}

func TestLibraryImportPDFMultipart(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	server := newLibraryTestServer(t, "pdf-books")

	imported := libraryMultipartPost(t, server.URL+"/api/v1/library/imports", map[string]string{
		"workspace": "pdf-books", "library_id": "personal-pdf", "principal_id": "reader-1",
		"title": "PDF Book", "edition_label": "First", "language": "en", "format": "pdf",
	}, "source", "book.pdf", apiMinimalTextPDF("Grounded PDF evidence."), http.StatusAccepted)
	result := imported["result"].(map[string]any)
	if result["format"] != "pdf" || result["passage_count"].(float64) < 1 || result["node_count"] != float64(1) {
		t.Fatalf("unexpected PDF import: %+v", result)
	}
}

func TestLibraryImportRejectsUnsupportedAndOversizedMultipart(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	server := newLibraryTestServer(t, "bounded-books")

	libraryMultipartPost(t, server.URL+"/api/v1/library/imports", map[string]string{
		"workspace": "bounded-books", "library_id": "personal", "principal_id": "reader",
		"title": "Unsupported", "edition_label": "First", "language": "en", "format": "docx",
	}, "source", "book.docx", []byte("document"), http.StatusBadRequest)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/library/imports", bytes.NewReader([]byte("oversized")))
	request.Header.Set("content-type", "multipart/form-data; boundary=book")
	request.ContentLength = maxLibraryUploadBytes + 1
	response := httptest.NewRecorder()
	NewMux(&Service{}).ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized upload rejection, got %d: %s", response.Code, response.Body.String())
	}
}

type libraryResponse struct {
	Code int
	Data map[string]any
}

func libraryMuxRequest(t *testing.T, handler http.Handler, method, path string, body any, status int) map[string]any {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("content-type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var value struct {
		Data  map[string]any `json:"data"`
		Error any            `json:"error"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != status {
		t.Fatalf("%s %s: got %d data=%+v error=%+v", method, path, recorder.Code, value.Data, value.Error)
	}
	return value.Data
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

func newLibraryTestServer(t *testing.T, workspace string) *httptest.Server {
	t.Helper()
	modelDir := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := embeddings.NewLocalProvider(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(&Service{Workspace: workspace, BaseDir: t.TempDir(), EmbeddingProvider: provider}))
	t.Cleanup(server.Close)
	return server
}

func libraryMultipartPost(t *testing.T, url string, fields map[string]string, fileField, filename string, source []byte, status int) map[string]any {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile(fileField, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(url, writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	decoded := decodeLibraryResponse(t, response)
	if decoded.Code != status {
		t.Fatalf("multipart POST %s: got %d data=%+v", url, decoded.Code, decoded.Data)
	}
	return decoded.Data
}

func apiSyntheticEPUB(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	files := map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf":      `<package><metadata><title>API Book</title><language>en</language><identifier>api-1</identifier></metadata><manifest><item id="c1" href="c1.xhtml"/><item id="c2" href="c2.xhtml"/></manifest><spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`,
		"OEBPS/c1.xhtml":         `<html><head><title>One</title></head><body><h1>One</h1><p>First API chapter.</p></body></html>`,
		"OEBPS/c2.xhtml":         `<html><head><title>Two</title></head><body><h1>Two</h1><p>Second API chapter.</p></body></html>`,
	}
	for name, value := range files {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func apiMinimalTextPDF(text string) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
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
