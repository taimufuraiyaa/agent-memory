package edge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launch"
)

func TestProxyReplacesTrustedHeadersAndPreservesCorrelation(t *testing.T) {
	const secret = "edge-country-signing-secret-32-bytes"
	now := time.Unix(1_800_000_000, 0).UTC()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("X-Request-ID"); got != "request-123" {
			t.Errorf("request ID = %q", got)
		}
		country := r.Header.Get("X-Agent-Memory-Country")
		if country != "VN" {
			t.Errorf("country = %q", country)
		}
		verifier := launch.NewCountryVerifier(secret, func() time.Time { return now })
		if !verifier.Verify(country, r.Header.Get("X-Agent-Memory-Country-Timestamp"), r.Header.Get("X-Agent-Memory-Country-Signature")) {
			t.Error("edge geography assertion was not verifiable")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})

	target, _ := url.Parse("http://api:8080")
	handler, err := New(Config{Upstream: target, CountrySecret: secret, DefaultCountry: "VN", Now: func() time.Time { return now }, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "request-123")
	request.Header.Set("X-Agent-Memory-Country", "US")
	request.Header.Set("X-Agent-Memory-Country-Timestamp", "1")
	request.Header.Set("X-Agent-Memory-Country-Signature", "spoofed")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") != "request-123" {
		t.Fatalf("response request ID = %q", response.Header().Get("X-Request-ID"))
	}
}

func TestProxyBlocksMetricsAndReportsContentFreeUpstreamFailure(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:1")
	handler, err := New(Config{Upstream: target, CountrySecret: "edge-country-signing-secret-32-bytes", DefaultCountry: "VN", Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})})
	if err != nil {
		t.Fatal(err)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusNotFound {
		t.Fatalf("metrics status = %d", metrics.Code)
	}
	unavailable := httptest.NewRecorder()
	handler.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if unavailable.Code != http.StatusBadGateway {
		t.Fatalf("upstream status = %d", unavailable.Code)
	}
	body, _ := io.ReadAll(unavailable.Body)
	if string(body) != "{\"ok\":false,\"error\":\"upstream unavailable\"}\n" {
		t.Fatalf("unexpected failure body %q", body)
	}
	if strings.Contains(string(body), target.String()) {
		t.Fatal("failure response disclosed upstream")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	httpTarget, _ := url.Parse("http://api:8080")
	for name, cfg := range map[string]Config{
		"short secret": {Upstream: httpTarget, CountrySecret: "short", DefaultCountry: "VN"},
		"bad country":  {Upstream: httpTarget, CountrySecret: "edge-country-signing-secret-32-bytes", DefaultCountry: "VNM"},
		"no upstream":  {CountrySecret: "edge-country-signing-secret-32-bytes", DefaultCountry: "VN"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}
