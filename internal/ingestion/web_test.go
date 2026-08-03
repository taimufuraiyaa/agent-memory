package ingestion_test

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"testing"
	"time"
)

type webFixture struct {
	body    string
	allowed bool
	err     error
}

func (f *webFixture) Allowed(context.Context, string) (bool, error) { return f.allowed, f.err }
func (f *webFixture) Fetch(context.Context, string) (ingestion.WebFetchResult, error) {
	if f.err != nil {
		return ingestion.WebFetchResult{}, f.err
	}
	return ingestion.WebFetchResult{FinalURL: "https://example.com/book", Body: []byte(f.body), ContentType: "text/html", FetchedAt: time.Now().UTC()}, nil
}
func TestWebCaptureVersionsAndCitationIdentity(t *testing.T) {
	policy := core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true, AllowQuote: true, MaxQuoteRunes: 100}
	fixture := &webFixture{allowed: true, body: "<html><body><p>First version</p></body></html>"}
	adapter := ingestion.WebCaptureAdapter{ParserVersion: "web-v1", NormalizationVersion: "text-v1", Fetcher: fixture, Robots: fixture}
	first, err := adapter.Capture(context.Background(), "edition", "asset", "https://EXAMPLE.com/book#now", policy)
	if err != nil {
		t.Fatal(err)
	}
	same, err := adapter.Capture(context.Background(), "edition", "asset", "https://example.com/book", policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != same.ID || first.Passages[0].Locator.Web.CaptureID != first.ID || first.Passages[0].Locator.Web.CanonicalURL != "https://example.com/book" {
		t.Fatalf("capture identity unstable: %+v", first)
	}
	fixture.body = "<html><body><p>Changed version</p></body></html>"
	changed, err := adapter.Capture(context.Background(), "edition", "asset", "https://example.com/book", policy)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID {
		t.Fatal("changed web content reused source version")
	}
}
func TestWebCapturePolicyFailuresPreventPublication(t *testing.T) {
	policy := core.SourcePolicy{Retention: core.RetentionRetained, AllowSearch: true}
	fixture := &webFixture{allowed: false, body: "<p>denied</p>"}
	adapter := ingestion.WebCaptureAdapter{ParserVersion: "v1", NormalizationVersion: "v1", Fetcher: fixture, Robots: fixture}
	if _, err := adapter.Capture(context.Background(), "e", "a", "https://example.com", policy); err == nil {
		t.Fatal("robots denial ignored")
	}
	fixture.allowed = true
	fixture.err = errors.New("access denied")
	if _, err := adapter.Capture(context.Background(), "e", "a", "https://example.com", policy); err == nil {
		t.Fatal("access failure ignored")
	}
	policy.Retention = core.RetentionSessionOnly
	if _, err := adapter.Capture(context.Background(), "e", "a", "https://example.com", policy); err == nil {
		t.Fatal("retention failure ignored")
	}
}
