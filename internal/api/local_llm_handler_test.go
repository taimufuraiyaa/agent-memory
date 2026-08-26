package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/localllm"
)

func TestLibraryLocalLLMTranslateUsesSavedLocalModel(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen3:4b"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Xin chào"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	service := &Service{BaseDir: t.TempDir()}
	localLLMRequest(t, NewMux(service), http.MethodPut, "/api/v1/library/local-llm", map[string]any{
		"enabled": true, "base_url": provider.URL + "/v1", "text_model": "qwen3:4b", "timeout_seconds": 2,
	})
	response := performLocalTranslationRequest(t, NewMux(service), map[string]any{
		"workspace": "agent-memory", "text": "Hello", "target_language": "vi",
	})
	if response.Text != "Xin chào" || response.TargetLanguage != "vi" || response.Model != "qwen3:4b" {
		t.Fatalf("translation = %+v", response)
	}
}

func TestLibraryLocalLLMTranslateReportsUnavailableWithoutConfiguration(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"workspace": "agent-memory", "text": "Hello", "target_language": "vi"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/library/local-llm/translate", bytes.NewReader(body))
	request.Header.Set("content-type", "application/json")
	response := httptest.NewRecorder()
	NewMux(&Service{BaseDir: t.TempDir()}).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !bytes.Contains(response.Body.Bytes(), []byte(`"translation_unavailable"`)) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestLibraryLocalLLMTranslateValidatesWorkspaceAndLanguage(t *testing.T) {
	for name, payload := range map[string]map[string]any{
		"workspace": {"workspace": "", "text": "Hello", "target_language": "vi"},
		"language":  {"workspace": "agent-memory", "text": "Hello", "target_language": "unsupported"},
	} {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(payload)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/library/local-llm/translate", bytes.NewReader(body))
			request.Header.Set("content-type", "application/json")
			response := httptest.NewRecorder()
			NewMux(&Service{BaseDir: t.TempDir()}).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"invalid_translation"`)) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

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

func performLocalTranslationRequest(t *testing.T, handler http.Handler, payload any) localllm.TranslationResult {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/library/local-llm/translate", bytes.NewReader(body))
	request.Header.Set("content-type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("translate status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data localllm.TranslationResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
