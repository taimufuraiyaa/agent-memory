package api

import (
	"net/http"
	"strings"
)

// schedulerStatusHandler implements GET /api/v1/scheduler/status.
func schedulerStatusHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if svc.Scheduler == nil {
			writeOK(w, http.StatusOK, &SchedulerStatus{Enabled: false, Workspaces: []SchedulerWorkspaceStatus{}})
			return
		}
		status, err := svc.Scheduler.Status(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, status)
	}
}

// schedulerHistoryHandler implements GET /api/v1/scheduler/history.
func schedulerHistoryHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if svc.Scheduler == nil {
			writeOK(w, http.StatusOK, map[string]any{
				"workspace": strings.TrimSpace(r.URL.Query().Get("workspace")),
				"limit":     clamp(parseIntOrDefault(r.URL.Query().Get("limit"), 30), 1, 200),
				"runs":      []SchedulerRun{},
			})
			return
		}
		workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
		limit := clamp(parseIntOrDefault(r.URL.Query().Get("limit"), 30), 1, 200)
		runs, err := svc.Scheduler.History(r.Context(), workspace, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace": workspace,
			"limit":     limit,
			"runs":      runs,
		})
	}
}

// schedulerRunHandler implements POST /api/v1/scheduler/run.
func schedulerRunHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if svc.Scheduler == nil {
			writeErr(w, http.StatusNotFound, "not_found", "scheduler not available")
			return
		}
		workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if workspace == "" {
			workspace = workspaceFromRequest(r, svc.Workspace)
		}
		force := strings.TrimSpace(r.URL.Query().Get("force"))
		run, err := svc.Scheduler.RunNow(r.Context(), workspace, force == "1" || strings.EqualFold(force, "true"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, run)
	}
}
