package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	writer    *engine.WritePipeline
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

func WithSolutionWriter(writer *engine.WritePipeline) SolutionServiceOption {
	return func(service *SolutionService) {
		if writer != nil {
			service.writer = writer
		}
	}
}

func NewSolutionService(store *sqlite.Store, admission *engine.SolutionAdmissionPolicy, options ...SolutionServiceOption) *SolutionService {
	if admission == nil {
		admission = engine.NewSolutionAdmissionPolicy()
	}
	service := &SolutionService{store: store, admission: admission, writer: engine.NewWritePipeline(store), now: time.Now}
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
	for _, parentID := range input.ParentStepIDs {
		parent, parentErr := s.store.GetSolutionStep(ctx, parentID)
		if parentErr != nil || parent.EpisodeID != episode.ID {
			return core.SolutionStep{}, false, errors.New("solution reference not authorized")
		}
	}
	references, err := s.bindSolutionReferences(ctx, episode, input.References)
	if err != nil {
		return core.SolutionStep{}, false, err
	}
	step, deduplicated, err := s.store.AppendSolutionStep(ctx, sqlite.SolutionStepInsert{
		EpisodeID: episode.ID, Kind: input.Kind, Status: input.Status, Summary: summary,
		RationaleSummary: rationale, Source: input.Source, ParentStepIDs: input.ParentStepIDs,
		References: references, Confidence: input.Confidence, Sensitivity: input.Sensitivity,
		IdempotencyKey: input.IdempotencyKey, CreatedAt: s.now().UTC(),
	})
	if err == nil && !deduplicated {
		s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID,
			input.IdempotencyKey, "solution_step_append", "success", "accepted", step.ID,
			map[string]any{"kind": step.Kind, "ordinal": step.Ordinal})
	}
	return step, deduplicated, err
}

func (s *SolutionService) bindSolutionReferences(ctx context.Context, episode core.SolutionEpisode, references []core.SolutionReference) ([]core.SolutionReference, error) {
	bound := append([]core.SolutionReference(nil), references...)
	for i := range bound {
		bound[i].Workspace, bound[i].SessionID = episode.Workspace, episode.SessionID
		switch bound[i].Kind {
		case core.SolutionReferenceObservation:
			observation, err := s.store.GetObservation(ctx, bound[i].TargetID)
			if err != nil || observation.Workspace != episode.Workspace || observation.SessionID != episode.SessionID {
				return nil, errors.New("solution reference not authorized")
			}
			bound[i].Resolution = core.SolutionReferenceVerified
		case core.SolutionReferenceMemory:
			memory, err := s.store.GetMemory(ctx, bound[i].TargetID)
			if err != nil || memory.Workspace != episode.Workspace || (memory.SessionID != nil && *memory.SessionID != episode.SessionID) {
				return nil, errors.New("solution reference not authorized")
			}
			bound[i].Resolution = core.SolutionReferenceVerified
		case core.SolutionReferenceStep:
			step, err := s.store.GetSolutionStep(ctx, bound[i].TargetID)
			if err != nil || step.EpisodeID != episode.ID {
				return nil, errors.New("solution reference not authorized")
			}
			bound[i].Resolution = core.SolutionReferenceVerified
		default:
			bound[i].Resolution = core.SolutionReferenceScoped
		}
	}
	return bound, nil
}

type SolutionCorrelationInput struct {
	Workspace       string
	PrincipalID     string
	EpisodeID       string
	ToolName        string
	ExternalEventID string
	OccurredAround  time.Time
	Window          time.Duration
	Limit           int
}

func (s *SolutionService) ProposeObservationLinks(ctx context.Context, input SolutionCorrelationInput) (core.SolutionCorrelationResult, error) {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionCorrelationResult{}, err
	}
	if strings.TrimSpace(input.ToolName) == "" && strings.TrimSpace(input.ExternalEventID) == "" {
		return core.SolutionCorrelationResult{}, errors.New("tool_name or external_event_id is required")
	}
	window := input.Window
	if window <= 0 {
		window = 5 * time.Minute
	}
	if window > 15*time.Minute {
		return core.SolutionCorrelationResult{}, errors.New("solution correlation window exceeds 15 minutes")
	}
	around := input.OccurredAround.UTC()
	if around.IsZero() {
		around = s.now().UTC()
	}
	from, to := around.Add(-window), around.Add(window)
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	observations, err := s.store.ListObservations(ctx, episode.Workspace, episode.SessionID, &from, &to, limit)
	if err != nil {
		return core.SolutionCorrelationResult{}, err
	}
	matches := make([]core.Observation, 0, len(observations))
	basis, confidence := "tool_and_time_window", 0.7
	if eventID := strings.TrimSpace(input.ExternalEventID); eventID != "" {
		basis, confidence = "external_event_id", 1.0
		for _, observation := range observations {
			if observation.ExternalEventID == eventID {
				matches = append(matches, observation)
			}
		}
	} else {
		toolName := strings.TrimSpace(input.ToolName)
		for _, observation := range observations {
			if observation.ToolName != nil && *observation.ToolName == toolName {
				matches = append(matches, observation)
			}
		}
	}
	result := core.SolutionCorrelationResult{Examined: len(observations), Ambiguous: len(matches) > 1}
	if len(matches) == 1 {
		result.Proposals = []core.SolutionCorrelationProposal{{Reference: core.SolutionReference{
			Kind: core.SolutionReferenceObservation, TargetID: matches[0].ID, Workspace: episode.Workspace,
			SessionID: episode.SessionID, Resolution: core.SolutionReferenceVerified,
		}, Basis: basis, Confidence: confidence}}
	}
	return result, nil
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

type SolutionFinalizeInput struct {
	Workspace       string
	PrincipalID     string
	EpisodeID       string
	ExpectedVersion int64
	IdempotencyKey  string
}

func (s *SolutionService) Finalize(ctx context.Context, input SolutionFinalizeInput) (core.SolutionSummary, error) {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	if episode.Version != input.ExpectedVersion {
		return core.SolutionSummary{}, errors.New("solution episode version conflict")
	}
	if !episode.Status.Terminal() {
		return core.SolutionSummary{}, errors.New("solution episode must be terminal before finalization")
	}
	steps, err := s.loadFinalizationSteps(ctx, episode.ID)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	assembled := assembleSolutionSummary(episode, steps)
	snapshotPayload, err := json.Marshal(struct {
		Episode core.SolutionEpisode
		Steps   []core.SolutionStep
	}{episode, steps})
	if err != nil {
		return core.SolutionSummary{}, err
	}
	snapshotSum := sha256.Sum256(snapshotPayload)
	assembled.SnapshotHash = hex.EncodeToString(snapshotSum[:])
	assembled.IdempotencyKey = input.IdempotencyKey
	assembled.ExpectedEpisodeVersion = input.ExpectedVersion
	assembled.CreatedAt = s.now().UTC()
	summary, _, err := s.store.CreateSolutionSummary(ctx, assembled)
	if err == nil {
		s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, input.IdempotencyKey,
			"solution_finalize", "success", string(summary.Outcome), summary.ID, map[string]any{"summary_version": summary.Version, "episode_version": input.ExpectedVersion})
	}
	return summary, err
}

type SolutionPromotionTarget struct {
	MemoryType    core.MemoryType
	Content       string
	SourceStepIDs []string
}

type SolutionPromoteInput struct {
	Workspace, PrincipalID, EpisodeID, SummaryID, IdempotencyKey string
	Targets                                                      []SolutionPromotionTarget
}

type SolutionPromotionResult struct {
	Promotions []core.SolutionPromotion `json:"promotions"`
	Partial    bool                     `json:"partial"`
}

func (s *SolutionService) Promote(ctx context.Context, input SolutionPromoteInput) (SolutionPromotionResult, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return SolutionPromotionResult{}, errors.New("solution promotion idempotency key is required")
	}
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return SolutionPromotionResult{}, err
	}
	summary, err := s.store.GetSolutionSummary(ctx, input.SummaryID)
	if err != nil || summary.EpisodeID != episode.ID {
		return SolutionPromotionResult{}, errors.New("solution summary not authorized")
	}
	if summary.SupersededBy != "" {
		return SolutionPromotionResult{}, errors.New("superseded solution summary cannot be promoted")
	}
	if len(input.Targets) == 0 || len(input.Targets) > 8 {
		return SolutionPromotionResult{}, errors.New("solution promotion requires 1 to 8 targets")
	}
	observationIDs := make([]string, 0)
	for _, evidence := range summary.Evidence {
		if evidence.Kind == core.SolutionReferenceObservation && evidence.Resolution == core.SolutionReferenceVerified {
			observationIDs = append(observationIDs, evidence.TargetID)
		}
	}
	result := SolutionPromotionResult{Promotions: make([]core.SolutionPromotion, 0, len(input.Targets))}
	for index, target := range input.Targets {
		if !core.IsMemoryType(target.MemoryType) {
			return SolutionPromotionResult{}, fmt.Errorf("invalid promotion memory type %q", target.MemoryType)
		}
		for _, stepID := range target.SourceStepIDs {
			step, stepErr := s.store.GetSolutionStep(ctx, stepID)
			if stepErr != nil || step.EpisodeID != episode.ID {
				return SolutionPromotionResult{}, errors.New("solution promotion step not authorized")
			}
		}
		itemKey := fmt.Sprintf("%s:%d:%s", strings.TrimSpace(input.IdempotencyKey), index, target.MemoryType)
		promotion, replay, err := s.store.BeginSolutionPromotion(ctx, sqlite.SolutionPromotionInsert{EpisodeID: episode.ID, SummaryID: summary.ID,
			MemoryType: target.MemoryType, SourceStepIDs: target.SourceStepIDs, ObservationIDs: observationIDs,
			IdempotencyKey: itemKey, PolicyIdentity: "solution-promotion-v1", CreatedAt: s.now().UTC()})
		if err != nil {
			return result, err
		}
		if replay && promotion.State == core.SolutionPromotionPublished {
			result.Promotions = append(result.Promotions, promotion)
			continue
		}
		content := strings.TrimSpace(target.Content)
		if content == "" {
			content = defaultSolutionPromotionContent(summary, target.MemoryType)
		}
		writeInput := engine.WriteInput{Workspace: episode.Workspace, Type: target.MemoryType, Content: content,
			Tags: []string{"solution-path", "promoted"}, Keywords: []string{"solution-path"},
			Source: core.MemorySource{Type: core.SourceAgentObservation, SessionID: episode.SessionID}, Mode: engine.ExtractFast,
			ContentHashSalt: "solution:" + summary.ID + ":" + itemKey}
		if target.MemoryType == core.OutcomeMemory {
			writeInput.Outcome = &core.Outcome{Result: summary.Outcome, Approach: summary.NextGuidance, Reason: strings.Join(summary.Risks, "; ")}
		}
		written, writeErr := s.writer.Write(ctx, writeInput)
		if writeErr != nil {
			promotion, err = s.store.CompleteSolutionPromotion(ctx, promotion.ID, promotion.TargetID, core.SolutionPromotionFailed, writeErr.Error())
		} else if written.Rejected {
			promotion, err = s.store.CompleteSolutionPromotion(ctx, promotion.ID, "", core.SolutionPromotionFailed, written.RejectReason)
		} else {
			if linkErr := s.store.LinkMemoryObservations(ctx, written.ID, observationIDs); linkErr != nil {
				promotion, err = s.store.CompleteSolutionPromotion(ctx, promotion.ID, written.ID, core.SolutionPromotionFailed, linkErr.Error())
			} else {
				promotion, err = s.store.CompleteSolutionPromotion(ctx, promotion.ID, written.ID, core.SolutionPromotionPublished, "")
			}
		}
		if err != nil {
			return result, err
		}
		if promotion.State == core.SolutionPromotionFailed {
			result.Partial = true
		}
		result.Promotions = append(result.Promotions, promotion)
	}
	s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, input.IdempotencyKey,
		"solution_promote", "success", map[bool]string{true: "partial", false: "published"}[result.Partial], summary.ID,
		map[string]any{"target_count": len(result.Promotions), "partial": result.Partial})
	return result, nil
}

func defaultSolutionPromotionContent(summary core.SolutionSummary, memoryType core.MemoryType) string {
	if memoryType == core.ProceduralMemory && strings.TrimSpace(summary.NextGuidance) != "" {
		return "Next guidance: " + summary.NextGuidance + "\n\n" + summary.Summary
	}
	return summary.Summary
}

type SolutionToolEventInput struct {
	Workspace, PrincipalID, EpisodeID, StepID, ToolName, ToolVersion, Operation, Capability, InputSummary, IdempotencyKey string
	Kind                                                                                                                  core.SolutionToolEventKind
	ResultClass                                                                                                           core.SolutionToolResultClass
	TaskVerified                                                                                                          bool
	DurationMS                                                                                                            int64
	Evidence                                                                                                              []core.SolutionReference
}

func (s *SolutionService) RecordToolEvent(ctx context.Context, input SolutionToolEventInput) (core.SolutionToolInvocationRecord, error) {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionToolInvocationRecord{}, err
	}
	step, err := s.store.GetSolutionStep(ctx, input.StepID)
	if err != nil || step.EpisodeID != episode.ID {
		return core.SolutionToolInvocationRecord{}, errors.New("solution tool event step not authorized")
	}
	if !input.Kind.Valid() || !input.ResultClass.Valid() {
		return core.SolutionToolInvocationRecord{}, errors.New("invalid solution tool event classification")
	}
	if input.Kind != core.SolutionToolResult && input.TaskVerified {
		return core.SolutionToolInvocationRecord{}, errors.New("only a tool result can be task verified")
	}
	if strings.TrimSpace(input.ToolName) == "" || strings.TrimSpace(input.Operation) == "" {
		return core.SolutionToolInvocationRecord{}, errors.New("tool_name and operation are required")
	}
	capability, err := s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, input.IdempotencyKey, engine.SolutionOriginAgent, engine.SolutionFieldWorkingStateItem, input.Capability)
	if err != nil {
		return core.SolutionToolInvocationRecord{}, err
	}
	inputSummary := ""
	if strings.TrimSpace(input.InputSummary) != "" {
		inputSummary, err = s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, input.IdempotencyKey, engine.SolutionOriginAgent, engine.SolutionFieldWorkingStateItem, input.InputSummary)
		if err != nil {
			return core.SolutionToolInvocationRecord{}, err
		}
	}
	evidence, err := s.bindSolutionReferences(ctx, episode, input.Evidence)
	if err != nil {
		return core.SolutionToolInvocationRecord{}, err
	}
	record, _, err := s.store.InsertSolutionToolEvent(ctx, sqlite.SolutionToolEventInsert{Record: core.SolutionToolInvocationRecord{
		Workspace: episode.Workspace, EpisodeID: episode.ID, StepID: step.ID, Kind: input.Kind, ToolName: strings.TrimSpace(input.ToolName),
		ToolVersion: strings.TrimSpace(input.ToolVersion), Operation: strings.TrimSpace(input.Operation), Capability: capability,
		InputSummary: inputSummary, ResultClass: input.ResultClass, TaskVerified: input.TaskVerified, DurationMS: input.DurationMS,
		Evidence: evidence, OccurredAt: s.now().UTC(),
	}, IdempotencyKey: input.IdempotencyKey})
	return record, err
}

type SolutionToolLessonInput struct {
	Workspace, PrincipalID, Fallback string
	EventIDs                         []string
	Reviewed                         bool
}

func (s *SolutionService) DeriveToolLesson(ctx context.Context, input SolutionToolLessonInput) (core.SolutionToolLesson, error) {
	if len(input.EventIDs) == 0 || len(input.EventIDs) > 100 {
		return core.SolutionToolLesson{}, errors.New("tool lesson requires 1 to 100 source events")
	}
	events := make([]core.SolutionToolInvocationRecord, 0, len(input.EventIDs))
	for _, eventID := range input.EventIDs {
		event, err := s.store.GetSolutionToolEvent(ctx, eventID)
		if err != nil {
			return core.SolutionToolLesson{}, err
		}
		if _, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, event.EpisodeID); err != nil {
			return core.SolutionToolLesson{}, err
		}
		events = append(events, event)
	}
	first := events[0]
	lesson := core.SolutionToolLesson{Workspace: first.Workspace, ToolName: first.ToolName, Capability: first.Capability,
		Validation: core.SolutionValidationProposed, Confidence: 0.5, SourceEventIDs: append([]string(nil), input.EventIDs...), CreatedAt: s.now().UTC()}
	versions, episodes, steps, operations := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	successVersions, failedVersions := map[string]struct{}{}, map[string]struct{}{}
	evidence := []core.SolutionReference{}
	for _, event := range events {
		if event.Workspace != first.Workspace || event.ToolName != first.ToolName || event.Capability != first.Capability {
			return core.SolutionToolLesson{}, errors.New("tool lesson events must share workspace, tool_name, and capability")
		}
		if event.ToolVersion != "" {
			versions[event.ToolVersion] = struct{}{}
		}
		episodes[event.EpisodeID] = struct{}{}
		steps[event.StepID] = struct{}{}
		operations[event.Operation] = struct{}{}
		evidence = append(evidence, event.Evidence...)
		if event.Kind == core.SolutionToolResult && event.ResultClass == core.SolutionToolResultSuccess && event.TaskVerified {
			lesson.SuccessCount++
			successVersions[event.ToolVersion] = struct{}{}
		}
		if event.Kind == core.SolutionToolResult && (event.ResultClass == core.SolutionToolResultFailure || event.ResultClass == core.SolutionToolResultPartial) {
			failedVersions[event.ToolVersion] = struct{}{}
			failure := strings.TrimSpace(event.InputSummary)
			if failure == "" {
				failure = "Tool result was " + string(event.ResultClass)
			}
			lesson.FailureModes = append(lesson.FailureModes, clipSolutionText(failure, core.MaxSolutionStateItemBytes))
		}
	}
	lesson.ToolVersions = sortedSolutionSet(versions)
	lesson.SourceEpisodeIDs = sortedSolutionSet(episodes)
	lesson.SourceStepIDs = sortedSolutionSet(steps)
	lesson.Preconditions = sortedSolutionSet(operations)
	lesson.Evidence = deduplicateSolutionReferences(evidence, core.MaxSolutionReferencesPerStep)
	conflictingVersion := false
	for version := range failedVersions {
		if _, sameSucceeded := successVersions[version]; !sameSucceeded && len(successVersions) > 0 {
			conflictingVersion = true
		}
	}
	if lesson.SuccessCount == 0 {
		lesson.Limitations = append(lesson.Limitations, "No task-verified successful result is present.")
	}
	if conflictingVersion {
		lesson.Limitations = append(lesson.Limitations, "Tool behavior conflicts across recorded versions.")
	}
	if input.Reviewed || (lesson.SuccessCount >= 2 && !conflictingVersion) {
		lesson.Validation = core.SolutionValidationVerified
		lesson.Confidence = 0.9
	}
	fallback := strings.TrimSpace(input.Fallback)
	if fallback == "" {
		fallback = "Use an alternative tool or complete the workflow manually."
	}
	admittedFallback, err := s.admit(ctx, first.Workspace, "", input.PrincipalID, "", "", engine.SolutionOriginAgent, engine.SolutionFieldWorkingStateItem, fallback)
	if err != nil {
		return core.SolutionToolLesson{}, err
	}
	lesson.Fallback = admittedFallback
	stored, _, err := s.store.PutSolutionToolLesson(ctx, lesson)
	return stored, err
}

func sortedSolutionSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func deduplicateSolutionReferences(values []core.SolutionReference, limit int) []core.SolutionReference {
	seen := make(map[string]struct{})
	result := make([]core.SolutionReference, 0, len(values))
	for _, value := range values {
		key := string(value.Kind) + "\x00" + value.TargetID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if len(result) >= limit {
			break
		}
	}
	return result
}

type ToolLessonPromotionInput struct{ Workspace, PrincipalID, LessonID, IdempotencyKey string }

func (s *SolutionService) PromoteToolLesson(ctx context.Context, input ToolLessonPromotionInput) (core.SolutionPromotion, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return core.SolutionPromotion{}, errors.New("tool lesson promotion idempotency key is required")
	}
	lesson, err := s.store.GetSolutionToolLesson(ctx, input.LessonID)
	if err != nil || lesson.Workspace != strings.TrimSpace(input.Workspace) {
		return core.SolutionPromotion{}, errors.New("tool lesson not authorized")
	}
	if lesson.Validation != core.SolutionValidationVerified {
		return core.SolutionPromotion{}, errors.New("tool lesson requires verified success evidence or review")
	}
	var episode core.SolutionEpisode
	for index, eventID := range lesson.SourceEventIDs {
		event, eventErr := s.store.GetSolutionToolEvent(ctx, eventID)
		if eventErr != nil {
			return core.SolutionPromotion{}, eventErr
		}
		authorized, authErr := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, event.EpisodeID)
		if authErr != nil {
			return core.SolutionPromotion{}, authErr
		}
		if index == 0 {
			episode = authorized
		}
	}
	promotion, replay, err := s.store.BeginToolLessonPromotion(ctx, lesson.ID, episode.ID, input.IdempotencyKey, "tool-lesson-promotion-v1", s.now().UTC())
	if err != nil {
		return core.SolutionPromotion{}, err
	}
	if replay && promotion.State == core.SolutionPromotionPublished {
		promotion.SourceStepIDs, promotion.ObservationIDs = lesson.SourceStepIDs, toolLessonObservationIDs(lesson)
		return promotion, nil
	}
	content := formatToolLessonProceduralMemory(lesson)
	written, writeErr := s.writer.Write(ctx, engine.WriteInput{Workspace: lesson.Workspace, Type: core.ProceduralMemory, Content: content,
		Tags: []string{"tool-lesson", "solution-path"}, Keywords: []string{lesson.ToolName, "tool-lesson"},
		Source: core.MemorySource{Type: core.SourceAgentObservation, SessionID: episode.SessionID}, Mode: engine.ExtractFast,
		ContentHashSalt: "tool-lesson:" + lesson.ID})
	observationIDs := toolLessonObservationIDs(lesson)
	if writeErr != nil {
		promotion, err = s.store.CompleteToolLessonPromotion(ctx, promotion.ID, promotion.TargetID, core.SolutionPromotionFailed, writeErr.Error())
	} else if written.Rejected {
		promotion, err = s.store.CompleteToolLessonPromotion(ctx, promotion.ID, "", core.SolutionPromotionFailed, written.RejectReason)
	} else if linkErr := s.store.LinkMemoryObservations(ctx, written.ID, observationIDs); linkErr != nil {
		promotion, err = s.store.CompleteToolLessonPromotion(ctx, promotion.ID, written.ID, core.SolutionPromotionFailed, linkErr.Error())
	} else {
		promotion, err = s.store.CompleteToolLessonPromotion(ctx, promotion.ID, written.ID, core.SolutionPromotionPublished, "")
	}
	if err != nil {
		return core.SolutionPromotion{}, err
	}
	promotion.SourceStepIDs, promotion.ObservationIDs = lesson.SourceStepIDs, observationIDs
	return promotion, nil
}

func toolLessonObservationIDs(lesson core.SolutionToolLesson) []string {
	ids := make([]string, 0)
	for _, evidence := range lesson.Evidence {
		if evidence.Kind == core.SolutionReferenceObservation && evidence.Resolution == core.SolutionReferenceVerified {
			ids = append(ids, evidence.TargetID)
		}
	}
	return ids
}

func formatToolLessonProceduralMemory(lesson core.SolutionToolLesson) string {
	var builder strings.Builder
	for _, line := range []string{"Tool: " + lesson.ToolName, "Capability: " + lesson.Capability,
		"Preconditions: " + strings.Join(lesson.Preconditions, "; "), "Limitations: " + strings.Join(lesson.Limitations, "; "),
		"Failure modes: " + strings.Join(lesson.FailureModes, "; "), "Fallback: " + lesson.Fallback} {
		if !strings.HasSuffix(line, ": ") {
			appendSolutionSummaryLine(&builder, line)
		}
	}
	return builder.String()
}

func (s *SolutionService) loadFinalizationSteps(ctx context.Context, episodeID string) ([]core.SolutionStep, error) {
	const maxSteps = 500
	steps := make([]core.SolutionStep, 0, 64)
	var after int64
	for len(steps) < maxSteps {
		page, err := s.store.ListSolutionSteps(ctx, episodeID, after, 200)
		if err != nil {
			return nil, err
		}
		steps = append(steps, page...)
		if len(page) < 200 {
			return steps, nil
		}
		after = page[len(page)-1].Ordinal
	}
	probe, err := s.store.ListSolutionSteps(ctx, episodeID, after, 1)
	if err != nil {
		return nil, err
	}
	if len(probe) > 0 {
		return nil, errors.New("solution episode exceeds 500-step finalization bound")
	}
	return steps, nil
}

func assembleSolutionSummary(episode core.SolutionEpisode, steps []core.SolutionStep) sqlite.SolutionSummaryInsert {
	result := sqlite.SolutionSummaryInsert{EpisodeID: episode.ID, Outcome: solutionOutcome(episode.Status), Validation: core.SolutionValidationVerified}
	seenEvidence := make(map[string]struct{})
	lastGuidance := episode.GoalSummary
	var builder strings.Builder
	appendSolutionSummaryLine(&builder, "Goal: "+episode.GoalSummary)
	appendSolutionSummaryLine(&builder, "Outcome: "+string(result.Outcome))
	for _, step := range steps {
		if step.Status == core.SolutionStepFailed && len(result.UsefulFailureStepIDs) < core.MaxSolutionSummaryStepIDs {
			result.UsefulFailureStepIDs = append(result.UsefulFailureStepIDs, step.ID)
			if len(result.Risks) < core.MaxSolutionStateItems {
				result.Risks = append(result.Risks, clipSolutionText(step.Summary, core.MaxSolutionStateItemBytes))
			}
			appendSolutionSummaryLine(&builder, "Useful failure: "+step.Summary)
		}
		if step.Status == core.SolutionStepCompleted && (step.Kind == core.SolutionStepDecision || step.Kind == core.SolutionStepResult || step.Kind == core.SolutionStepCheckpoint) {
			if len(result.DecisiveStepIDs) < core.MaxSolutionSummaryStepIDs {
				result.DecisiveStepIDs = append(result.DecisiveStepIDs, step.ID)
			}
			appendSolutionSummaryLine(&builder, "Decisive step: "+step.Summary)
			lastGuidance = step.Summary
		}
		for _, reference := range step.References {
			key := string(reference.Kind) + "\x00" + reference.TargetID
			if _, exists := seenEvidence[key]; exists || len(result.Evidence) >= core.MaxSolutionReferencesPerStep {
				continue
			}
			seenEvidence[key] = struct{}{}
			result.Evidence = append(result.Evidence, reference)
		}
	}
	if len(result.Risks) == 0 && episode.Status != core.SolutionEpisodeCompleted {
		result.Risks = []string{"The episode ended without a fully successful outcome."}
	}
	result.NextGuidance = clipSolutionText(lastGuidance, core.MaxSolutionStateItemBytes)
	result.Summary = builder.String()
	return result
}

func solutionOutcome(status core.SolutionEpisodeStatus) core.OutcomeResult {
	switch status {
	case core.SolutionEpisodeCompleted:
		return core.OutcomeSuccess
	case core.SolutionEpisodePartial:
		return core.OutcomePartial
	default:
		return core.OutcomeFailure
	}
}

func appendSolutionSummaryLine(builder *strings.Builder, line string) {
	if builder.Len() >= core.MaxSolutionSummaryBytes {
		return
	}
	remaining := core.MaxSolutionSummaryBytes - builder.Len()
	line = strings.TrimSpace(line) + "\n"
	if len(line) > remaining {
		line = clipSolutionText(line, remaining)
	}
	builder.WriteString(line)
}

func clipSolutionText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		for limit > 0 && !utf8.ValidString(value[:limit]) {
			limit--
		}
		return value[:limit]
	}
	end := limit - 3
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return strings.TrimSpace(value[:end]) + "..."
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
