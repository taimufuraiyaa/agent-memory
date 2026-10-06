package application

import (
	"context"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillRecurrenceSchedulerRepository interface {
	SkillRecurrenceRepository
	ListCurrentSolutionToolLessons(context.Context, string, int) ([]core.SolutionToolLesson, error)
	GetSolutionToolEvent(context.Context, string) (core.SolutionToolInvocationRecord, error)
	GetSolutionEpisode(context.Context, string) (core.SolutionEpisode, error)
}

type SkillRecurrenceScheduler struct {
	repository     SkillRecurrenceSchedulerRepository
	detector       *SkillRecurrenceDetector
	maximumLessons int
}

func NewSkillRecurrenceScheduler(repository SkillRecurrenceSchedulerRepository, policy SkillRecurrencePolicy) *SkillRecurrenceScheduler {
	return &SkillRecurrenceScheduler{repository: repository, detector: NewSkillRecurrenceDetector(repository, policy), maximumLessons: 100}
}

func (s *SkillRecurrenceScheduler) Run(ctx context.Context, input SkillRecurrenceInput) (SkillRecurrenceResult, error) {
	lessons, err := s.repository.ListCurrentSolutionToolLessons(ctx, input.Workspace, s.maximumLessons)
	if err != nil {
		return SkillRecurrenceResult{}, err
	}
	evidence := make([]SkillRecurrenceEvidence, 0, len(lessons))
	for _, lesson := range lessons {
		item := SkillRecurrenceEvidence{ID: lesson.ID, Workspace: lesson.Workspace, ToolLessonID: lesson.ID, ToolName: lesson.ToolName, Capability: lesson.Capability,
			EpisodeIDs: append([]string(nil), lesson.SourceEpisodeIDs...), Validated: lesson.Validation == core.SolutionValidationVerified,
			Confidence: lesson.Confidence, OccurredAt: lesson.CreatedAt}
		item.Authorized, item.TaskVerified = true, false
		for _, episodeID := range lesson.SourceEpisodeIDs {
			episode, episodeErr := s.repository.GetSolutionEpisode(ctx, episodeID)
			if episodeErr != nil || episode.Workspace != strings.TrimSpace(input.Workspace) || episode.PrincipalID != strings.TrimSpace(input.PrincipalID) {
				item.Authorized = false
				break
			}
		}
		if item.Authorized {
			for _, eventID := range lesson.SourceEventIDs {
				event, eventErr := s.repository.GetSolutionToolEvent(ctx, eventID)
				if eventErr != nil {
					item.Authorized = false
					break
				}
				if event.Kind == core.SolutionToolResult && event.ResultClass == core.SolutionToolResultSuccess && event.TaskVerified {
					item.TaskVerified = true
				}
			}
		}
		evidence = append(evidence, item)
	}
	input.Evidence = evidence
	return s.detector.Detect(ctx, input)
}
