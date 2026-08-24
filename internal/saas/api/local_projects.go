package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type LocalProjectStudyInput struct {
	Workspace string `json:"workspace"`
	Depth     string `json:"depth"`
	DryRun    bool   `json:"dry_run"`
	MaxFiles  int    `json:"max_files"`
	Offset    int    `json:"offset"`
}

type LocalProjectFeedbackInput struct {
	Workspace   string `json:"workspace"`
	RequestID   string `json:"request_id"`
	Score       int    `json:"score"`
	Reason      string `json:"reason"`
	UsefulCount *int   `json:"useful_count,omitempty"`
	TotalCount  *int   `json:"total_count,omitempty"`
}

type LocalProjectSearchInput struct {
	Workspace string
	Query     string
	Limit     int
	Offset    int
}

type LocalProjectBrowseInput struct {
	Workspace string
	Mode      string
	Limit     int
	Offset    int
}

type LocalProjectMemoryResult struct {
	Memory      core.MemoryEntry `json:"memory"`
	Score       float64          `json:"score"`
	Explanation string           `json:"explanation,omitempty"`
}

type LocalProjectService interface {
	List(context.Context) ([]workspace.ListItem, error)
	Study(context.Context, LocalProjectStudyInput) (*engine.StudyResult, error)
	ListFeedback(context.Context, string) ([]core.RetrievalRequestLog, error)
	RecordFeedback(context.Context, LocalProjectFeedbackInput) error
	Search(context.Context, LocalProjectSearchInput) ([]LocalProjectMemoryResult, error)
	Browse(context.Context, LocalProjectBrowseInput) ([]core.MemoryEntry, error)
	GetMemory(context.Context, string, string) (*core.MemoryEntry, error)
}

func localProjectBoundary(capability string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		caller, ok := auth.FromContext(request.Context())
		if !ok || strings.TrimSpace(caller.SessionID) == "" || strings.TrimSpace(caller.CredentialID) != "" || !caller.Can(capability) {
			writeError(response, http.StatusForbidden, requestID(request), "browser_owner_required", "A browser owner session with the required capability is required.")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func validLocalProjectWorkspace(raw string) (string, bool) {
	workspaceName := strings.TrimSpace(raw)
	normalized, err := workspace.ValidateProjectName(workspaceName)
	return workspaceName, err == nil && normalized == workspaceName
}

func localProjectPage(rawLimit, rawCursor string) (int, int, error) {
	limit := 20
	if strings.TrimSpace(rawLimit) != "" {
		value, err := strconv.Atoi(rawLimit)
		if err != nil || value < 1 || value > 100 {
			return 0, 0, strconv.ErrSyntax
		}
		limit = value
	}
	offset := 0
	if strings.TrimSpace(rawCursor) != "" {
		value, err := strconv.Atoi(rawCursor)
		if err != nil || value < 0 || value > engine.MaxStudyOffset {
			return 0, 0, strconv.ErrSyntax
		}
		offset = value
	}
	return limit, offset, nil
}

func searchLocalProject(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body struct {
			Workspace string `json:"workspace"`
			Query     string `json:"query"`
			Limit     int    `json:"limit"`
			Cursor    string `json:"cursor"`
		}
		if err := decodeJSON(request, &body); err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		query := strings.TrimSpace(body.Query)
		limit, offset, err := localProjectPage(strconv.Itoa(body.Limit), body.Cursor)
		if body.Limit == 0 {
			limit, offset, err = localProjectPage("", body.Cursor)
		}
		if !valid || query == "" || err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_search", "a registered workspace name, query, limit, and cursor are required")
			return
		}
		items, err := service.Search(request.Context(), LocalProjectSearchInput{Workspace: workspaceName, Query: query, Limit: limit, Offset: offset})
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_search_failed", err.Error())
			return
		}
		nextCursor := ""
		if len(items) == limit {
			nextCursor = strconv.Itoa(offset + len(items))
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"items": items, "next_cursor": nextCursor})
	}
}

func browseLocalProject(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		workspaceName, valid := validLocalProjectWorkspace(request.URL.Query().Get("workspace"))
		mode := strings.TrimSpace(request.URL.Query().Get("mode"))
		if mode == "" {
			mode = "recent"
		}
		limit, offset, err := localProjectPage(request.URL.Query().Get("limit"), request.URL.Query().Get("cursor"))
		if !valid || (mode != "recent" && mode != "pinned" && mode != "type") || err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_browse", "a registered workspace name, browse mode, limit, and cursor are required")
			return
		}
		items, err := service.Browse(request.Context(), LocalProjectBrowseInput{Workspace: workspaceName, Mode: mode, Limit: limit, Offset: offset})
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_browse_failed", err.Error())
			return
		}
		nextCursor := ""
		if len(items) == limit {
			nextCursor = strconv.Itoa(offset + len(items))
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"items": items, "next_cursor": nextCursor})
	}
}

func detailLocalProjectMemory(service LocalProjectService) func(http.ResponseWriter, *http.Request, string) {
	return func(response http.ResponseWriter, request *http.Request, memoryID string) {
		workspaceName, valid := validLocalProjectWorkspace(request.URL.Query().Get("workspace"))
		memoryID = strings.TrimSpace(memoryID)
		if !valid || memoryID == "" || strings.ContainsAny(memoryID, "/\\") {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_memory", "a registered workspace name and memory ID are required")
			return
		}
		memory, err := service.GetMemory(request.Context(), workspaceName, memoryID)
		if err != nil || memory == nil || memory.Workspace != workspaceName {
			writeError(response, http.StatusNotFound, requestID(request), "project_memory_not_found", "memory was not found in this workspace")
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), memory)
	}
}

func getLocalProjectMemory(service LocalProjectService) http.HandlerFunc {
	detail := detailLocalProjectMemory(service)
	return func(response http.ResponseWriter, request *http.Request) {
		detail(response, request, request.PathValue("memory_id"))
	}
}

func listLocalProjectFeedback(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		workspaceName := strings.TrimSpace(request.URL.Query().Get("workspace"))
		if workspaceName == "" {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_feedback", "workspace is required")
			return
		}
		feedback, err := service.ListFeedback(request.Context(), workspaceName)
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_feedback_unavailable", err.Error())
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"feedback": feedback})
	}
}

func recordLocalProjectFeedback(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var input LocalProjectFeedbackInput
		if err := decodeJSON(request, &input); err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		input.Workspace = strings.TrimSpace(input.Workspace)
		input.RequestID = strings.TrimSpace(input.RequestID)
		input.Reason = strings.TrimSpace(input.Reason)
		invalidCounts := (input.UsefulCount != nil && *input.UsefulCount < 0) || (input.TotalCount != nil && *input.TotalCount < 0) || (input.UsefulCount != nil && input.TotalCount != nil && *input.UsefulCount > *input.TotalCount)
		if input.Workspace == "" || input.RequestID == "" || input.Score < 0 || input.Score > 5 || (input.Score < 4 && input.Reason == "") || invalidCounts {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_feedback", "workspace, request_id, a valid score, required reason, and valid hit counts are required")
			return
		}
		if err := service.RecordFeedback(request.Context(), input); err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_feedback_failed", err.Error())
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), input)
	}
}

func listLocalProjects(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projects, err := service.List(request.Context())
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "local_projects_unavailable", err.Error())
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"projects": projects})
	}
}

func studyLocalProject(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var input LocalProjectStudyInput
		if err := decodeJSON(request, &input); err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		input.Workspace = strings.TrimSpace(input.Workspace)
		input.Depth = strings.TrimSpace(input.Depth)
		if input.Workspace == "" || (input.Depth != "shallow" && input.Depth != "medium" && input.Depth != "deep") || input.MaxFiles < 1 || input.MaxFiles > engine.DefaultMaxFiles || input.Offset < 0 || input.Offset > engine.MaxStudyOffset {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_study", "workspace, scan depth, safe max_files, and a valid offset are required")
			return
		}
		result, err := service.Study(request.Context(), input)
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_study_failed", err.Error())
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), result)
	}
}
