package ingestion

import (
	"context"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WebFetchResult struct {
	FinalURL    string    `json:"final_url"`
	Body        []byte    `json:"body"`
	ContentType string    `json:"content_type"`
	FetchedAt   time.Time `json:"fetched_at"`
}
type WebFetcher interface {
	Fetch(context.Context, string) (WebFetchResult, error)
}
type RobotsChecker interface {
	Allowed(context.Context, string) (bool, error)
}
type WebCapture struct {
	ID                 string            `json:"id"`
	CanonicalURL       string            `json:"canonical_url"`
	ContentFingerprint string            `json:"content_fingerprint"`
	CapturedAt         time.Time         `json:"captured_at"`
	Passages           []library.Passage `json:"passages"`
}
type WebCaptureAdapter struct {
	ParserVersion, NormalizationVersion string
	Fetcher                             WebFetcher
	Robots                              RobotsChecker
}

func (a WebCaptureAdapter) Capture(ctx context.Context, editionID, assetID, rawURL string, policy core.SourcePolicy) (WebCapture, error) {
	if a.Fetcher == nil || a.Robots == nil || editionID == "" || assetID == "" || a.ParserVersion == "" || a.NormalizationVersion == "" {
		return WebCapture{}, errors.New("web capture adapter identity, versions, fetcher, and robots checker are required")
	}
	if err := policy.Validate(); err != nil {
		return WebCapture{}, err
	}
	if policy.Retention == core.RetentionDeleted || policy.Retention == core.RetentionSessionOnly || !policy.AllowSearch {
		return WebCapture{}, errors.New("source retention policy does not permit versioned web capture")
	}
	canonical, err := canonicalWebURL(rawURL)
	if err != nil {
		return WebCapture{}, err
	}
	allowed, err := a.Robots.Allowed(ctx, canonical)
	if err != nil || !allowed {
		if err != nil {
			return WebCapture{}, err
		}
		return WebCapture{}, errors.New("robots policy denied capture")
	}
	fetched, err := a.Fetcher.Fetch(ctx, canonical)
	if err != nil {
		return WebCapture{}, err
	}
	if !strings.Contains(strings.ToLower(fetched.ContentType), "html") {
		return WebCapture{}, errors.New("web book capture requires HTML")
	}
	if fetched.FetchedAt.IsZero() {
		return WebCapture{}, errors.New("web capture time is required")
	}
	if fetched.FinalURL != "" {
		canonical, err = canonicalWebURL(fetched.FinalURL)
		if err != nil {
			return WebCapture{}, err
		}
	}
	text := normalizeHTMLText(string(fetched.Body))
	if text == "" {
		return WebCapture{}, errors.New("web capture has no readable text")
	}
	fingerprint := core.FingerprintText(text)
	captureID := stableImportID("capture", canonical, fingerprint)
	passage := library.Passage{ID: stableImportID("passage", editionID, captureID, text), EditionID: editionID, SourceAssetID: assetID, StructuralNodeID: "web:" + captureID, Text: text, Fingerprint: fingerprint, Locator: core.SourceLocator{Kind: core.LocatorWeb, Display: canonical, ParserVersion: a.ParserVersion, NormalizationVersion: a.NormalizationVersion, Web: &core.WebLocator{CaptureID: captureID, CanonicalURL: canonical, Selector: "body", StartOffset: 0, EndOffset: len(text)}}}
	return WebCapture{ID: captureID, CanonicalURL: canonical, ContentFingerprint: fingerprint, CapturedAt: fetched.FetchedAt, Passages: []library.Passage{passage}}, nil
}
func canonicalWebURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid web source URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("unsupported web source scheme")
	}
	parsed.Fragment = ""
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func normalizeHTMLText(value string) string {
	return strings.Join(strings.Fields(htmlTagPattern.ReplaceAllString(value, " ")), " ")
}
