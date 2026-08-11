package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/edge"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/telemetry"
)

func TestEdgeReadinessTraversesAPITelemetryAndInternalLookupStaysPrivate(t *testing.T) {
	observer := telemetry.New("api", slog.New(slog.NewTextHandler(io.Discard, nil)))
	apiRoutes := http.NewServeMux()
	registerOperationalRoutes(apiRoutes, func(context.Context) error { return nil }, observer)
	apiServer := httptest.NewServer(apiRoutes)
	defer apiServer.Close()

	upstream, err := url.Parse(apiServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	edgeHandler, err := edge.New(edge.Config{
		Upstream:       upstream,
		CountrySecret:  "0123456789abcdef0123456789abcdef",
		DefaultCountry: "US",
	})
	if err != nil {
		t.Fatal(err)
	}
	edgeServer := httptest.NewServer(edgeHandler)
	defer edgeServer.Close()

	const requestID = "8c73a4f1-027e-4ea5-95c8-75eb9a847ac4"
	const traceID = "0123456789abcdef0123456789abcdef"
	request, err := http.NewRequest(http.MethodGet, edgeServer.URL+"/_edge/health/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("X-Trace-ID", traceID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Request-ID") != requestID || response.Header.Get("X-Trace-ID") != traceID {
		t.Fatalf("edge response status=%d request=%q trace=%q", response.StatusCode, response.Header.Get("X-Request-ID"), response.Header.Get("X-Trace-ID"))
	}

	lookup, err := http.Get(apiServer.URL + "/internal/evidence/requests/" + requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer lookup.Body.Close()
	var observation telemetry.EvidenceObservation
	if err := json.NewDecoder(lookup.Body).Decode(&observation); err != nil {
		t.Fatal(err)
	}
	if lookup.StatusCode != http.StatusOK || observation.RequestID != requestID || observation.TraceID != traceID || observation.Service != "api" || observation.Operation != "GET:/health/ready" || observation.Status != http.StatusOK || observation.Outcome != "success" {
		t.Fatalf("unexpected telemetry observation: status=%d observation=%+v", lookup.StatusCode, observation)
	}

	blocked, err := http.Get(edgeServer.URL + "/internal/evidence/requests/" + requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusNotFound {
		t.Fatalf("edge exposed internal lookup with status %d", blocked.StatusCode)
	}
}
