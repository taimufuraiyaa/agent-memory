package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
)

type blockingRoleRunner struct{}

func (blockingRoleRunner) Run(ctx context.Context, input readingroom.RoleRunInput) (readingroom.RoleRunResult, error) {
	<-ctx.Done()
	return readingroom.RoleRunResult{}, ctx.Err()
}

func TestSeminarProgressAndAuthorizedIdempotentCancellation(t *testing.T) {
	t.Setenv("AGENT_MEMORY_LIBRARY_ENABLED", "true")
	server := httptest.NewServer(NewMux(&Service{LibraryRoleRunner: blockingRoleRunner{}}))
	defer server.Close()
	packet := readingroom.EvidencePacket{Question: "Discuss the claim", AuthorizationFingerprint: readingroom.AuthorizationFingerprint(libraryScope("reader", nil)), RetrievalVersion: "v1", Evidence: []library.Passage{apiTestPassage()}, Profiles: readingroom.SeminarProfiles()}
	started := libraryPost(t, server.URL+"/api/v1/library/seminars/start", map[string]any{"run_id": "run-1", "principal_id": "reader", "packet": packet, "max_tokens": 700}, http.StatusAccepted)
	if started["status"] != "running" {
		t.Fatalf("unexpected start: %+v", started)
	}
	if hidden := libraryGetRaw(t, server.URL+"/api/v1/library/seminars/status?id=run-1&principal_id=peer"); hidden.Code != http.StatusNotFound {
		t.Fatal("seminar existence leaked")
	}
	for i := 0; i < 2; i++ {
		cancelled := libraryPost(t, server.URL+"/api/v1/library/seminars/cancel", map[string]any{"id": "run-1", "principal_id": "reader"}, http.StatusOK)
		if cancelled["status"] != "cancelled" {
			t.Fatalf("cancel was not idempotent: %+v", cancelled)
		}
	}
	time.Sleep(10 * time.Millisecond)
	status := libraryGet(t, server.URL+"/api/v1/library/seminars/status?id=run-1&principal_id=reader", http.StatusOK)
	if status["status"] != "cancelled" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if _, exists := status["result"]; exists {
		t.Fatal("progress endpoint exposed contribution/source content")
	}
}

func apiTestPassage() library.Passage {
	return library.Passage{ID: "p", EditionID: "e", SourceAssetID: "a", StructuralNodeID: "n", Text: "evidence", Fingerprint: core.FingerprintText("evidence"), Locator: core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter", ParserVersion: "v1", NormalizationVersion: "v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 1, NormalizedStart: 0, NormalizedEnd: 1}}}
}
func libraryGetRaw(t *testing.T, url string) libraryResponse {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return decodeLibraryResponse(t, response)
}
