package api

import (
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// healthHandler implements GET /health: a lightweight liveness/readiness
// check reporting basic workspace stats (memory count, db size, last
// lifecycle run) and embedding provider info.
func healthHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		var memoryCount int
		var lastLifecycleRun string
		var dbSizeMB float64

		ws := svc.Workspace
		if ws == "" {
			ws = "agent-memory" // fallback if empty
		}

		assets, err := svc.resolve(r.Context(), ws)
		if err == nil && assets.Store != nil {
			if summary, err := assets.Store.GetWorkspaceActivitySummary(r.Context(), ws); err == nil {
				memoryCount = summary.MemoryCount
			}
			if state, err := assets.Store.GetSchedulerWorkspaceState(r.Context(), ws); err == nil && state != nil && !state.LastCompletedAt.IsZero() {
				lastLifecycleRun = state.LastCompletedAt.Format(time.RFC3339)
			}
		}

		dbPath := filepath.Join(svc.BaseDir, ws+".db")
		if fi, err := os.Stat(dbPath); err == nil {
			dbSizeMB = float64(fi.Size()) / (1024 * 1024)
		}

		providerName := "unknown"
		providerVersion := "unknown"
		onnxAvailable := false
		if svc.EmbeddingProvider != nil {
			providerName = svc.EmbeddingProvider.Name()
			providerVersion = svc.EmbeddingProvider.ModelVersion()
			onnxAvailable = (providerName == "onnx-minilm-l6-v2")
		}

		// Round dbSizeMB to two decimal places
		dbSizeMB = math.Round(dbSizeMB*100) / 100

		writeOK(w, http.StatusOK, map[string]any{
			"status":                  status,
			"db_size_mb":              dbSizeMB,
			"memory_count":            memoryCount,
			"last_lifecycle_run":      lastLifecycleRun,
			"embedding_provider":      providerName,
			"embedding_model_version": providerVersion,
			"onnx_runtime_available":  onnxAvailable,
		})
	}
}

// opsDashboardHandler implements GET /ops/dashboard: a static operator HTML
// page (see OperatorDashboardHTML in ops_dashboard.go). It takes svc for
// consistency with the other route-handler factories, though it does not
// currently use it.
func opsDashboardHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(OperatorDashboardHTML))
	}
}
