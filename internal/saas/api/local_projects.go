package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
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

type LocalProjectSolutionPromotionTarget struct {
	MemoryType    string   `json:"memory_type"`
	Content       string   `json:"content,omitempty"`
	SourceStepIDs []string `json:"source_step_ids,omitempty"`
}

type LocalProjectSolutionPromoteInput struct {
	Workspace      string                                `json:"workspace"`
	PrincipalID    string                                `json:"principal_id"`
	EpisodeID      string                                `json:"episode_id"`
	SummaryID      string                                `json:"summary_id"`
	IdempotencyKey string                                `json:"idempotency_key"`
	Targets        []LocalProjectSolutionPromotionTarget `json:"targets"`
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

type LocalProjectLifecycleRun struct {
	ID             string    `json:"id"`
	Workspace      string    `json:"workspace"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	Trigger        string    `json:"trigger"`
	Result         string    `json:"result"`
	SkipReason     string    `json:"skip_reason,omitempty"`
	DurationMS     int       `json:"duration_ms"`
	DecayUpdated   int       `json:"decay_updated"`
	Consolidated   int       `json:"consolidated"`
	ConflictsFound int       `json:"conflicts_found"`
	Evicted        int       `json:"evicted"`
	Promoted       int       `json:"promoted"`
	Demoted        int       `json:"demoted"`
	Error          string    `json:"error,omitempty"`
}

type LocalProjectLifecycle struct {
	History []LocalProjectLifecycleRun `json:"history"`
}

type LocalProjectSkill struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Path        string `json:"path"`
}

type LocalProjectSystemService interface {
	Lifecycle(context.Context, string, int) (LocalProjectLifecycle, error)
	Skills(context.Context, string) ([]LocalProjectSkill, error)
}

type LocalProjectSkillLifecycleView struct {
	Skill           core.LogicalSkill          `json:"skill"`
	Revisions       []core.SkillRevision       `json:"revisions"`
	Evaluations     []core.SkillEvaluationRun  `json:"evaluations"`
	PolicyDecisions []core.SkillPolicyDecision `json:"policy_decisions"`
	Activation      *core.SkillActivation      `json:"activation,omitempty"`
}
type LocalProjectSkillLifecycleInput struct {
	Workspace string          `json:"workspace"`
	TenantID  string          `json:"-"`
	AccountID string          `json:"-"`
	Actor     string          `json:"-"`
	Operation string          `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}
type LocalProjectSkillLifecycleService interface {
	ListSkillLifecycle(context.Context, string) ([]core.LogicalSkill, error)
	InspectSkillLifecycle(context.Context, string, string, string) (LocalProjectSkillLifecycleView, error)
	OperateSkillLifecycle(context.Context, LocalProjectSkillLifecycleInput) (any, error)
}

type LocalProjectSkillOrchestrationStatusInput struct {
	Workspace   string
	Environment string
	WorkflowID  string
	SkillID     string
	JobCursor   string
	EventCursor string
	Limit       int
	TenantID    string
	AccountID   string
	Actor       string
}

type LocalProjectSkillOrchestrationControlInput struct {
	Workspace      string `json:"workspace"`
	Environment    string `json:"environment"`
	Action         string `json:"action"`
	WorkflowID     string `json:"workflow_id"`
	JobID          string `json:"job_id"`
	Generation     int64  `json:"expected_generation"`
	ReasonCode     string `json:"reason_code"`
	IdempotencyKey string `json:"idempotency_key"`
	Limit          int    `json:"limit"`
	TenantID       string `json:"-"`
	AccountID      string `json:"-"`
	Actor          string `json:"-"`
}

type LocalProjectSkillOrchestrationService interface {
	StatusSkillOrchestration(context.Context, LocalProjectSkillOrchestrationStatusInput) (application.SkillOrchestrationStatus, error)
	ControlSkillOrchestration(context.Context, LocalProjectSkillOrchestrationControlInput) (any, error)
}

type LocalClientProfileService interface {
	ListClientProfiles(context.Context) ([]clientprofile.Profile, error)
	CreateClientProfile(context.Context, clientprofile.Input) (clientprofile.Profile, error)
	UpdateClientProfile(context.Context, string, int64, clientprofile.Input) (clientprofile.Profile, error)
	DeleteClientProfile(context.Context, string, int64) error
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
	PromoteSolutionEpisode(context.Context, LocalProjectSolutionPromoteInput) (application.SolutionPromotionResult, error)
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

func localProjectOwnerBoundary(owner LocalOwnerService, capability string, next http.Handler) http.Handler {
	bound := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		caller, _ := auth.FromContext(request.Context())
		status, err := owner.Status(request.Context())
		if err != nil || status.State != "authenticated" || caller.TenantID != status.Account.TenantID || caller.AccountID != status.Account.AccountID {
			writeError(response, http.StatusForbidden, requestID(request), "local_owner_scope_required", "The session does not own this local project registry.")
			return
		}
		next.ServeHTTP(response, request)
	})
	return localProjectBoundary(capability, bound)
}

func localProjectSkillLifecycle(service LocalProjectSkillLifecycleService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		caller, ok := auth.FromContext(request.Context())
		if !ok {
			writeError(response, http.StatusForbidden, requestID(request), "browser_owner_required", "A browser owner session is required.")
			return
		}
		switch request.Method {
		case http.MethodGet:
			workspaceName := strings.TrimSpace(request.URL.Query().Get("workspace"))
			if _, valid := validLocalProjectWorkspace(workspaceName); !valid {
				writeError(response, 400, requestID(request), "invalid_workspace", "A registered workspace name is required.")
				return
			}
			skillID := strings.TrimSpace(request.URL.Query().Get("skill_id"))
			if skillID == "" {
				items, err := service.ListSkillLifecycle(request.Context(), workspaceName)
				if err != nil {
					writeError(response, 400, requestID(request), "skill_lifecycle_unavailable", err.Error())
					return
				}
				writeSuccess(response, 200, requestID(request), map[string]any{"skills": items})
				return
			}
			environment := strings.TrimSpace(request.URL.Query().Get("environment"))
			if environment == "" {
				environment = "local"
			}
			view, err := service.InspectSkillLifecycle(request.Context(), workspaceName, skillID, environment)
			if err != nil {
				writeError(response, 400, requestID(request), "skill_lifecycle_unavailable", err.Error())
				return
			}
			writeSuccess(response, 200, requestID(request), view)
		case http.MethodPost:
			var input LocalProjectSkillLifecycleInput
			if decodeJSON(request, &input) != nil {
				writeError(response, 400, requestID(request), "invalid_request", "request body is invalid")
				return
			}
			input.Workspace = strings.TrimSpace(input.Workspace)
			input.Operation = strings.TrimSpace(input.Operation)
			started := time.Now()
			outcome := "failure"
			defer func() {
				observability.DefaultSkillLifecycleMetrics().Observe(observability.SkillLifecycleObservation{Event: input.Operation, Outcome: outcome, Duration: time.Since(started)})
			}()
			if _, valid := validLocalProjectWorkspace(input.Workspace); !valid || !validLocalSkillOperation(input.Operation) || unsafeLocalSkillPayload(input.Payload) {
				writeError(response, 400, requestID(request), "invalid_skill_lifecycle", "workspace, operation, or payload is invalid")
				return
			}
			input.Actor, input.AccountID, input.TenantID = caller.SubjectID, caller.AccountID, caller.TenantID
			result, err := service.OperateSkillLifecycle(request.Context(), input)
			if err != nil {
				writeError(response, 400, requestID(request), "skill_lifecycle_failed", err.Error())
				return
			}
			outcome = "success"
			writeSuccess(response, 200, requestID(request), map[string]any{"operation": input.Operation, "result": result})
		default:
			writeError(response, 405, requestID(request), "method_not_allowed", "method not allowed")
		}
	}
}

func localProjectSkillOrchestrationStatus(service LocalProjectSkillOrchestrationService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writeError(response, http.StatusMethodNotAllowed, requestID(request), "method_not_allowed", "method not allowed")
			return
		}
		caller, ok := auth.FromContext(request.Context())
		if !ok {
			writeError(response, http.StatusForbidden, requestID(request), "browser_owner_required", "A browser owner session is required.")
			return
		}
		input := LocalProjectSkillOrchestrationStatusInput{
			Workspace: strings.TrimSpace(request.URL.Query().Get("workspace")), Environment: strings.TrimSpace(request.URL.Query().Get("environment")),
			WorkflowID: strings.TrimSpace(request.URL.Query().Get("workflow_id")), SkillID: strings.TrimSpace(request.URL.Query().Get("skill_id")), JobCursor: request.URL.Query().Get("job_cursor"),
			EventCursor: request.URL.Query().Get("event_cursor"), Limit: boundedLocalSkillLimit(request.URL.Query().Get("limit")),
			TenantID: caller.TenantID, AccountID: caller.AccountID, Actor: caller.SubjectID,
		}
		if !validLocalOrchestrationScope(input.Workspace, input.Environment, input.WorkflowID, input.SkillID) {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_skill_orchestration", "workspace, environment, or workflow is invalid")
			return
		}
		status, err := service.StatusSkillOrchestration(request.Context(), input)
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "skill_orchestration_unavailable", err.Error())
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), status)
	}
}

func localProjectSkillOrchestrationControl(service LocalProjectSkillOrchestrationService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writeError(response, http.StatusMethodNotAllowed, requestID(request), "method_not_allowed", "method not allowed")
			return
		}
		caller, ok := auth.FromContext(request.Context())
		if !ok {
			writeError(response, http.StatusForbidden, requestID(request), "browser_owner_required", "A browser owner session is required.")
			return
		}
		var input LocalProjectSkillOrchestrationControlInput
		if decodeJSON(request, &input) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		input.Workspace, input.Environment, input.Action = strings.TrimSpace(input.Workspace), strings.TrimSpace(input.Environment), strings.TrimSpace(input.Action)
		input.WorkflowID, input.JobID = strings.TrimSpace(input.WorkflowID), strings.TrimSpace(input.JobID)
		if !validLocalOrchestrationControl(input) {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_skill_orchestration", "workspace or control target is invalid")
			return
		}
		input.TenantID, input.AccountID, input.Actor = caller.TenantID, caller.AccountID, caller.SubjectID
		result, err := service.ControlSkillOrchestration(request.Context(), input)
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "skill_orchestration_failed", err.Error())
			return
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"action": input.Action, "result": result})
	}
}

func validLocalOrchestrationScope(workspaceName, environment, workflowID, skillID string) bool {
	if _, valid := validLocalProjectWorkspace(workspaceName); !valid {
		return false
	}
	return validLocalOrchestrationID(environment, true) && ((validLocalOrchestrationID(workflowID, false) && skillID == "") || (validLocalOrchestrationID(skillID, false) && workflowID == ""))
}

func validLocalOrchestrationControl(input LocalProjectSkillOrchestrationControlInput) bool {
	if _, valid := validLocalProjectWorkspace(input.Workspace); !valid || !validLocalOrchestrationID(input.Environment, true) {
		return false
	}
	switch input.Action {
	case "pause", "resume", "reconcile":
		return validLocalOrchestrationID(input.WorkflowID, false)
	case "cancel", "retry":
		return validLocalOrchestrationID(input.JobID, false)
	case "replay":
		return validLocalOrchestrationID(input.JobID, false) && strings.TrimSpace(input.ReasonCode) != "" && strings.TrimSpace(input.IdempotencyKey) != ""
	case "drain":
		return true
	default:
		return false
	}
}

func validLocalOrchestrationID(value string, optional bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return optional
	}
	return len(value) <= 128 && !strings.Contains(value, "..") && !strings.ContainsAny(value, `/\\`)
}

func boundedLocalSkillLimit(raw string) int {
	limit := 20
	if raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 20
		}
		limit = parsed
	}
	if limit < 1 {
		return 20
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func validLocalSkillOperation(value string) bool {
	switch value {
	case "propose", "evaluate", "approve", "canary", "promote", "resolve", "acknowledge", "complete", "disable", "pin", "rollback", "migration-verify":
		return true
	}
	return false
}
func unsafeLocalSkillPayload(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return true
	}
	var inspect func(any) bool
	inspect = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, item := range typed {
				normalized := strings.ToLower(strings.TrimSpace(key))
				if normalized == "path" || normalized == "project_root" || normalized == "db_path" || normalized == "workspace" || normalized == "tenant_id" || normalized == "account_id" {
					return true
				}
				if inspect(item) {
					return true
				}
			}
		case []any:
			for _, item := range typed {
				if inspect(item) {
					return true
				}
			}
		}
		return false
	}
	return inspect(value)
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

func promoteLocalProjectSolution(service LocalProjectService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		var body LocalProjectSolutionPromoteInput
		if decodeJSON(request, &body) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_request", "request body is invalid")
			return
		}
		workspaceName, valid := validLocalProjectWorkspace(body.Workspace)
		if !valid || !validSolutionIdentity(body.PrincipalID) || !validSolutionIdentity(body.EpisodeID) || !validSolutionIdentity(body.SummaryID) || strings.TrimSpace(body.IdempotencyKey) == "" || len(body.Targets) < 1 || len(body.Targets) > 8 {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_solution_promotion", "registered workspace and valid promotion fields are required")
			return
		}
		body.Workspace = workspaceName
		result, err := service.PromoteSolutionEpisode(request.Context(), body)
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
		if feedback == nil {
			feedback = []core.RetrievalRequestLog{}
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

func listLocalProjectLifecycle(service LocalProjectSystemService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		workspaceName, valid := validLocalProjectWorkspace(request.URL.Query().Get("workspace"))
		limit, err := strconv.Atoi(request.URL.Query().Get("limit"))
		if request.URL.Query().Get("limit") == "" {
			limit, err = 100, nil
		}
		if !valid || err != nil || limit < 1 || limit > 200 {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_lifecycle", "a registered workspace and limit from 1 to 200 are required")
			return
		}
		result, err := service.Lifecycle(request.Context(), workspaceName, limit)
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_lifecycle_unavailable", "Lifecycle history is unavailable for this registered project.")
			return
		}
		if result.History == nil {
			result.History = []LocalProjectLifecycleRun{}
		}
		writeSuccess(response, http.StatusOK, requestID(request), result)
	}
}

func listLocalProjectSkills(service LocalProjectSystemService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		workspaceName, valid := validLocalProjectWorkspace(request.URL.Query().Get("workspace"))
		if !valid {
			writeError(response, http.StatusBadRequest, requestID(request), "invalid_project_skills", "a registered workspace is required")
			return
		}
		skills, err := service.Skills(request.Context(), workspaceName)
		if err != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "project_skills_unavailable", "Skills are unavailable for this registered project.")
			return
		}
		if skills == nil {
			skills = []LocalProjectSkill{}
		}
		writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"skills": skills})
	}
}

func localClientProfiles(service LocalClientProfileService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			profiles, err := service.ListClientProfiles(request.Context())
			if err != nil {
				writeLocalClientProfileError(response, request, err)
				return
			}
			if profiles == nil {
				profiles = []clientprofile.Profile{}
			}
			writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"profiles": profiles})
		case http.MethodPost:
			var input clientprofile.Input
			if decodeJSON(request, &input) != nil {
				writeError(response, http.StatusBadRequest, requestID(request), "client_profile_validation", "invalid client profile request")
				return
			}
			profile, err := service.CreateClientProfile(request.Context(), input)
			if err != nil {
				writeLocalClientProfileError(response, request, err)
				return
			}
			writeSuccess(response, http.StatusCreated, requestID(request), map[string]any{"profile": profile})
		default:
			writeError(response, http.StatusMethodNotAllowed, requestID(request), "method_not_allowed", "method not allowed")
		}
	}
}

func localClientProfile(service LocalClientProfileService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		id := strings.TrimSpace(request.PathValue("client_id"))
		if clientprofile.ValidateID(id) != nil {
			writeError(response, http.StatusBadRequest, requestID(request), "client_profile_validation", "client profile id is invalid")
			return
		}
		switch request.Method {
		case http.MethodPut:
			var input struct {
				DisplayName      string `json:"display_name"`
				ClientKind       string `json:"client_kind"`
				ToolProfile      string `json:"tool_profile"`
				ExpectedRevision int64  `json:"expected_revision"`
			}
			if decodeJSON(request, &input) != nil {
				writeError(response, http.StatusBadRequest, requestID(request), "client_profile_validation", "invalid client profile request")
				return
			}
			profile, err := service.UpdateClientProfile(request.Context(), id, input.ExpectedRevision, clientprofile.Input{DisplayName: input.DisplayName, ClientKind: input.ClientKind, ToolProfile: input.ToolProfile})
			if err != nil {
				writeLocalClientProfileError(response, request, err)
				return
			}
			writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"profile": profile})
		case http.MethodDelete:
			revision, err := strconv.ParseInt(request.URL.Query().Get("expected_revision"), 10, 64)
			if err != nil || revision < 1 {
				writeError(response, http.StatusBadRequest, requestID(request), "client_profile_validation", "expected_revision must be a positive integer")
				return
			}
			if err := service.DeleteClientProfile(request.Context(), id, revision); err != nil {
				writeLocalClientProfileError(response, request, err)
				return
			}
			writeSuccess(response, http.StatusOK, requestID(request), map[string]any{"deleted": true, "id": id})
		default:
			writeError(response, http.StatusMethodNotAllowed, requestID(request), "method_not_allowed", "method not allowed")
		}
	}
}

func writeLocalClientProfileError(response http.ResponseWriter, request *http.Request, err error) {
	status, code, message := http.StatusServiceUnavailable, "client_profiles_unavailable", "client profiles are temporarily unavailable"
	switch {
	case errors.Is(err, clientprofile.ErrValidation):
		status, code, message = http.StatusBadRequest, "client_profile_validation", err.Error()
	case errors.Is(err, clientprofile.ErrNotFound):
		status, code, message = http.StatusNotFound, "client_profile_not_found", "client profile was not found"
	case errors.Is(err, clientprofile.ErrConflict):
		status, code, message = http.StatusConflict, "client_profile_conflict", "client profile already exists"
	case errors.Is(err, clientprofile.ErrRevisionConflict):
		status, code, message = http.StatusConflict, "client_profile_revision_conflict", "client profile changed; reload and try again"
	}
	writeError(response, status, requestID(request), code, message)
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
