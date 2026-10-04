package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientProbesAndExecutesOnlySelectedModel(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing API auth")
		}
		switch r.URL.Path {
		case "/v1/models/gpt-6-luna":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "gpt-6-luna", "object": "model"})
		case "/v1/responses":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["model"] != "gpt-6-luna" || request["store"] != false || request["max_output_tokens"] != float64(128) {
				t.Errorf("unsafe response request: %+v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "model": "gpt-6-luna", "output_text": "Answer", "usage": map[string]any{"input_tokens": 5, "output_tokens": 3}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClientWithURL(server.URL)
	if err := client.ProbeModel(context.Background(), "test-key", "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	result, err := client.Generate(context.Background(), "test-key", "gpt-6-luna", "Task", 128)
	if err != nil || result.Text != "Answer" || result.Model != "gpt-6-luna" || calls != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}

func TestClientRejectsDeniedMalformedAndMismatchedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"wrong_model": `{"status":"completed","model":"other","output_text":"No"}`,
		"incomplete":  `{"status":"incomplete","model":"gpt-6-luna","output_text":"Partial"}`,
		"empty":       `{"status":"completed","model":"gpt-6-luna","output_text":""}`,
		"malformed":   `{`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			if _, err := NewClientWithURL(server.URL).Generate(context.Background(), "test-key", "gpt-6-luna", "Task", 128); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("private-key"))
	}))
	defer server.Close()
	if _, err := NewClientWithURL(server.URL).Generate(context.Background(), "test-key", "gpt-6-luna", "Task", 128); err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("denial leaked response: %v", err)
	}
}
