// Package edge provides the local customer-ingress trust boundary.
package edge

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/launch"
)

const (
	countryHeader   = "X-Agent-Memory-Country"
	timestampHeader = "X-Agent-Memory-Country-Timestamp"
	signatureHeader = "X-Agent-Memory-Country-Signature"
)

type Config struct {
	Upstream       *url.URL
	CountrySecret  string
	DefaultCountry string
	Now            func() time.Time
	Transport      http.RoundTripper
}

func New(cfg Config) (http.Handler, error) {
	if cfg.Upstream == nil || cfg.Upstream.Host == "" || (cfg.Upstream.Scheme != "http" && cfg.Upstream.Scheme != "https") {
		return nil, errors.New("edge upstream must be an HTTP or HTTPS URL")
	}
	secret := strings.TrimSpace(cfg.CountrySecret)
	if len(secret) < 32 {
		return nil, errors.New("edge country signing secret must contain at least 32 characters")
	}
	country := strings.ToUpper(strings.TrimSpace(cfg.DefaultCountry))
	if len(country) != 2 || !allLetters(country) {
		return nil, errors.New("edge default country must be a two-letter code")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	signer := launch.NewCountryVerifier(secret, now)
	proxy := httputil.NewSingleHostReverseProxy(cfg.Upstream)
	if cfg.Transport != nil {
		proxy.Transport = cfg.Transport
	}
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "{\"ok\":false,\"error\":\"upstream unavailable\"}\n")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/_edge/health/live" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{\"service\":\"edge\",\"status\":\"ok\"}\n")
			return
		}
		if r.URL.Path == "/metrics" || strings.HasPrefix(r.URL.Path, "/metrics/") {
			http.NotFound(w, r)
			return
		}
		requestID := boundedRequestID(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", requestID)
		r.Header.Set("X-Request-ID", requestID)
		timestamp := strconv.FormatInt(now().UTC().Unix(), 10)
		r.Header.Set(countryHeader, country)
		r.Header.Set(timestampHeader, timestamp)
		r.Header.Set(signatureHeader, signer.Sign(country, timestamp))
		if r.URL.Path == "/_edge/health/ready" {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/health/ready"
			r = clone
		}
		proxy.ServeHTTP(w, r)
	}), nil
}

func boundedRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 96 {
		return uuid.NewString()
	}
	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return uuid.NewString()
		}
	}
	return value
}

func allLetters(value string) bool {
	for _, r := range value {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
