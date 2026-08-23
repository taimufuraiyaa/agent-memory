package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
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
		if err := svc.evictWorkspace(req.From); err != nil {
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

// projectsStudyHandler implements POST /api/v1/projects/study.
// Sources are always derived from the registered workspace root; browser
// callers cannot extend the scan to arbitrary host paths.
func projectsStudyHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			Workspace string `json:"workspace"`
			Depth     string `json:"depth"`
			DryRun    bool   `json:"dry_run"`
			MaxFiles  int    `json:"max_files"`
			Offset    int    `json:"offset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		req.Workspace = strings.TrimSpace(req.Workspace)
		req.Depth = strings.TrimSpace(req.Depth)
		if req.Workspace == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "workspace is required")
			return
		}
		if req.Depth != "shallow" && req.Depth != "medium" && req.Depth != "deep" {
			writeErr(w, http.StatusBadRequest, "bad_request", "depth must be shallow, medium, or deep")
			return
		}
		if req.MaxFiles <= 0 || req.MaxFiles > engine.DefaultMaxFiles {
			writeErr(w, http.StatusBadRequest, "bad_request", "max_files must be between 1 and the safe study limit")
			return
		}
		if req.Offset < 0 || req.Offset > engine.MaxStudyOffset {
			writeErr(w, http.StatusBadRequest, "bad_request", "offset must be between 0 and the safe study offset limit")
			return
		}

		mgr, err := workspace.NewManager(svc.BaseDir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		project, err := mgr.Project(req.Workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		if strings.TrimSpace(project.WorkspaceRoot) == "" {
			writeErr(w, http.StatusBadRequest, "runtime", "project has no registered root; re-register it before studying")
			return
		}
		assets, err := svc.resolve(r.Context(), req.Workspace)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		result, err := engine.NewStudyEngine(assets.Writer).IngestWithOptions(r.Context(), engine.StudyOptions{
			Workspace: req.Workspace,
			Sources:   workspace.DefaultStudySources(project.WorkspaceRoot),
			Depth:     req.Depth,
			DryRun:    req.DryRun,
			MaxFiles:  req.MaxFiles,
			Offset:    req.Offset,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, "runtime", err.Error())
			return
		}
		writeOK(w, http.StatusOK, result)
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
		if err := svc.evictWorkspace(req.ProjectName); err != nil {
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
