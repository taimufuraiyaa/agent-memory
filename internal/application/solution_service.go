package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

const (
	defaultSolutionWorkingStateTTL = 24 * time.Hour
	maxSolutionWorkingStateTTL     = 7 * 24 * time.Hour
)

type SolutionService struct {
	store     *sqlite.Store
	admission *engine.SolutionAdmissionPolicy
	now       func() time.Time
}

type SolutionServiceOption func(*SolutionService)

func WithSolutionClock(now func() time.Time) SolutionServiceOption {
	return func(service *SolutionService) {
		if now != nil {
			service.now = now
		}
	}
}

func NewSolutionService(store *sqlite.Store, admission *engine.SolutionAdmissionPolicy, options ...SolutionServiceOption) *SolutionService {
	if admission == nil {
		admission = engine.NewSolutionAdmissionPolicy()
	}
	service := &SolutionService{store: store, admission: admission, now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

type SolutionStartInput struct {
	Workspace      string
	SessionID      string
	PrincipalID    string
	ClientID       string
	GoalSummary    string
	CapturePolicy  core.SolutionCapturePolicy
	RetentionClass core.SolutionRetentionClass
	IdempotencyKey string
	Origin         engine.SolutionAdmissionOrigin
}

func (s *SolutionService) Start(ctx context.Context, input SolutionStartInput) (core.SolutionEpisode, bool, error) {
	origin := solutionOriginOrAgent(input.Origin)
	goal, err := s.admit(ctx, input.Workspace, input.SessionID, input.PrincipalID, input.ClientID,
		input.IdempotencyKey, origin, engine.SolutionFieldGoalSummary, input.GoalSummary)
	if err != nil {
		return core.SolutionEpisode{}, false, err
	}
	episode, deduplicated, err := s.store.CreateSolutionEpisode(ctx, sqlite.SolutionEpisodeInsert{
		Workspace: input.Workspace, SessionID: input.SessionID, PrincipalID: input.PrincipalID,
		ClientID: input.ClientID, GoalSummary: goal, CapturePolicy: input.CapturePolicy,
		RetentionClass: input.RetentionClass, IdempotencyKey: input.IdempotencyKey, CreatedAt: s.now().UTC(),
	})
	if err != nil {
		if active, activeErr := s.store.FindActiveSolutionEpisode(ctx, input.Workspace, input.SessionID, input.PrincipalID); activeErr == nil && active.ClientID == strings.TrimSpace(input.ClientID) {
			return core.SolutionEpisode{}, false, errors.New("an active episode already exists for this session and client")
		}
	}
	if err == nil && !deduplicated {
		s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, input.ClientID,
			input.IdempotencyKey, "solution_start", "success", "accepted", episode.ID, nil)
	}
	return episode, deduplicated, err
}

type SolutionAppendStepInput struct {
	Workspace        string
	PrincipalID      string
	EpisodeID        string
	Kind             core.SolutionStepKind
	Status           core.SolutionStepStatus
	Summary          string
	RationaleSummary string
	Source           string
	ParentStepIDs    []string
	References       []core.SolutionReference
	Confidence       float64
	Sensitivity      core.SolutionSensitivity
	IdempotencyKey   string
	Origin           engine.SolutionAdmissionOrigin
}

func (s *SolutionService) AppendStep(ctx context.Context, input SolutionAppendStepInput) (core.SolutionStep, bool, error) {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	origin := solutionOriginOrAgent(input.Origin)
	summary, err := s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID,
		input.IdempotencyKey, origin, engine.SolutionFieldStepSummary, input.Summary)
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	rationale := ""
	if strings.TrimSpace(input.RationaleSummary) != "" {
		rationale, err = s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID,
			input.IdempotencyKey, origin, engine.SolutionFieldRationaleSummary, input.RationaleSummary)
		if err != nil {
			return core.SolutionStep{}, false, err
		}
	}
	step, deduplicated, err := s.store.AppendSolutionStep(ctx, sqlite.SolutionStepInsert{
		EpisodeID: episode.ID, Kind: input.Kind, Status: input.Status, Summary: summary,
		RationaleSummary: rationale, Source: input.Source, ParentStepIDs: input.ParentStepIDs,
		References: input.References, Confidence: input.Confidence, Sensitivity: input.Sensitivity,
		IdempotencyKey: input.IdempotencyKey, CreatedAt: s.now().UTC(),
	})
	if err == nil && !deduplicated {
		s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID,
			input.IdempotencyKey, "solution_step_append", "success", "accepted", step.ID,
			map[string]any{"kind": step.Kind, "ordinal": step.Ordinal})
	}
	return step, deduplicated, err
}

type SolutionTransitionInput struct {
	Workspace       string
	PrincipalID     string
	EpisodeID       string
	ExpectedVersion int64
	Status          core.SolutionEpisodeStatus
	IdempotencyKey  string
}

func (s *SolutionService) Transition(ctx context.Context, input SolutionTransitionInput) (core.SolutionEpisode, error) {
	transition := sqlite.SolutionEpisodeTransition{
		EpisodeID: input.EpisodeID, Workspace: input.Workspace, PrincipalID: input.PrincipalID,
		ExpectedVersion: input.ExpectedVersion, Status: input.Status, IdempotencyKey: input.IdempotencyKey,
	}
	if replay, found, err := s.store.ReplaySolutionEpisodeTransition(ctx, transition); err != nil {
		return core.SolutionEpisode{}, err
	} else if found {
		return replay, nil
	}
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionEpisode{}, err
	}
	if episode.Version != input.ExpectedVersion {
		return core.SolutionEpisode{}, errors.New("solution episode version conflict")
	}
	if !validSolutionTransition(episode.Status, input.Status) {
		return core.SolutionEpisode{}, fmt.Errorf("invalid solution episode transition from %s to %s", episode.Status, input.Status)
	}
	transition.EpisodeID, transition.Workspace, transition.PrincipalID = episode.ID, episode.Workspace, episode.PrincipalID
	transition.UpdatedAt = s.now().UTC()
	updated, err := s.store.TransitionSolutionEpisode(ctx, transition)
	if errors.Is(err, sql.ErrNoRows) {
		return core.SolutionEpisode{}, errors.New("solution episode version conflict")
	}
	if err == nil {
		s.audit(ctx, updated.Workspace, updated.SessionID, updated.PrincipalID, updated.ClientID, "",
			"solution_transition", "success", string(input.Status), updated.ID,
			map[string]any{"from": episode.Status, "to": input.Status, "version": updated.Version})
	}
	return updated, err
}

type SolutionHandoffInput struct {
	Workspace         string
	PrincipalID       string
	EpisodeID         string
	ExpectedVersion   int64
	TargetPrincipalID string
	TargetSessionID   string
	IdempotencyKey    string
}

func (s *SolutionService) Handoff(ctx context.Context, input SolutionHandoffInput) (core.SolutionEpisode, error) {
	transition := sqlite.SolutionEpisodeTransition{
		EpisodeID: input.EpisodeID, Workspace: input.Workspace, PrincipalID: input.PrincipalID,
		ExpectedVersion: input.ExpectedVersion, Status: core.SolutionEpisodePaused,
		TargetPrincipalID: input.TargetPrincipalID, TargetSessionID: input.TargetSessionID,
		IdempotencyKey: input.IdempotencyKey,
	}
	if replay, found, err := s.store.ReplaySolutionEpisodeTransition(ctx, transition); err != nil {
		return core.SolutionEpisode{}, err
	} else if found {
		return replay, nil
	}
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionEpisode{}, err
	}
	if episode.Version != input.ExpectedVersion {
		return core.SolutionEpisode{}, errors.New("solution episode version conflict")
	}
	if episode.Status.Terminal() || strings.TrimSpace(input.TargetPrincipalID) == "" || strings.TrimSpace(input.TargetSessionID) == "" {
		return core.SolutionEpisode{}, errors.New("invalid solution episode handoff")
	}
	transition.EpisodeID, transition.Workspace, transition.PrincipalID = episode.ID, episode.Workspace, episode.PrincipalID
	transition.UpdatedAt = s.now().UTC()
	updated, err := s.store.TransitionSolutionEpisode(ctx, transition)
	if errors.Is(err, sql.ErrNoRows) {
		return core.SolutionEpisode{}, errors.New("solution episode version conflict")
	}
	if err == nil {
		s.audit(ctx, updated.Workspace, updated.SessionID, updated.PrincipalID, updated.ClientID, "",
			"solution_handoff", "success", "ownership_transferred", updated.ID,
			map[string]any{"from_principal": episode.PrincipalID, "to_principal": updated.PrincipalID, "version": updated.Version})
	}
	return updated, err
}

type SolutionCheckpointInput struct {
	Workspace          string
	PrincipalID        string
	EpisodeID          string
	ExpectedGeneration int64
	GoalSummary        string
	Constraints        []string
	PlanItems          []core.SolutionPlanItem
	CompletedItems     []string
	OpenQuestions      []string
	NextAction         string
	Artifacts          []core.SolutionReference
	Sensitivity        core.SolutionSensitivity
	TTL                time.Duration
	Origin             engine.SolutionAdmissionOrigin
}

func (s *SolutionService) Checkpoint(ctx context.Context, input SolutionCheckpointInput) (core.SolutionWorkingState, error) {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	origin := solutionOriginOrAgent(input.Origin)
	admitItem := func(field engine.SolutionAdmissionField, content string) (string, error) {
		if strings.TrimSpace(content) == "" {
			return "", nil
		}
		return s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID,
			"", origin, field, content)
	}
	goal, err := admitItem(engine.SolutionFieldGoalSummary, input.GoalSummary)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	constraints, err := admitSolutionStrings(input.Constraints, func(content string) (string, error) {
		return admitItem(engine.SolutionFieldWorkingStateItem, content)
	})
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	completed, err := admitSolutionStrings(input.CompletedItems, func(content string) (string, error) {
		return admitItem(engine.SolutionFieldWorkingStateItem, content)
	})
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	questions, err := admitSolutionStrings(input.OpenQuestions, func(content string) (string, error) {
		return admitItem(engine.SolutionFieldWorkingStateItem, content)
	})
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	plans := append([]core.SolutionPlanItem(nil), input.PlanItems...)
	for i := range plans {
		plans[i].Summary, err = admitItem(engine.SolutionFieldWorkingStateItem, plans[i].Summary)
		if err != nil {
			return core.SolutionWorkingState{}, err
		}
	}
	nextAction, err := admitItem(engine.SolutionFieldWorkingStateItem, input.NextAction)
	if err != nil {
		return core.SolutionWorkingState{}, err
	}
	ttl := input.TTL
	if ttl <= 0 {
		ttl = defaultSolutionWorkingStateTTL
	}
	if ttl > maxSolutionWorkingStateTTL {
		return core.SolutionWorkingState{}, errors.New("solution working state TTL exceeds 7 days")
	}
	now := s.now().UTC()
	state, err := s.store.PutSolutionWorkingState(ctx, core.SolutionWorkingState{
		EpisodeID: episode.ID, Workspace: episode.Workspace, SessionID: episode.SessionID,
		PrincipalID: episode.PrincipalID, GoalSummary: goal, Constraints: constraints, PlanItems: plans,
		CompletedItems: completed, OpenQuestions: questions, NextAction: nextAction,
		Artifacts: input.Artifacts, Sensitivity: input.Sensitivity, UpdatedAt: now, ExpiresAt: now.Add(ttl),
	}, input.ExpectedGeneration)
	if err == nil {
		s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "",
			"solution_checkpoint", "success", "accepted", episode.ID,
			map[string]any{"generation": state.Generation, "expires_at": state.ExpiresAt.Format(time.RFC3339)})
	}
	return state, err
}

func (s *SolutionService) GetWorkingState(ctx context.Context, workspace, principalID, episodeID string) (core.SolutionWorkingState, error) {
	if _, err := s.authorizedEpisode(ctx, workspace, principalID, episodeID); err != nil {
		return core.SolutionWorkingState{}, err
	}
	return s.store.GetSolutionWorkingState(ctx, workspace, principalID, episodeID, s.now().UTC())
}

func (s *SolutionService) ClearWorkingState(ctx context.Context, workspace, principalID, episodeID string) error {
	if _, err := s.authorizedEpisode(ctx, workspace, principalID, episodeID); err != nil {
		return err
	}
	return s.store.ClearSolutionWorkingState(ctx, workspace, principalID, episodeID)
}

func (s *SolutionService) CleanupExpiredWorkingState(ctx context.Context, limit int) (int, error) {
	return s.store.CleanupExpiredSolutionWorkingState(ctx, s.now().UTC(), limit)
}

func (s *SolutionService) authorizedEpisode(ctx context.Context, workspace, principalID, episodeID string) (core.SolutionEpisode, error) {
	episode, err := s.store.GetSolutionEpisode(ctx, episodeID)
	if err != nil {
		return core.SolutionEpisode{}, err
	}
	if episode.Workspace != strings.TrimSpace(workspace) || episode.PrincipalID != strings.TrimSpace(principalID) {
		return core.SolutionEpisode{}, errors.New("solution episode not authorized")
	}
	return episode, nil
}

func (s *SolutionService) admit(ctx context.Context, workspace, sessionID, principalID, clientID, requestID string,
	origin engine.SolutionAdmissionOrigin, field engine.SolutionAdmissionField, content string) (string, error) {
	decision := s.admission.Evaluate(ctx, engine.SolutionAdmissionInput{
		Workspace: workspace, Origin: origin, Field: field, Content: content,
	})
	if decision.Disposition == engine.SolutionAdmissionAllow {
		return decision.SafeContent, nil
	}
	if decision.Disposition == engine.SolutionAdmissionRedact {
		s.audit(ctx, workspace, sessionID, principalID, clientID, requestID, "solution_admission",
			string(decision.Disposition), string(decision.Reason), "", map[string]any{"field": field, "origin": origin})
		return decision.SafeContent, nil
	}
	s.audit(ctx, workspace, sessionID, principalID, clientID, requestID, "solution_admission",
		string(decision.Disposition), string(decision.Reason), "", map[string]any{"field": field, "origin": origin})
	return "", fmt.Errorf("solution content admission %s: %s", decision.Disposition, decision.Reason)
}

func (s *SolutionService) audit(ctx context.Context, workspace, sessionID, principalID, clientID, requestID,
	operation, outcome, reason, targetID string, metadata map[string]any) {
	ids := []string(nil)
	if strings.TrimSpace(targetID) != "" {
		ids = []string{targetID}
	}
	_, _ = s.store.AppendAuditEvent(ctx, sqlite.AuditEventInput{
		Workspace: workspace, Operation: operation, Outcome: outcome, Actor: principalID,
		Source: clientID, RequestID: requestID, SessionID: sessionID, TargetType: "solution_episode",
		TargetIDs: ids, Reason: reason, Metadata: metadata, OccurredAt: s.now().UTC(),
	})
}

func solutionOriginOrAgent(origin engine.SolutionAdmissionOrigin) engine.SolutionAdmissionOrigin {
	if origin == "" {
		return engine.SolutionOriginAgent
	}
	return origin
}

func validSolutionTransition(from, to core.SolutionEpisodeStatus) bool {
	if from.Terminal() || from == to {
		return false
	}
	switch from {
	case core.SolutionEpisodeActive:
		return to == core.SolutionEpisodePaused || to.Terminal()
	case core.SolutionEpisodePaused:
		return to == core.SolutionEpisodeActive || to.Terminal()
	default:
		return false
	}
}

func admitSolutionStrings(values []string, admit func(string) (string, error)) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		accepted, err := admit(value)
		if err != nil {
			return nil, err
		}
		out = append(out, accepted)
	}
	return out, nil
}
