package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// llmUsageHandler implements POST /api/v1/llm-usage: records an LLM token
// usage sample for a workspace/run.
func llmUsageHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace        string `json:"workspace"`
			Provider         string `json:"provider"`
			Model            string `json:"model"`
			PromptTokens     int    `json:"prompt_tokens"`
			CompletionTokens int    `json:"completion_tokens"`
			TotalTokens      int    `json:"total_tokens"`
			RunLabel         string `json:"run_label"`
			MemoryEnabled    *bool  `json:"memory_enabled"`
			CreatedAt        string `json:"created_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		if strings.TrimSpace(req.Provider) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "provider is required")
			return
		}
		if req.PromptTokens < 0 || req.CompletionTokens < 0 || req.TotalTokens < 0 {
			writeErr(w, http.StatusBadRequest, "validation", "token counts must be non-negative")
			return
		}
		enabled := engine.MemoryEnabled()
		if req.MemoryEnabled != nil {
			enabled = *req.MemoryEnabled
		}
		label := strings.TrimSpace(req.RunLabel)
		if label == "" {
			label = engine.RunLabel()
		}
		var at time.Time
		if strings.TrimSpace(req.CreatedAt) != "" {
			t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.CreatedAt))
			if err != nil {
				t, err = time.Parse(time.RFC3339, strings.TrimSpace(req.CreatedAt))
				if err != nil {
					writeErr(w, http.StatusBadRequest, "validation", "invalid created_at")
					return
				}
			}
			at = t
		}
		if err := assets.Store.AddLLMUsageMetric(r.Context(), sqlite.LLMUsageInsert{
			Workspace:        ws,
			Provider:         strings.TrimSpace(req.Provider),
			Model:            strings.TrimSpace(req.Model),
			PromptTokens:     req.PromptTokens,
			CompletionTokens: req.CompletionTokens,
			TotalTokens:      req.TotalTokens,
			RunLabel:         label,
			MemoryEnabled:    enabled,
			CreatedAt:        at,
		}); err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"stored":         true,
			"workspace":      ws,
			"run_label":      label,
			"memory_enabled": enabled,
		})
	}
}

// benchmarkIngestHandler implements POST /api/v1/benchmark/ingest: stores a
// benchmark run record for a workspace.
func benchmarkIngestHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req sqlite.BenchmarkRun
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := strings.TrimSpace(req.Workspace)
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		if strings.TrimSpace(req.RunID) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "run_id is required")
			return
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		req.Workspace = ws
		stored, err := assets.Store.InsertBenchmarkRun(r.Context(), req)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"stored":    true,
			"workspace": ws,
			"run":       stored,
		})
	}
}

// benchmarkRunsHandler implements GET /api/v1/benchmark/runs: lists recent
// benchmark runs for a workspace.
func benchmarkRunsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if ws == "" {
			ws = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		limit := parseIntOrDefault(r.URL.Query().Get("limit"), 10)
		runs, err := assets.Store.ListBenchmarkRuns(r.Context(), ws, limit)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": ws,
			"limit":     clamp(limit, 1, 200),
			"runs":      runs,
		})
	}
}
