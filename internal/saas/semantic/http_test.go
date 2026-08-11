package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
