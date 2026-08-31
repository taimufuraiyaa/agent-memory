package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type skillOrchestrationControlRequest struct {
	Action         string `json:"action"`
	Workspace      string `json:"workspace"`
	Environment    string `json:"environment"`
	Actor          string `json:"actor"`
	WorkflowID     string `json:"workflow_id"`
	JobID          string `json:"job_id"`
	Generation     int64  `json:"expected_generation"`
	ReasonCode     string `json:"reason_code"`
	IdempotencyKey string `json:"idempotency_key"`
	Limit          int    `json:"limit"`
}

func skillOrchestrationStatusHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace"))
		assets, err := svc.resolve(r.Context(), workspaceID)
		if err != nil {
			writeSkillOrchestrationError(w, err)
			return
		}
		if workspaceID == "" {
			workspaceID = svc.Workspace
		}
		workflowID := strings.TrimSpace(r.URL.Query().Get("workflow_id"))
		skillID := strings.TrimSpace(r.URL.Query().Get("skill_id"))
		if (workflowID == "") == (skillID == "") {
			writeErr(w, http.StatusBadRequest, "validation", "exactly one workflow_id or skill_id is required")
			return
		}
		actor := strings.TrimSpace(r.URL.Query().Get("actor"))
		authorizationTarget := workflowID
		if authorizationTarget == "" {
			authorizationTarget = skillID
		}
		if err := authorizeSkillMutation(r.Context(), svc, actor, workspaceID, "orchestration_status", authorizationTarget); err != nil {
			writeErr(w, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		if workflowID == "" && skillID != "" {
			workflow, lookupErr := assets.Store.GetLatestSkillWorkflow(r.Context(), skillOrchestrationScope(workspaceID, r.URL.Query().Get("environment")), skillID)
			if lookupErr != nil {
				writeSkillOrchestrationError(w, lookupErr)
				return
			}
			workflowID = workflow.ID
		}
		limit := parseSkillLimit(r.URL.Query().Get("limit"))
		scope := skillOrchestrationScope(workspaceID, r.URL.Query().Get("environment"))
		status, err := application.NewSkillOrchestrationControlService(assets.Store, nil).Status(r.Context(), scope, workflowID, r.URL.Query().Get("job_cursor"), r.URL.Query().Get("event_cursor"), limit)
		if err != nil {
			writeSkillOrchestrationError(w, err)
			return
		}
		writeOK(w, http.StatusOK, status)
	}
}

func skillOrchestrationControlHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		var request skillOrchestrationControlRequest
		if !decodeSolutionRequest(w, r, &request) {
			return
		}
		request.Action, request.Actor = strings.TrimSpace(request.Action), strings.TrimSpace(request.Actor)
		assets, err := svc.resolve(r.Context(), request.Workspace)
		if err != nil {
			writeSkillOrchestrationError(w, err)
			return
		}
		workspaceID := strings.TrimSpace(request.Workspace)
		if workspaceID == "" {
			workspaceID = svc.Workspace
		}
		target := request.WorkflowID
		if request.JobID != "" {
			target = request.JobID
		}
		if err := authorizeSkillMutation(r.Context(), svc, request.Actor, workspaceID, "orchestration_"+request.Action, target); err != nil {
			writeErr(w, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		scope := skillOrchestrationScope(workspaceID, request.Environment)
		controls := application.NewSkillOrchestrationControlService(assets.Store, nil)
		var result any
		switch request.Action {
		case "pause":
			result, err = controls.SetPaused(r.Context(), scope, request.WorkflowID, request.Generation, true, request.Actor)
		case "resume":
			result, err = controls.SetPaused(r.Context(), scope, request.WorkflowID, request.Generation, false, request.Actor)
		case "cancel":
			err = controls.Cancel(r.Context(), scope, request.JobID, request.Generation, request.Actor)
			result = map[string]any{"job_id": request.JobID, "cancel_requested": err == nil}
		case "retry":
			result, err = controls.Retry(r.Context(), scope, request.JobID, request.Generation, request.Actor)
		case "replay":
			var replayResult contracts.SkillSignalRouteResult
			replayResult, err = controls.Replay(r.Context(), application.SkillDeadLetterReplayRequest{Scope: scope, JobID: request.JobID, ActorID: request.Actor, Authorized: true, ReasonCode: request.ReasonCode, IdempotencyKey: request.IdempotencyKey})
			result = map[string]any{"workflow": replayResult.Workflow, "job": replayResult.Job, "created": replayResult.Created}
		case "reconcile":
			if request.Limit == 0 {
				request.Limit = 100
			}
			result, err = controls.Reconcile(r.Context(), scope, request.WorkflowID, request.Generation, request.Limit)
		case "drain":
			if svc.SkillOrchestrationDrainer != nil {
				err = svc.SkillOrchestrationDrainer.Drain(r.Context())
			}
			result = map[string]any{"drained": err == nil}
		default:
			err = errors.New("unsupported skill orchestration control")
		}
		if err != nil {
			writeSkillOrchestrationError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"action": request.Action, "result": result})
	}
}

func skillOrchestrationScope(workspaceID, environment string) core.SkillOrchestratorScope {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		environment = "local"
	}
	return core.SkillOrchestratorScope{WorkspaceID: workspaceID, Environment: environment}
}

func writeSkillOrchestrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "timeout", "skill orchestration control timed out")
	case errors.Is(err, sqlite.ErrSkillOrchestratorNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "skill orchestration target not found")
	case errors.Is(err, sqlite.ErrSkillOrchestratorGeneration), errors.Is(err, sqlite.ErrSkillOrchestratorConflict), errors.Is(err, sqlite.ErrSkillOrchestratorStaleLease), strings.Contains(err.Error(), "generation conflict"):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "validation", err.Error())
	}
}
