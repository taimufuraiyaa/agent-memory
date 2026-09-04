package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type SolutionActivityEpisode struct {
	Episode   core.SolutionEpisode  `json:"episode"`
	Summary   *core.SolutionSummary `json:"summary,omitempty"`
	Pinned    bool                  `json:"pinned"`
	StepCount int                   `json:"step_count"`
}

type SolutionActivityDetail struct {
	Episode          core.SolutionEpisode                   `json:"episode"`
	Summary          *core.SolutionSummary                  `json:"summary,omitempty"`
	Steps            []core.SolutionStep                    `json:"steps"`
	Promotions       []core.SolutionPromotion               `json:"promotions"`
	PromotionTargets []SolutionActivityPromotionTarget      `json:"promotion_targets"`
	StepReviews      []core.SolutionStepReview              `json:"step_reviews"`
	PathFeedback     []core.SolutionRetrievalFeedbackRecord `json:"path_feedback"`
	Pinned           bool                                   `json:"pinned"`
}

type SolutionActivityPromotionTarget struct {
	Promotion    core.SolutionPromotion `json:"promotion"`
	Memory       *core.MemoryEntry      `json:"memory,omitempty"`
	Availability string                 `json:"availability"`
}

func (s *SolutionService) ListActivityEpisodes(ctx context.Context, workspace string, limit int) ([]SolutionActivityEpisode, error) {
	episodes, err := s.store.ListSolutionEpisodes(ctx, workspace, limit)
	if err != nil {
		return nil, err
	}
	result := make([]SolutionActivityEpisode, 0, len(episodes))
	for _, episode := range episodes {
		item := SolutionActivityEpisode{Episode: episode}
		if summary, summaryErr := s.store.LatestSolutionSummary(ctx, episode.ID); summaryErr == nil {
			item.Summary = &summary
		} else if !errors.Is(summaryErr, sql.ErrNoRows) {
			return nil, summaryErr
		}
		item.StepCount, err = s.store.CountSolutionSteps(ctx, episode.ID)
		if err != nil {
			return nil, err
		}
		item.Pinned, err = s.store.SolutionEpisodePinned(ctx, workspace, episode.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *SolutionService) GetActivityEpisode(ctx context.Context, workspace, episodeID string) (SolutionActivityDetail, error) {
	episode, err := s.store.GetSolutionEpisode(ctx, episodeID)
	if err != nil || episode.Workspace != strings.TrimSpace(workspace) {
		return SolutionActivityDetail{}, errors.New("solution episode not authorized")
	}
	detail := SolutionActivityDetail{Episode: episode, Steps: []core.SolutionStep{}, Promotions: []core.SolutionPromotion{}, PromotionTargets: []SolutionActivityPromotionTarget{}, StepReviews: []core.SolutionStepReview{}, PathFeedback: []core.SolutionRetrievalFeedbackRecord{}}
	if summary, summaryErr := s.store.LatestSolutionSummary(ctx, episode.ID); summaryErr == nil {
		detail.Summary = &summary
	} else if !errors.Is(summaryErr, sql.ErrNoRows) {
		return SolutionActivityDetail{}, summaryErr
	}
	if detail.Steps, err = s.store.ListSolutionSteps(ctx, episode.ID, 0, 200); err != nil {
		return SolutionActivityDetail{}, err
	}
	if detail.Promotions, err = s.store.ListSolutionPromotionsByEpisode(ctx, episode.ID); err != nil {
		return SolutionActivityDetail{}, err
	}
	detail.PromotionTargets, err = s.resolveActivityPromotionTargets(ctx, workspace, detail.Promotions)
	if err != nil {
		return SolutionActivityDetail{}, err
	}
	if detail.StepReviews, err = s.store.ListSolutionStepReviews(ctx, workspace, episode.ID); err != nil {
		return SolutionActivityDetail{}, err
	}
	if detail.Pinned, err = s.store.SolutionEpisodePinned(ctx, workspace, episode.ID); err != nil {
		return SolutionActivityDetail{}, err
	}
	if detail.Summary != nil {
		detail.PathFeedback, err = s.store.ListHowRetrievalFeedback(ctx, workspace, string(engine.HowTargetPath), detail.Summary.ID, 100)
		if err != nil {
			return SolutionActivityDetail{}, err
		}
	}
	return detail, nil
}

func (s *SolutionService) resolveActivityPromotionTargets(ctx context.Context, workspace string, promotions []core.SolutionPromotion) ([]SolutionActivityPromotionTarget, error) {
	ids := make([]string, 0, len(promotions))
	for _, promotion := range promotions {
		if promotion.Kind == core.SolutionPromotionMemory && promotion.State == core.SolutionPromotionPublished && strings.TrimSpace(promotion.TargetID) != "" {
			ids = append(ids, promotion.TargetID)
		}
	}
	memories, err := s.store.GetMemoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]SolutionActivityPromotionTarget, 0, len(promotions))
	for _, promotion := range promotions {
		item := SolutionActivityPromotionTarget{Promotion: promotion, Availability: string(promotion.State)}
		if promotion.Kind == core.SolutionPromotionMemory && promotion.State == core.SolutionPromotionPublished {
			memory, ok := memories[promotion.TargetID]
			if ok && memory.Workspace == strings.TrimSpace(workspace) {
				copy := memory
				item.Memory = &copy
				item.Availability = "available"
			} else {
				item.Availability = "unavailable"
			}
		}
		result = append(result, item)
	}
	return result, nil
}

type SolutionEpisodePinInput struct {
	Workspace, PrincipalID, EpisodeID string
	Pinned                            bool
}

func (s *SolutionService) SetEpisodePinned(ctx context.Context, input SolutionEpisodePinInput) error {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return err
	}
	if err := s.store.SetSolutionEpisodePinned(ctx, episode.Workspace, episode.ID, input.Pinned, s.now().UTC()); err != nil {
		return err
	}
	s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "", "solution_pin", "success", map[bool]string{true: "pinned", false: "unpinned"}[input.Pinned], episode.ID, nil)
	return nil
}

type SolutionStepReviewInput struct {
	Workspace, PrincipalID, EpisodeID, StepID, Reason string
}

func (s *SolutionService) MarkStepMisleading(ctx context.Context, input SolutionStepReviewInput) error {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return err
	}
	step, err := s.store.GetSolutionStep(ctx, input.StepID)
	if err != nil || step.EpisodeID != episode.ID {
		return errors.New("solution step not authorized")
	}
	reason, err := s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "", engine.SolutionOriginHuman, engine.SolutionFieldRationaleSummary, input.Reason)
	if err != nil {
		return err
	}
	if err := s.store.PutSolutionStepReview(ctx, core.SolutionStepReview{StepID: step.ID, EpisodeID: episode.ID, Workspace: episode.Workspace, Misleading: true, Reason: reason, UpdatedAt: s.now().UTC()}); err != nil {
		return err
	}
	s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "", "solution_step_feedback", "success", "misleading", episode.ID, map[string]any{"step_id": step.ID})
	return nil
}

type SolutionStepRedactInput struct {
	Workspace, PrincipalID, EpisodeID, StepID, ReasonClass string
}

func (s *SolutionService) RedactStep(ctx context.Context, input SolutionStepRedactInput) error {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return err
	}
	reasonClass := strings.TrimSpace(input.ReasonClass)
	switch reasonClass {
	case "user_request", "secret", "privacy", "policy", "incorrect":
	default:
		return errors.New("invalid solution redaction reason class")
	}
	if err := s.store.RedactSolutionStep(ctx, episode.Workspace, episode.ID, input.StepID, "[REDACTED: "+reasonClass+"]", reasonClass, s.now().UTC()); err != nil {
		return errors.New("solution step not authorized")
	}
	s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "", "solution_redact", "success", reasonClass, episode.ID, map[string]any{"step_id": input.StepID})
	return nil
}

type SolutionSummaryCorrectionInput struct {
	Workspace, PrincipalID, EpisodeID, Summary, IdempotencyKey string
}

func (s *SolutionService) CorrectSummary(ctx context.Context, input SolutionSummaryCorrectionInput) (core.SolutionSummary, error) {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	previous, err := s.store.LatestSolutionSummary(ctx, episode.ID)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	corrected, err := s.admit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, input.IdempotencyKey, engine.SolutionOriginHuman, engine.SolutionFieldFinalSummary, input.Summary)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	hash := sha256.Sum256([]byte(previous.ID + "\x00" + corrected))
	reviews, err := s.store.ListSolutionStepReviews(ctx, episode.Workspace, episode.ID)
	if err != nil {
		return core.SolutionSummary{}, err
	}
	redacted := make(map[string]struct{})
	for _, review := range reviews {
		if review.Redacted {
			redacted[review.StepID] = struct{}{}
		}
	}
	summary, _, err := s.store.CreateSolutionSummary(ctx, sqlite.SolutionSummaryInsert{
		EpisodeID: episode.ID, ExpectedEpisodeVersion: episode.Version, Outcome: previous.Outcome, Summary: corrected,
		DecisiveStepIDs: filterRedactedSolutionIDs(previous.DecisiveStepIDs, redacted), UsefulFailureStepIDs: filterRedactedSolutionIDs(previous.UsefulFailureStepIDs, redacted), Evidence: filterRedactedSolutionEvidence(previous.Evidence, redacted),
		Risks: previous.Risks, NextGuidance: previous.NextGuidance, Validation: core.SolutionValidationVerified,
		SnapshotHash: hex.EncodeToString(hash[:]), IdempotencyKey: input.IdempotencyKey, CreatedAt: s.now().UTC(),
	})
	if err == nil {
		s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, input.IdempotencyKey, "solution_correct", "success", "human_review", episode.ID, map[string]any{"summary_id": summary.ID, "summary_version": summary.Version})
	}
	return summary, err
}

func filterRedactedSolutionIDs(ids []string, redacted map[string]struct{}) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, found := redacted[id]; !found {
			result = append(result, id)
		}
	}
	return result
}

func filterRedactedSolutionEvidence(evidence []core.SolutionReference, redacted map[string]struct{}) []core.SolutionReference {
	result := make([]core.SolutionReference, 0, len(evidence))
	for _, reference := range evidence {
		if reference.Kind == core.SolutionReferenceStep {
			if _, found := redacted[reference.TargetID]; found {
				continue
			}
		}
		result = append(result, reference)
	}
	return result
}

type SolutionEpisodeSupersedeInput struct {
	Workspace, PrincipalID, EpisodeID, SuccessorEpisodeID string
}

func (s *SolutionService) SupersedeEpisode(ctx context.Context, input SolutionEpisodeSupersedeInput) error {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return err
	}
	successor, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.SuccessorEpisodeID)
	if err != nil || successor.ID == episode.ID || !episode.Status.Terminal() || !successor.Status.Terminal() {
		return errors.New("invalid solution episode successor")
	}
	if err := s.store.SupersedeSolutionEpisode(ctx, episode.Workspace, episode.ID, successor.ID, s.now().UTC()); err != nil {
		return err
	}
	s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "", "solution_supersede", "success", "human_review", episode.ID, map[string]any{"successor_episode_id": successor.ID})
	return nil
}

type SolutionEpisodeDeleteInput struct {
	Workspace, PrincipalID, EpisodeID, Reason string
}

func (s *SolutionService) DeleteEpisode(ctx context.Context, input SolutionEpisodeDeleteInput) error {
	episode, err := s.authorizedEpisode(ctx, input.Workspace, input.PrincipalID, input.EpisodeID)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "user_request"
	}
	if err := s.store.DeleteSolutionEpisode(ctx, episode.Workspace, episode.ID); err != nil {
		return err
	}
	s.audit(ctx, episode.Workspace, episode.SessionID, episode.PrincipalID, episode.ClientID, "", "solution_delete", "success", reason, episode.ID, nil)
	return nil
}
