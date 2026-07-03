package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// requestsFeedbackHandler implements POST /api/v1/requests/feedback:
// submits a retrieval request feedback score (0-5) for a specific request ID.
func requestsFeedbackHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace   string `json:"workspace"`
			RequestID   string `json:"request_id"`
			Score       int    `json:"score"`
			Reason      string `json:"reason"`
			UsefulCount *int   `json:"useful_count,omitempty"`
			TotalCount  *int   `json:"total_count,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		requestID := strings.TrimSpace(req.RequestID)
		if requestID == "" {
			writeErr(w, http.StatusBadRequest, "validation", "request_id is required")
			return
		}
		if req.Score < 0 || req.Score > 5 {
			writeErr(w, http.StatusBadRequest, "validation", "score must be between 0 and 5")
			return
		}
		if req.Score < 4 && strings.TrimSpace(req.Reason) == "" {
			writeErr(w, http.StatusBadRequest, "validation", "reason is required for scores below 4")
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
		useful := -1
		if req.UsefulCount != nil {
			useful = *req.UsefulCount
		}
		total := -1
		if req.TotalCount != nil {
			total = *req.TotalCount
		}
		if err := assets.Store.RecordRequestFeedback(r.Context(), requestID, req.Score, req.Reason, useful, total); err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{
			"workspace":    ws,
			"request_id":   requestID,
			"score":        req.Score,
			"reason":       req.Reason,
			"useful_count": useful,
			"total_count":  total,
			"ok":           true,
		})
	}
}

// feedbackStatsHandler implements GET /api/v1/feedback/stats:
// returns retrieval feedback score statistics (week, month, year averages).
func feedbackStatsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		stats, err := assets.Store.GetFeedbackStats(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, stats)
	}
}

// listFeedbackHandler implements GET /api/v1/feedback:
// returns list of all retrieval requests (with their feedback score and reason).
func listFeedbackHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		if assets.Store == nil {
			writeErr(w, http.StatusInternalServerError, "runtime", "store is not available")
			return
		}
		list, err := assets.Store.ListRetrievalRequests(r.Context(), ws)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, list)
	}
}
