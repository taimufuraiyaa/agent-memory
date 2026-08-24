package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
)

const solutionRequestLimit = 256 << 10

type solutionStartRequest struct {
	Workspace      string                      `json:"workspace"`
	SessionID      string                      `json:"session_id"`
	PrincipalID    string                      `json:"principal_id"`
	ClientID       string                      `json:"client_id"`
	GoalSummary    string                      `json:"goal_summary"`
	CapturePolicy  core.SolutionCapturePolicy  `json:"capture_policy"`
	RetentionClass core.SolutionRetentionClass `json:"retention_class"`
	IdempotencyKey string                      `json:"idempotency_key"`
}

type solutionStepRequest struct {
	Workspace        string                   `json:"workspace"`
	PrincipalID      string                   `json:"principal_id"`
	EpisodeID        string                   `json:"episode_id"`
	Kind             core.SolutionStepKind    `json:"kind"`
	Status           core.SolutionStepStatus  `json:"status"`
	Summary          string                   `json:"summary"`
	RationaleSummary string                   `json:"rationale_summary"`
	Source           string                   `json:"source"`
	ParentStepIDs    []string                 `json:"parent_step_ids"`
	References       []core.SolutionReference `json:"references"`
	Confidence       float64                  `json:"confidence"`
	Sensitivity      core.SolutionSensitivity `json:"sensitivity"`
	IdempotencyKey   string                   `json:"idempotency_key"`
}

type solutionCheckpointRequest struct {
	Workspace          string                   `json:"workspace"`
	PrincipalID        string                   `json:"principal_id"`
	EpisodeID          string                   `json:"episode_id"`
	ExpectedGeneration int64                    `json:"expected_generation"`
	GoalSummary        string                   `json:"goal_summary"`
	Constraints        []string                 `json:"constraints"`
	PlanItems          []core.SolutionPlanItem  `json:"plan_items"`
	CompletedItems     []string                 `json:"completed_items"`
	OpenQuestions      []string                 `json:"open_questions"`
	NextAction         string                   `json:"next_action"`
	Artifacts          []core.SolutionReference `json:"artifacts"`
	Sensitivity        core.SolutionSensitivity `json:"sensitivity"`
	TTLSeconds         int64                    `json:"ttl_seconds"`
}

type solutionTransitionRequest struct {
	Workspace       string                     `json:"workspace"`
	PrincipalID     string                     `json:"principal_id"`
	EpisodeID       string                     `json:"episode_id"`
	ExpectedVersion int64                      `json:"expected_version"`
	Status          core.SolutionEpisodeStatus `json:"status"`
	IdempotencyKey  string                     `json:"idempotency_key"`
}

type solutionHandoffRequest struct {
	Workspace         string `json:"workspace"`
	PrincipalID       string `json:"principal_id"`
	EpisodeID         string `json:"episode_id"`
	ExpectedVersion   int64  `json:"expected_version"`
	TargetPrincipalID string `json:"target_principal_id"`
	TargetSessionID   string `json:"target_session_id"`
	IdempotencyKey    string `json:"idempotency_key"`
}

type solutionReviewRequest struct {
	Workspace          string `json:"workspace"`
	PrincipalID        string `json:"principal_id"`
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

func solutionStartHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req solutionStartRequest
		if !decodeSolutionRequest(w, r, &req) {
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, req.Workspace)
		if !ok {
			return
		}
		episode, deduplicated, err := service.Start(r.Context(), application.SolutionStartInput{
			Workspace: ws, SessionID: req.SessionID, PrincipalID: req.PrincipalID, ClientID: req.ClientID,
			GoalSummary: req.GoalSummary, CapturePolicy: req.CapturePolicy, RetentionClass: req.RetentionClass,
			IdempotencyKey: req.IdempotencyKey, Origin: engine.SolutionOriginAgent,
		})
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"episode": episode, "deduplicated": deduplicated})
	}
}

func solutionStepHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req solutionStepRequest
		if !decodeSolutionRequest(w, r, &req) {
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, req.Workspace)
		if !ok {
			return
		}
		step, deduplicated, err := service.AppendStep(r.Context(), application.SolutionAppendStepInput{
			Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, Kind: req.Kind, Status: req.Status,
			Summary: req.Summary, RationaleSummary: req.RationaleSummary, Source: req.Source,
			ParentStepIDs: req.ParentStepIDs, References: req.References, Confidence: req.Confidence,
			Sensitivity: req.Sensitivity, IdempotencyKey: req.IdempotencyKey, Origin: engine.SolutionOriginAgent,
		})
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"step": step, "deduplicated": deduplicated})
	}
}

func solutionCheckpointHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req solutionCheckpointRequest
		if !decodeSolutionRequest(w, r, &req) {
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, req.Workspace)
		if !ok {
			return
		}
		state, err := service.Checkpoint(r.Context(), application.SolutionCheckpointInput{
			Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, ExpectedGeneration: req.ExpectedGeneration,
			GoalSummary: req.GoalSummary, Constraints: req.Constraints, PlanItems: req.PlanItems,
			CompletedItems: req.CompletedItems, OpenQuestions: req.OpenQuestions, NextAction: req.NextAction,
			Artifacts: req.Artifacts, Sensitivity: req.Sensitivity, TTL: time.Duration(req.TTLSeconds) * time.Second,
			Origin: engine.SolutionOriginAgent,
		})
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"working_state": state})
	}
}

func solutionStateHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, r.URL.Query().Get("workspace"))
		if !ok {
			return
		}
		state, err := service.GetWorkingState(r.Context(), ws, r.URL.Query().Get("principal_id"), r.URL.Query().Get("episode_id"))
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"working_state": state})
	}
}

func solutionTransitionHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req solutionTransitionRequest
		if !decodeSolutionRequest(w, r, &req) {
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, req.Workspace)
		if !ok {
			return
		}
		episode, err := service.Transition(r.Context(), application.SolutionTransitionInput{
			Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, ExpectedVersion: req.ExpectedVersion,
			Status: req.Status, IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"episode": episode})
	}
}

func solutionHandoffHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req solutionHandoffRequest
		if !decodeSolutionRequest(w, r, &req) {
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, req.Workspace)
		if !ok {
			return
		}
		episode, err := service.Handoff(r.Context(), application.SolutionHandoffInput{
			Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, ExpectedVersion: req.ExpectedVersion,
			TargetPrincipalID: req.TargetPrincipalID, TargetSessionID: req.TargetSessionID, IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"episode": episode})
	}
}

func solutionActivityHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, r.URL.Query().Get("workspace"))
		if !ok {
			return
		}
		if episodeID := strings.TrimSpace(r.URL.Query().Get("episode_id")); episodeID != "" {
			detail, err := service.GetActivityEpisode(r.Context(), ws, episodeID)
			if err != nil {
				writeSolutionError(w, err)
				return
			}
			writeOK(w, http.StatusOK, map[string]any{"detail": detail})
			return
		}
		limit := 20
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			var err error
			if limit, err = strconv.Atoi(raw); err != nil {
				writeErr(w, 400, "bad_request", "invalid activity limit")
				return
			}
		}
		items, err := service.ListActivityEpisodes(r.Context(), ws, limit)
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"episodes": items})
	}
}

func solutionReviewHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req solutionReviewRequest
		if !decodeSolutionRequest(w, r, &req) {
			return
		}
		ws, service, ok := resolveSolutionService(w, r, svc, req.Workspace)
		if !ok {
			return
		}
		var result any
		var err error
		switch strings.TrimSpace(req.Action) {
		case "pin":
			err = service.SetEpisodePinned(r.Context(), application.SolutionEpisodePinInput{Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, Pinned: req.Pinned})
		case "misleading":
			err = service.MarkStepMisleading(r.Context(), application.SolutionStepReviewInput{Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, StepID: req.StepID, Reason: req.Reason})
		case "redact":
			err = service.RedactStep(r.Context(), application.SolutionStepRedactInput{Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, StepID: req.StepID, ReasonClass: req.ReasonClass})
		case "correct":
			result, err = service.CorrectSummary(r.Context(), application.SolutionSummaryCorrectionInput{Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, Summary: req.Summary, IdempotencyKey: req.IdempotencyKey})
		case "supersede":
			err = service.SupersedeEpisode(r.Context(), application.SolutionEpisodeSupersedeInput{Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, SuccessorEpisodeID: req.SuccessorEpisodeID})
		case "delete":
			err = service.DeleteEpisode(r.Context(), application.SolutionEpisodeDeleteInput{Workspace: ws, PrincipalID: req.PrincipalID, EpisodeID: req.EpisodeID, Reason: req.Reason})
		default:
			err = errors.New("invalid solution review action")
		}
		if err != nil {
			writeSolutionError(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"reviewed": true, "result": result})
	}
}

func decodeSolutionRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, solutionRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return false
	}
	return true
}

func resolveSolutionService(w http.ResponseWriter, r *http.Request, svc *Service, requested string) (string, *application.SolutionService, bool) {
	ws := strings.TrimSpace(requested)
	if ws == "" {
		ws = workspaceFromRequest(r, svc.Workspace)
	}
	assets, err := svc.resolve(r.Context(), ws)
	if err != nil {
		writeWorkspaceResolveError(w, err)
		return "", nil, false
	}
	return ws, assets.Solutions, true
}

func writeSolutionError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, 404, "not_found", "solution state not found")
	case strings.Contains(message, "not authorized"):
		writeErr(w, 403, "forbidden", "solution episode not authorized")
	case strings.Contains(message, "conflict") || strings.Contains(message, "already exists"):
		writeErr(w, 409, "conflict", message)
	default:
		writeErr(w, 400, "validation", message)
	}
}
