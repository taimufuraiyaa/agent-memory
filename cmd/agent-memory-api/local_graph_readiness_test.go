package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLocalGraphOllamaEndpointUsesOnlyFixedLocalOptions(t *testing.T) {
	t.Setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", "")
	if got := localGraphOllamaEndpoint(); got != "http://127.0.0.1:11434" {
		t.Fatalf("default Ollama endpoint = %q", got)
	}
	t.Setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", "true")
	if got := localGraphOllamaEndpoint(); got != "http://host.docker.internal:11434" {
		t.Fatalf("dev Ollama endpoint = %q", got)
	}
	for _, value := range []string{"false", "http://remote.example:11434", "TRUE"} {
		t.Setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", value)
		if got := localGraphOllamaEndpoint(); got != "http://127.0.0.1:11434" {
			t.Fatalf("Ollama endpoint accepted %q: %q", value, got)
		}
	}
}

func TestLocalGraphOllamaReadinessRequiresEveryExactModel(t *testing.T) {
	client := &http.Client{Transport: graphReadinessRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/tags" {
			t.Fatalf("unexpected Ollama readiness path: %s", request.URL.Path)
		}
		return graphReadinessResponse(http.StatusOK, `{"models":[{"name":"qwen3:8b"},{"name":"qwen3-embedding:0.6b"}]}`), nil
	})}

	if err := localOllamaModelsAvailableWithClient(context.Background(), client, "http://127.0.0.1:11434", []string{"qwen3:8b", "qwen3-embedding:0.6b"}); err != nil {
		t.Fatalf("available exact local models rejected: %v", err)
	}
	if err := localOllamaModelsAvailableWithClient(context.Background(), client, "http://127.0.0.1:11434", []string{"qwen3:8b", "qwen3-embedding:0.6b-q4"}); err == nil {
		t.Fatal("a different model tag was accepted")
	}
}

func TestLocalGraphOllamaReadinessDoesNotFollowRedirects(t *testing.T) {
	requested := 0
	client := &http.Client{
		Transport: graphReadinessRoundTripper(func(request *http.Request) (*http.Response, error) {
			requested++
			return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"https://remote.example/api/tags"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}

	if err := localOllamaModelsAvailableWithClient(context.Background(), client, "http://127.0.0.1:11434", []string{"qwen3:8b"}); err == nil {
		t.Fatal("Ollama model inventory redirect was followed")
	}
	if requested != 1 {
		t.Fatalf("redirect performed %d requests; expected only the fixed local request", requested)
	}
}

type graphReadinessRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip graphReadinessRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func graphReadinessResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
