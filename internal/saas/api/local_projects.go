package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
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

type LocalProjectSolutionReviewInput struct {
	Workspace          string `json:"workspace"`
	EpisodeID          string `json:"episode_id"`
	Action             string `json:"action"`
	StepID             string `json:"step_id,omitempty"`
	Reason             string `json:"reason,omitempty"`
	ReasonClass        string `json:"reason_class,omitempty"`
	Summary            string `json:"summary,omitempty"`
	SuccessorEpisodeID string `json:"successor_episode_id,omitempty"`
	IdempotencyKey     string `json:"idempotency_key,omitempty"`
	Pinned             bool   `json:"pinned,omitempty"`
}

type LocalProjectSolutionStartInput struct {
	Workspace      string `json:"workspace"`
	SessionID      string `json:"session_id"`
	PrincipalID    string `json:"principal_id"`
	ClientID       string `json:"client_id"`
	GoalSummary    string `json:"goal_summary"`
	CapturePolicy  string `json:"capture_policy"`
	RetentionClass string `json:"retention_class"`
	IdempotencyKey string `json:"idempotency_key"`
}

type LocalProjectSolutionStepInput struct {
	Workspace        string  `json:"workspace"`
	PrincipalID      string  `json:"principal_id"`
	EpisodeID        string  `json:"episode_id"`
	Kind             string  `json:"kind"`
	Status           string  `json:"status"`
	Summary          string  `json:"summary"`
	RationaleSummary string  `json:"rationale_summary"`
	Source           string  `json:"source"`
	Sensitivity      string  `json:"sensitivity"`
	IdempotencyKey   string  `json:"idempotency_key"`
	Confidence       float64 `json:"confidence"`
}

type LocalProjectSolutionCheckpointInput struct {
	Workspace          string   `json:"workspace"`
	PrincipalID        string   `json:"principal_id"`
	EpisodeID          string   `json:"episode_id"`
	GoalSummary        string   `json:"goal_summary"`
	NextAction         string   `json:"next_action"`
	Sensitivity        string   `json:"sensitivity"`
	ExpectedGeneration int64    `json:"expected_generation"`
	Constraints        []string `json:"constraints"`
	CompletedItems     []string `json:"completed_items"`
	OpenQuestions      []string `json:"open_questions"`
	TTLSeconds         int64    `json:"ttl_seconds"`
}

type LocalProjectSolutionTransitionInput struct {
	Workspace       string `json:"workspace"`
	PrincipalID     string `json:"principal_id"`
	EpisodeID       string `json:"episode_id"`
	Status          string `json:"status"`
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion int64  `json:"expected_version"`
}

type LocalProjectSolutionHandoffInput struct {
	Workspace         string `json:"workspace"`
	PrincipalID       string `json:"principal_id"`
	EpisodeID         string `json:"episode_id"`
	TargetPrincipalID string `json:"target_principal_id"`
	TargetSessionID   string `json:"target_session_id"`
	IdempotencyKey    string `json:"idempotency_key"`
	ExpectedVersion   int64  `json:"expected_version"`
}

type LocalProjectSolutionFinalizeInput struct {
	Workspace       string `json:"workspace"`
	PrincipalID     string `json:"principal_id"`
	EpisodeID       string `json:"episode_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion int64  `json:"expected_version"`
}

type LocalProjectSolutionRecallInput struct {
	Workspace     string `json:"workspace"`
	PrincipalID   string `json:"principal_id"`
	SessionID     string `json:"session_id"`
	Task          string `json:"task"`
	TokenBudget   int    `json:"token_budget"`
	MaxCandidates int    `json:"max_candidates"`
}

type LocalProjectSolutionExportInput struct {
	Workspace   string `json:"workspace"`
	PrincipalID string `json:"principal_id"`
	EpisodeID   string `json:"episode_id"`
}

type LocalProjectSolutionExport struct {
	Detail       application.SolutionActivityDetail `json:"detail"`
	WorkingState *core.SolutionWorkingState         `json:"working_state,omitempty"`
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
	ListSolutionEpisodes(context.Context, string, int) ([]application.SolutionActivityEpisode, error)
	GetSolutionEpisode(context.Context, string, string) (application.SolutionActivityDetail, error)
	ReviewSolutionEpisode(context.Context, LocalProjectSolutionReviewInput) error
	StartSolutionEpisode(context.Context, LocalProjectSolutionStartInput) (core.SolutionEpisode, bool, error)
	AppendSolutionStep(context.Context, LocalProjectSolutionStepInput) (core.SolutionStep, bool, error)
	CheckpointSolutionEpisode(context.Context, LocalProjectSolutionCheckpointInput) (core.SolutionWorkingState, error)
	TransitionSolutionEpisode(context.Context, LocalProjectSolutionTransitionInput) (core.SolutionEpisode, error)
	HandoffSolutionEpisode(context.Context, LocalProjectSolutionHandoffInput) (core.SolutionEpisode, error)
	FinalizeSolutionEpisode(context.Context, LocalProjectSolutionFinalizeInput) (core.SolutionSummary, error)
	RecallSolutionPaths(context.Context, LocalProjectSolutionRecallInput) (engine.HowRecallResult, error)
	ExportSolutionEpisode(context.Context, LocalProjectSolutionExportInput) (LocalProjectSolutionExport, error)
}

func localProjectBoundary(capability string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		caller, ok := auth.FromContext(request.Context())
		if !ok || strings.TrimSpace(caller.SessionID) == "" || strings.TrimSpace(caller.CredentialID) != "" ||
			strings.TrimSpace(caller.SubjectID) == "" || strings.TrimSpace(caller.AccountID) == "" || strings.TrimSpace(caller.TenantID) == "" || caller.Role != "owner" || !caller.Can(capability) {
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
		if !valid || (mode != "recent" && mode != "pinned" && mode != "type" && mode != "ungrouped") || err != nil {
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

func listLocalProjectSolutions(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		workspaceName, valid := validLocalProjectWorkspace(request.URL.Query().Get("workspace"))
		if !valid {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution", "a registered workspace name is required")
			return
		}
		if episodeID := strings.TrimSpace(request.URL.Query().Get("episode_id")); episodeID != "" {
			if strings.ContainsAny(episodeID, "/\\") {
				writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution", "a valid episode ID is required")
				return
			}
			detail, err := service.GetSolutionEpisode(request.Context(), workspaceName, episodeID)
			if err != nil {
				writeError(response, http.StatusNotFound, requestID(request), "project_solution_not_found", "solution episode was not found in this workspace")
				return
			}
			writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"detail": detail})
			return
		}
		limit, _, err := localProjectPage(request.URL.Query().Get("limit"), "")
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution", "a valid limit is required")
			return
		}
		items, err := service.ListSolutionEpisodes(request.Context(), workspaceName, limit)
		if err != nil {
			writeError(response, http.StatusNotFound, requestID(request), "project_solution_not_found", "solution activity is unavailable for this workspace")
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"episodes": items})
	}
}

func reviewLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionReviewInput
		if err := decodeJSON(request, &body); err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		body.EpisodeID, body.Action = strings.TrimSpace(body.EpisodeID), strings.TrimSpace(body.Action)
		validAction := body.Action == "pin" || body.Action == "misleading" || body.Action == "redact" || body.Action == "correct" || body.Action == "supersede" || body.Action == "delete"
		if !valid || body.EpisodeID == "" || strings.ContainsAny(body.EpisodeID, "/\\") || !validAction {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_review", "a registered workspace, episode ID, and review action are required")
			return
		}
		body.Workspace = workspaceName
		err := service.ReviewSolutionEpisode(request.Context(), body)
		if err != nil {
			writeError(response, http.StatusNotFound, requestID(request), "project_solution_review_failed", "solution review could not be applied in this workspace")
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"reviewed": true})
	}
}

func validSolutionIdentity(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.ContainsAny(value, "/\\")
}

func solutionOperationError(response http.ResponseWriter, request *http.Request, err error) {
	writeError(response, http.StatusNotFound, requestID(request), "project_solution_operation_failed", "solution operation could not be applied in this workspace")
}

func startLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionStartInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.SessionID) || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.ClientID) || strings.TrimSpace(body.GoalSummary) == "" || strings.TrimSpace(body.IdempotencyKey) == "" {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_start", "registered workspace and bounded solution identity are required")
			return
		}
		body.Workspace, body.SessionID, body.PrincipalID, body.ClientID = workspaceName, strings.TrimSpace(body.SessionID), strings.TrimSpace(body.PrincipalID), strings.TrimSpace(body.ClientID)
		body.GoalSummary, body.CapturePolicy, body.RetentionClass, body.IdempotencyKey = strings.TrimSpace(body.GoalSummary), strings.TrimSpace(body.CapturePolicy), strings.TrimSpace(body.RetentionClass), strings.TrimSpace(body.IdempotencyKey)
		if body.CapturePolicy == "" {
			body.CapturePolicy = string(core.SolutionCaptureStructured)
		}
		if body.RetentionClass == "" {
			body.RetentionClass = string(core.SolutionRetentionStandard)
		}
		episode, deduplicated, err := service.StartSolutionEpisode(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"episode": episode, "deduplicated": deduplicated})
	}
}

func appendLocalProjectSolutionStep(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionStepInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.EpisodeID) || strings.TrimSpace(body.Summary) == "" || strings.TrimSpace(body.IdempotencyKey) == "" {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_step", "registered workspace, principal, episode, summary, and idempotency key are required")
			return
		}
		body.Workspace = workspaceName
		if strings.TrimSpace(body.Kind) == "" {
			body.Kind = string(core.SolutionStepAction)
		}
		if strings.TrimSpace(body.Status) == "" {
			body.Status = string(core.SolutionStepCompleted)
		}
		if strings.TrimSpace(body.Source) == "" {
			body.Source = "human"
		}
		if strings.TrimSpace(body.Sensitivity) == "" {
			body.Sensitivity = string(core.SolutionSensitivityInternal)
		}
		step, deduplicated, err := service.AppendSolutionStep(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"step": step, "deduplicated": deduplicated})
	}
}

func checkpointLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionCheckpointInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.EpisodeID) || strings.TrimSpace(body.GoalSummary) == "" || body.TTLSeconds < 0 || time.Duration(body.TTLSeconds)*time.Second > 7*24*time.Hour {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_checkpoint", "registered workspace, principal, episode, goal, and bounded TTL are required")
			return
		}
		body.Workspace = workspaceName
		state, err := service.CheckpointSolutionEpisode(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"working_state": state})
	}
}

func transitionLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionTransitionInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.EpisodeID) || body.ExpectedVersion < 1 || strings.TrimSpace(body.Status) == "" || strings.TrimSpace(body.IdempotencyKey) == "" {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_transition", "registered workspace and valid transition fields are required")
			return
		}
		body.Workspace = workspaceName
		episode, err := service.TransitionSolutionEpisode(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"episode": episode})
	}
}

func handoffLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionHandoffInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.EpisodeID) || !validSolutionIdentity(body.TargetPrincipalID) || !validSolutionIdentity(body.TargetSessionID) || body.ExpectedVersion < 1 || strings.TrimSpace(body.IdempotencyKey) == "" {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_handoff", "registered workspace and valid handoff identities are required")
			return
		}
		body.Workspace = workspaceName
		episode, err := service.HandoffSolutionEpisode(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"episode": episode})
	}
}

func finalizeLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionFinalizeInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.EpisodeID) || body.ExpectedVersion < 1 || strings.TrimSpace(body.IdempotencyKey) == "" {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_finalize", "registered workspace and valid finalization fields are required")
			return
		}
		body.Workspace = workspaceName
		summary, err := service.FinalizeSolutionEpisode(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"summary": summary})
	}
}

func recallLocalProjectSolutions(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionRecallInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || strings.TrimSpace(body.Task) == "" || (body.PrincipalID != "" && !validSolutionIdentity(body.PrincipalID)) || (body.SessionID != "" && !validSolutionIdentity(body.SessionID)) {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_recall", "registered workspace, task, and valid optional continuation identity are required")
			return
		}
		body.Workspace = workspaceName
		result, err := service.RecallSolutionPaths(request.Context(), body)
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), result)
	}
}

func exportLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		workspaceName, valid := validLocalProjectWorkspace(request.URL.Query().Get("workspace"))
		principalID, episodeID := strings.TrimSpace(request.URL.Query().Get("principal_id")), strings.TrimSpace(request.URL.Query().Get("episode_id"))
		if !valid || !validSolutionIdentity(principalID) || !validSolutionIdentity(episodeID) {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_export", "registered workspace, principal, and episode are required")
			return
		}
		result, err := service.ExportSolutionEpisode(request.Context(), LocalProjectSolutionExportInput{Workspace: workspaceName, PrincipalID: principalID, EpisodeID: episodeID})
		if err != nil {
			solutionOperationError(response, request, err)
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), result)
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
