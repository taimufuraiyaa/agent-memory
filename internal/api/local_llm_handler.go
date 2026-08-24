package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/localllm"
)

const maxLocalLLMConfigBytes = 64 << 10

type localLLMConfigRequest struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"base_url"`
	TextModel      string `json:"text_model"`
	VisionModel    string `json:"vision_model,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	ClearAPIKey    bool   `json:"clear_api_key,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (r localLLMConfigRequest) config() localllm.Config {
	return localllm.Config{Enabled: r.Enabled, BaseURL: r.BaseURL, TextModel: r.TextModel, VisionModel: r.VisionModel, APIKey: r.APIKey, TimeoutSeconds: r.TimeoutSeconds}
}

func (s *Service) localLLMRuntime() (*localllm.Store, *localllm.Checker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LocalLLMStore == nil {
		s.LocalLLMStore = localllm.NewStore(s.BaseDir)
	}
	if s.LocalLLMChecker == nil {
		s.LocalLLMChecker = localllm.NewChecker(nil)
	}
	return s.LocalLLMStore, s.LocalLLMChecker
}

func libraryLocalLLMHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		store, checker := svc.localLLMRuntime()
		switch r.Method {
		case http.MethodGet:
			config, found, err := store.Load()
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
				return
			}
			if !found {
				writeOK(w, http.StatusOK, localllm.Status{Config: localllm.Config{}.Public()})
				return
			}
			writeOK(w, http.StatusOK, checker.Check(r.Context(), config))
		case http.MethodPut:
			request, err := decodeLocalLLMConfigRequest(r)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "validation", err.Error())
				return
			}
			saved, err := store.Save(request.config(), request.ClearAPIKey)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "validation", err.Error())
				return
			}
			writeOK(w, http.StatusOK, checker.Check(r.Context(), saved))
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	}
}

func libraryLocalLLMTestHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		store, checker := svc.localLLMRuntime()
		request, err := decodeLocalLLMConfigRequest(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		config := request.config()
		if strings.TrimSpace(config.APIKey) == "" && !request.ClearAPIKey {
			if existing, found, loadErr := store.Load(); loadErr == nil && found {
				config.APIKey = existing.APIKey
			}
		}
		writeOK(w, http.StatusOK, checker.Check(r.Context(), config))
	}
}

func decodeLocalLLMConfigRequest(r *http.Request) (localLLMConfigRequest, error) {
	if contentType := r.Header.Get("content-type"); contentType != "application/json" && !strings.HasPrefix(contentType, "application/json;") {
		return localLLMConfigRequest{}, errors.New("content-type must be application/json")
	}
	var request localLLMConfigRequest
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxLocalLLMConfigBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return localLLMConfigRequest{}, err
	}
	return request, nil
}
