package api

import (
	"encoding/json"
	"net/http"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// sessionEndHandler implements POST /api/v1/memories/session-end and
// /api/v1/sessions/end (two paths registered for backwards compatibility;
// both run the same session-end lifecycle).
func sessionEndHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Transcript     string `json:"transcript"`
			SessionID      string `json:"session_id,omitempty"`
			PrincipalID    string `json:"principal_id,omitempty"`
			TerminalStatus string `json:"terminal_status,omitempty"`
			IdempotencyKey string `json:"idempotency_key,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		ws := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), ws)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		out, err := application.RunSessionEnd(r.Context(), application.SessionEndInput{
			Workspace: ws, SessionID: req.SessionID, PrincipalID: req.PrincipalID, Transcript: req.Transcript,
			TerminalStatus: core.SolutionEpisodeStatus(req.TerminalStatus), IdempotencyKey: req.IdempotencyKey,
		}, assets.Store, assets.Writer)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}
