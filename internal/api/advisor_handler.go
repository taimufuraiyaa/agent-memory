package api

import (
	"net/http"

	"github.com/taimufuraiyaa/agent-memory/internal/advisor"
)

func advisorHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspace := workspaceFromRequest(r, svc.Workspace)
		assets, err := svc.resolve(r.Context(), workspace)
		if err != nil {
			writeWorkspaceResolveError(w, err)
			return
		}
		report, err := advisor.BuildReport(r.Context(), assets.Store, workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, report)
	}
}
