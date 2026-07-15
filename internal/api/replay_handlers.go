package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/replay"
)

func replaySessionsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if workspaceName == "" {
			workspaceName = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		limit := clamp(parseIntOrDefault(r.URL.Query().Get("limit"), 50), 1, 200)
		sessions, err := assets.Store.ListSessions(r.Context(), workspaceName, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"workspace": workspaceName, "sessions": sessions, "limit": limit})
	}
}

func replayEventsHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceName := strings.TrimSpace(r.URL.Query().Get("workspace"))
		if workspaceName == "" {
			workspaceName = workspaceFromRequest(r, svc.Workspace)
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sessionID == "" {
			writeErr(w, http.StatusBadRequest, "validation", "session_id is required")
			return
		}
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		limit := clamp(parseIntOrDefault(r.URL.Query().Get("limit"), 100), 1, 499)
		events, err := assets.Store.LoadReplayEvents(r.Context(), workspaceName, sessionID, limit+1, strings.TrimSpace(r.URL.Query().Get("cursor")))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "runtime", err.Error())
			return
		}
		nextCursor := ""
		if len(events) > limit {
			events = events[:limit]
			nextCursor = events[len(events)-1].OccurredAt.Format(time.RFC3339Nano)
		}
		writeOK(w, http.StatusOK, map[string]any{"workspace": workspaceName, "session_id": sessionID, "events": events, "count": len(events), "next_cursor": nextCursor})
	}
}

func replayImportJSONLHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var request struct {
			Workspace string `json:"workspace"`
			Path      string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		workspaceName := strings.TrimSpace(request.Workspace)
		if workspaceName == "" {
			workspaceName = workspaceFromRequest(r, svc.Workspace)
		}
		assets, err := svc.resolve(r.Context(), workspaceName)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		result, err := replay.NewImporter(assets.Store).Import(r.Context(), replay.ImportOptions{Workspace: workspaceName, Path: request.Path})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "import", err.Error())
			return
		}
		writeOK(w, http.StatusOK, result)
	}
}
