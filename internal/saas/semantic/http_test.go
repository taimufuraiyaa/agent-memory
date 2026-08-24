package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPPlannerReturnsValidatedVietnameseDefinitionPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		format, _ := request["response_format"].(map[string]any)
		if request["reasoning_effort"] != "none" || request["seed"] != float64(0) || format["type"] != "json_schema" {
			t.Fatalf("planner request=%+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"version\":\"query-plan-v1\",\"language\":\"vi\",\"intent\":\"definition\",\"subject\":\"throughput\",\"retrieval_terms\":[\"throughput\",\"requests per second\",\"throughput\"],\"exclusions\":[\"garbage collection\"],\"answer_form\":\"concise_definition\"}"}}]}`))
	}))
	defer server.Close()

	planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: server.URL, Model: "qwen3:8b", APIKey: "local", AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Plan(context.Background(), "Throughput là gì?")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Language != "vi" || plan.Intent != IntentDefinition || plan.Subject != "throughput" || len(plan.RetrievalTerms) != 2 {
		t.Fatalf("plan=%+v", plan)
	}
	if len(plan.Exclusions) != 1 || plan.Exclusions[0] != "garbage collection" || plan.AnswerForm != AnswerConciseDefinition {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestHTTPPlannerWarmPreloadsExactModelWithBoundedResidency(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/generate" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["model"] != "qwen3:8b" || request["keep_alive"] != "30m0s" || request["stream"] != false {
			t.Fatalf("warmup request=%+v", request)
		}
		if _, exists := request["prompt"]; exists {
			t.Fatalf("warmup transmitted prompt=%+v", request)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"done":true,"load_duration":123}`))}, nil
	})}

	planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second, AllowLoopback: true, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.Warm(context.Background(), 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if status := planner.WarmStatus(); status.State != PlannerWarm || status.Model != "qwen3:8b" || status.LastError != "" {
		t.Fatalf("warm status=%+v", status)
	}
}

func TestHTTPPlannerWarmFailureIsSanitizedAndUnavailable(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("private provider details"))}, nil
	})}

	planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second, AllowLoopback: true, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	err = planner.Warm(context.Background(), 30*time.Minute)
	if err == nil || strings.Contains(err.Error(), "private provider details") {
		t.Fatalf("warm error=%v", err)
	}
	if status := planner.WarmStatus(); status.State != PlannerUnavailable || status.LastError == "" || strings.Contains(status.LastError, "private provider details") {
		t.Fatalf("warm status=%+v", status)
	}
}

func TestHTTPPlannerWarmRequiresCompletedBoundedResponse(t *testing.T) {
	for name, body := range map[string]string{
		"incomplete": `{}`,
		"oversized":  strings.Repeat("x", maxProviderResponseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second, AllowLoopback: true, Client: client})
			if err != nil {
				t.Fatal(err)
			}
			if err := planner.Warm(context.Background(), 30*time.Minute); err == nil {
				t.Fatal("incomplete or oversized preload response was accepted")
			}
		})
	}
}

func TestHTTPPlannerWarmDeniesRedirects(t *testing.T) {
	var redirected atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/generate" {
			redirected.Add(1)
		}
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": []string{"http://127.0.0.1:11435/redirected"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
		}, nil
	})}
	planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second, AllowLoopback: true, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.Warm(context.Background(), 30*time.Minute); err == nil || redirected.Load() != 0 {
		t.Fatalf("redirect warmup err=%v redirected=%d", err, redirected.Load())
	}
}

func TestHTTPPlannerWarmUsesIndependentTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	planner, err := NewHTTPPlanner(PlannerConfig{
		Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: 5 * time.Second, WarmupTimeout: time.Second,
		AllowLoopback: true, Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := planner.Warm(context.Background(), 30*time.Minute); err == nil || time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("warm timeout err=%v elapsed=%s", err, time.Since(started))
	}
}

func TestHTTPPlannerCachesValidatedPlansByDigest(t *testing.T) {
	var requests atomic.Int32
	client := plannerResponseClient(func() string {
		requests.Add(1)
		return validDefinitionPlanResponse()
	})
	planner, err := NewHTTPPlanner(PlannerConfig{
		Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second,
		AllowLoopback: true, Client: client, CacheCapacity: 2, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range []string{"Throughput là gì?", "  Throughput   là gì?  "} {
		if _, err := planner.Plan(context.Background(), question); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("provider requests=%d, want 1", requests.Load())
	}
	for key := range planner.cache {
		if strings.Contains(key, "Throughput") || len(key) != 64 {
			t.Fatalf("cache key exposes question: %q", key)
		}
	}
}

func TestHTTPPlannerExpiresCachedPlansAndDoesNotCacheFailures(t *testing.T) {
	var requests atomic.Int32
	client := plannerResponseClient(func() string {
		if requests.Add(1) == 1 {
			return `{"choices":[{"message":{"content":"not-json"}}]}`
		}
		return validDefinitionPlanResponse()
	})
	planner, err := NewHTTPPlanner(PlannerConfig{
		Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second,
		AllowLoopback: true, Client: client, CacheCapacity: 2, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	planner.now = func() time.Time { return now }
	if _, err := planner.Plan(context.Background(), "Throughput là gì?"); err == nil {
		t.Fatal("malformed plan was accepted")
	}
	if _, err := planner.Plan(context.Background(), "Throughput là gì?"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute + time.Nanosecond)
	if _, err := planner.Plan(context.Background(), "Throughput là gì?"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatalf("provider requests=%d, want malformed retry plus expired retry", requests.Load())
	}
}

func TestHTTPPlannerCoalescesConcurrentIdenticalQuestions(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	client := plannerResponseClient(func() string {
		requests.Add(1)
		<-release
		return validDefinitionPlanResponse()
	})
	planner, err := NewHTTPPlanner(PlannerConfig{
		Endpoint: "http://127.0.0.1:11434", Model: "qwen3:8b", Timeout: time.Second,
		AllowLoopback: true, Client: client, CacheCapacity: 2, CacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, planErr := planner.Plan(context.Background(), "Throughput là gì?")
			errorsSeen <- planErr
		}()
	}
	for requests.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wait.Wait()
	close(errorsSeen)
	for planErr := range errorsSeen {
		if planErr != nil {
			t.Fatal(planErr)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("provider requests=%d, want 1", requests.Load())
	}
}

func plannerResponseClient(response func() string) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response()))}, nil
	})}
}

func validDefinitionPlanResponse() string {
	return `{"choices":[{"message":{"content":"{\"version\":\"query-plan-v1\",\"language\":\"vi\",\"intent\":\"definition\",\"subject\":\"throughput\",\"retrieval_terms\":[\"throughput\"],\"exclusions\":[],\"answer_form\":\"concise_definition\"}"}}]}`
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHTTPPlannerRejectsMalformedAndExcessivePlans(t *testing.T) {
	for name, content := range map[string]string{
		"malformed":        `not-json`,
		"unknown intent":   `{"version":"query-plan-v1","language":"vi","intent":"execute_sql","subject":"throughput","retrieval_terms":["throughput"],"answer_form":"concise_definition"}`,
		"invented subject": `{"version":"query-plan-v1","language":"vi","intent":"definition","subject":"technology","retrieval_terms":["throughput"],"exclusions":[],"answer_form":"concise_definition"}`,
		"too many terms":   fmt.Sprintf(`{"version":"query-plan-v1","language":"vi","intent":"definition","subject":"throughput","retrieval_terms":[%s],"answer_form":"concise_definition"}`, strings.Repeat(`"term",`, 16)+`"last"`),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
			}))
			defer server.Close()
			planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: server.URL, Model: "qwen3:8b", AllowLoopback: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planner.Plan(context.Background(), "Throughput là gì?"); err == nil {
				t.Fatal("unsafe plan was accepted")
			}
		})
	}
}

func TestLocalInferenceClientsRejectRedirectsAndUnapprovedEndpoints(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("redirect destination was reached")
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: redirect.URL, Model: "qwen3:8b", AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Plan(context.Background(), "Throughput là gì?"); err == nil {
		t.Fatal("redirect was accepted")
	}
	if _, err := NewHTTPPlanner(PlannerConfig{Endpoint: "http://example.com", Model: "qwen3:8b", AllowLoopback: true}); err == nil {
		t.Fatal("arbitrary remote endpoint was accepted")
	}
}

func TestLocalInferenceClientsEnforceRoleTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	planner, err := NewHTTPPlanner(PlannerConfig{Endpoint: server.URL, Model: "qwen3:8b", Timeout: time.Second, AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := planner.Plan(context.Background(), "Throughput là gì?"); err == nil || time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("planner timeout err=%v elapsed=%s", err, time.Since(started))
	}

	reranker, err := NewHTTPReranker(RerankerConfig{Endpoint: server.URL, Model: "reranker", Timeout: time.Second, AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	if _, err := reranker.Rerank(context.Background(), "query", []string{"document"}); err == nil || time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("reranker timeout err=%v elapsed=%s", err, time.Since(started))
	}
}

func TestHTTPRerankerReturnsScoresInSubmittedDocumentOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.08},{"index":0,"relevance_score":0.97}]}`))
	}))
	defer server.Close()

	reranker, err := NewHTTPReranker(RerankerConfig{Endpoint: server.URL, Model: "qwen3-reranker:0.6b", APIKey: "local", AllowLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	scores, err := reranker.Rerank(context.Background(), "Throughput là gì?", []string{"Throughput is work per unit time.", "Garbage collection pauses applications."})
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 || scores[0] != 0.97 || scores[1] != 0.08 {
		t.Fatalf("scores=%v", scores)
	}
}

func TestHTTPRerankerRejectsMissingDuplicateAndNonFiniteScores(t *testing.T) {
	for name, response := range map[string]string{
		"missing":   `{"results":[{"index":0,"relevance_score":0.9}]}`,
		"duplicate": `{"results":[{"index":0,"relevance_score":0.9},{"index":0,"relevance_score":0.8}]}`,
		"nonfinite": `{"results":[{"index":0,"relevance_score":1e999},{"index":1,"relevance_score":0.8}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			reranker, err := NewHTTPReranker(RerankerConfig{Endpoint: server.URL, Model: "reranker", AllowLoopback: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reranker.Rerank(context.Background(), "query", []string{"one", "two"}); err == nil {
				t.Fatal("invalid reranking response was accepted")
			}
		})
	}
}
