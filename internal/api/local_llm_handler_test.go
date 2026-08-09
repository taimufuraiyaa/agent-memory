package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/localllm"
)

func TestLibraryLocalLLMStatusDefaultsToParserOnly(t *testing.T) {
	response := httptest.NewRecorder()
	NewMux(&Service{BaseDir: t.TempDir()}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/library/local-llm", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data localllm.Status `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Configured || envelope.Data.Enabled || envelope.Data.Reachable {
		t.Fatalf("unexpected default status: %+v", envelope.Data)
	}
}

func TestLibraryLocalLLMSetupTestsAndSavesOpenAICompatibleEndpoint(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
	}))
	defer provider.Close()

	service := &Service{BaseDir: t.TempDir()}
	payload := map[string]any{"enabled": true, "base_url": provider.URL, "text_model": "qwen-local", "api_key": "secret", "timeout_seconds": 2}
	tested := localLLMRequest(t, NewMux(service), http.MethodPost, "/api/v1/library/local-llm/test", payload)
	if !tested.Reachable || !tested.TextModelAvailable {
		t.Fatalf("test status = %+v", tested)
	}
	saved := localLLMRequest(t, NewMux(service), http.MethodPut, "/api/v1/library/local-llm", payload)
	if !saved.Configured || !saved.Enabled || !saved.Reachable || !saved.Config.APIKeyConfigured {
		t.Fatalf("saved status = %+v", saved)
	}
	encoded, _ := json.Marshal(saved)
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatal("API response exposed the local LLM credential")
	}
}

func localLLMRequest(t *testing.T, handler http.Handler, method, path string, payload any) localllm.Status {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("content-type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, body = %s", method, path, response.Code, response.Body.String())
	}
	var envelope struct {
		Data localllm.Status `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
