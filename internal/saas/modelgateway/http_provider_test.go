package modelgateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPProviderEmbedsAndGeneratesWithoutLeakingCredential(t *testing.T) {
	const secret = "provider-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatalf("missing provider authorization")
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/embeddings":
			_, _ = response.Write([]byte(`{"data":[{"index":1,"embedding":[0,1,0]},{"index":0,"embedding":[1,0,0]}]}`))
		case "/v1/chat/completions":
			_, _ = response.Write([]byte(`{"choices":[{"message":{"content":"bounded answer"}}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(HTTPProviderConfig{Name: "openai-compatible", Endpoint: server.URL, APIKey: secret, Model: "private-route-v1", Dimension: 3, Retention: "zero-retention", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := provider.EmbedBatch(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("provider did not restore response index order: %#v", vectors)
	}
	generated, err := provider.Generate(context.Background(), "prompt")
	if err != nil || generated != "bounded answer" {
		t.Fatalf("generate = %q, %v", generated, err)
	}
}

func TestHTTPProviderRejectsRedirectAndSanitizesUpstreamFailure(t *testing.T) {
	const upstreamSecret = "upstream-body-customer-secret"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/embeddings" {
			http.Redirect(response, request, "/capture", http.StatusTemporaryRedirect)
			return
		}
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(upstreamSecret))
	}))
	defer server.Close()

	provider, err := NewHTTPProvider(HTTPProviderConfig{Name: "managed", Endpoint: server.URL, APIKey: "key-value", Model: "route", Dimension: 3, Retention: "zero-retention", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.EmbedBatch(context.Background(), []string{"input"})
	if err == nil || strings.Contains(err.Error(), upstreamSecret) {
		t.Fatalf("expected sanitized redirect failure, got %v", err)
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		t.Fatal("redirect policy failure must not be retried")
	}
}

func TestHTTPProviderMarksRetryableStatusAndBoundsResponse(t *testing.T) {
	t.Run("retryable status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusTooManyRequests)
			_, _ = response.Write([]byte("sensitive provider detail"))
		}))
		defer server.Close()
		provider, _ := NewHTTPProvider(HTTPProviderConfig{Name: "managed", Endpoint: server.URL, APIKey: "key", Model: "route", Dimension: 3, Retention: "zero-retention", Client: server.Client()})
		_, err := provider.EmbedBatch(context.Background(), []string{"input"})
		var temporary interface{ Temporary() bool }
		if !errors.As(err, &temporary) || !temporary.Temporary() || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("expected sanitized temporary failure, got %v", err)
		}
	})

	t.Run("oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			_, _ = response.Write([]byte(strings.Repeat(" ", maxProviderResponseBytes+1)))
		}))
		defer server.Close()
		provider, _ := NewHTTPProvider(HTTPProviderConfig{Name: "managed", Endpoint: server.URL, APIKey: "key", Model: "route", Dimension: 3, Retention: "zero-retention", Client: server.Client()})
		if _, err := provider.EmbedBatch(context.Background(), []string{"input"}); err == nil {
			t.Fatal("expected oversized provider response to fail")
		}
	})
}

func TestHTTPProviderRequiresCompleteConfiguration(t *testing.T) {
	for _, config := range []HTTPProviderConfig{
		{},
		{Name: "managed", Endpoint: "https://models.example", APIKey: "key", Model: "route"},
		{Name: "managed", Endpoint: "ftp://models.example", APIKey: "key", Model: "route", Dimension: 3, Retention: "zero-retention"},
	} {
		if _, err := NewHTTPProvider(config); err == nil {
			t.Fatalf("expected incomplete configuration to fail: %#v", config)
		}
	}
}
