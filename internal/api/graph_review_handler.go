package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func graphExplorerHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return
		}
		workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		maxNodes := boundedGraphQueryLimit(r.URL.Query().Get("max_nodes"), 512, 4096)
		maxEdges := boundedGraphQueryLimit(r.URL.Query().Get("max_edges"), 2048, 16384)
		maxCommunities := boundedGraphQueryLimit(r.URL.Query().Get("max_communities"), 128, 2048)
		snapshot, err := assets.Store.LoadActiveGraphSnapshot(r.Context(), core.GraphScope{WorkspaceID: workspace}, maxNodes, maxEdges, maxCommunities)
		if err != nil {
			writeGraphOperationError(w, err)
			return
		}
		writeOK(w, http.StatusOK, snapshot)
	}
}

func graphReviewHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return
		}
		var review core.GraphReview
		if !decodeStrictGraphBody(w, r, &review) {
			return
		}
		review.Scope.TenantID = ""
		review.Scope.WorkspaceID = strings.TrimSpace(review.Scope.WorkspaceID)
		review.ReviewerID = "api"
		if review.ID == "" {
			review.ID = uuid.NewString()
		}
		if review.CreatedAt.IsZero() {
			review.CreatedAt = time.Now().UTC()
		}
		assets, err := svc.resolve(r.Context(), review.Scope.WorkspaceID)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		if err := assets.Store.ReviewGraphRecord(r.Context(), review); err != nil {
			if strings.Contains(err.Error(), "version conflict") {
				writeErr(w, http.StatusConflict, "graph_review_conflict", "The graph record changed; refresh and retry.")
			} else {
				writeErr(w, http.StatusBadRequest, "invalid_graph_review", "The graph review request is invalid.")
			}
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"reviewed": true, "review_id": review.ID})
	}
}

func graphFeedbackHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
			return
		}
		var feedback core.GraphFeedback
		if !decodeStrictGraphBody(w, r, &feedback) {
			return
		}
		feedback.Scope.TenantID = ""
		feedback.Scope.WorkspaceID = strings.TrimSpace(feedback.Scope.WorkspaceID)
		if feedback.ID == "" {
			feedback.ID = uuid.NewString()
		}
		assets, err := svc.resolve(r.Context(), feedback.Scope.WorkspaceID)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		if err := assets.Store.RecordGraphFeedback(r.Context(), feedback); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_graph_feedback", "The graph feedback request is invalid.")
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"recorded": true, "feedback_id": feedback.ID})
	}
}

func decodeStrictGraphBody(w http.ResponseWriter, r *http.Request, output any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, graphOperationRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "The graph request is invalid.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "invalid_request", "The request must contain one JSON object.")
		return false
	}
	return true
}

func boundedGraphQueryLimit(raw string, fallback, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}
