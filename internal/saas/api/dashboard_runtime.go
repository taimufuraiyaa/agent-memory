package api

import (
	"encoding/json"
	"net/http"
)

func dashboardRuntime(mode, apiPrefix string, features ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema":     "agent-memory-dashboard-runtime-v1",
			"mode":       mode,
			"api_prefix": apiPrefix,
			"features":   features,
		})
	}
}
