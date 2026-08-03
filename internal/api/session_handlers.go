package api

import (
	"encoding/json"
	"net/http"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
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
			Transcript string `json:"transcript"`
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
		out, err := engine.RunSessionEndLifecycle(r.Context(), ws, req.Transcript, assets.Store, assets.Writer)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}
