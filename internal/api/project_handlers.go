package api

import (
	"encoding/json"
	"net/http"

	"github.com/time/timebooks/agent-memory/internal/workspace"
)

// projectsInitHandler implements POST /api/v1/projects/init.
func projectsInitHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			CWD         string `json:"cwd"`
			ProjectName string `json:"project_name"`
			Study       bool   `json:"study"`
			Reuse       bool   `json:"reuse"`
			Force       bool   `json:"force"`
			NoRule      bool   `json:"no_rule"`
			RulePath    string `json:"rule_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.Init(r.Context(), workspace.InitOptions{
			CWD:         req.CWD,
			ProjectName: req.ProjectName,
			Study:       req.Study,
			Reuse:       req.Reuse,
			Force:       req.Force,
			NoRule:      req.NoRule,
			RulePath:    req.RulePath,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}

// projectsRenameHandler implements POST /api/v1/projects/rename.
func projectsRenameHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			CWD  string `json:"cwd"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.Rename(r.Context(), workspace.RenameOptions{CWD: req.CWD, From: req.From, To: req.To})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}

// projectsListHandler implements GET /api/v1/projects/list.
func projectsListHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.List(r.Context())
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"projects": out})
	}
}

// projectsDeleteHandler implements POST /api/v1/projects/delete.
func projectsDeleteHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			ProjectName string `json:"project_name"`
			KeepData    bool   `json:"keep_data"`
			Yes         bool   `json:"yes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		out, err := mgr.Delete(r.Context(), workspace.DeleteOptions{
			ProjectName: req.ProjectName,
			KeepData:    req.KeepData,
			Yes:         req.Yes,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, out)
	}
}
