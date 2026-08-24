package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type HowTargetKind string

const (
	HowTargetPath       HowTargetKind = "path"
	HowTargetToolLesson HowTargetKind = "tool_lesson"
	HowTargetProcedure  HowTargetKind = "procedure"
	HowTargetSkill      HowTargetKind = "skill"
)

type HowRecallInput struct {
	Workspace, PrincipalID, SessionID, Task string
	TokenBudget, MaxCandidates              int
}
type HowFeedbackInput struct {
	Workspace  string
	TargetKind HowTargetKind
	TargetID   string
	Outcome    core.RetrievalFeedback
}

type HowPathHit struct {
	Summary         core.SolutionSummary `json:"summary"`
	Score           float64              `json:"score"`
	Confidence      float64              `json:"confidence"`
	Recency         time.Time            `json:"recency"`
	EvidenceQuality float64              `json:"evidence_quality"`
	Provenance      HowPathProvenance    `json:"provenance"`
	Warnings        []string             `json:"warnings,omitempty"`
}
type HowPathProvenance struct {
	EpisodeID            string                   `json:"episode_id"`
	DecisiveStepIDs      []string                 `json:"decisive_step_ids,omitempty"`
	UsefulFailureStepIDs []string                 `json:"useful_failure_step_ids,omitempty"`
	Evidence             []core.SolutionReference `json:"evidence,omitempty"`
}
type HowToolLessonHit struct {
	Lesson   core.SolutionToolLesson `json:"lesson"`
	Score    float64
	Warnings []string `json:"warnings,omitempty"`
}
type HowProcedureHit struct {
	Memory core.MemoryEntry `json:"memory"`
	Score  float64
}
type HowSkillHit struct {
	Skill core.DistilledSkillMetadata `json:"skill"`
	Score float64
}
type HowWarning struct {
	TargetID, Message string
	Score             float64
}
type HowRecallResult struct {
	RequestID               string                     `json:"request_id"`
	CurrentState            *core.SolutionWorkingState `json:"current_state,omitempty"`
	Paths                   []HowPathHit               `json:"paths"`
	ToolLessons             []HowToolLessonHit         `json:"tool_lessons"`
	Procedures              []HowProcedureHit          `json:"procedures"`
	Skills                  []HowSkillHit              `json:"skills"`
	Warnings                []HowWarning               `json:"warnings"`
	ContextBlock            string                     `json:"context_block"`
	TokensUsed, TokenBudget int
}

type HowRecallService struct {
	store *sqlite.Store
	now   func() time.Time
}

func NewHowRecallService(store *sqlite.Store) *HowRecallService {
	return &HowRecallService{store: store, now: time.Now}
}

func (s *HowRecallService) Recall(ctx context.Context, input HowRecallInput) (HowRecallResult, error) {
	if strings.TrimSpace(input.Workspace) == "" || strings.TrimSpace(input.Task) == "" {
		return HowRecallResult{}, errors.New("workspace and task are required for how recall")
	}
	if input.TokenBudget <= 0 {
		input.TokenBudget = 800
	}
	if input.TokenBudget > 32000 {
		input.TokenBudget = 32000
	}
	if input.MaxCandidates <= 0 {
		input.MaxCandidates = 50
	}
	if input.MaxCandidates > 100 {
		input.MaxCandidates = 100
	}
	result := HowRecallResult{RequestID: uuid.NewString(), TokenBudget: input.TokenBudget, Paths: []HowPathHit{}, ToolLessons: []HowToolLessonHit{}, Procedures: []HowProcedureHit{}, Skills: []HowSkillHit{}, Warnings: []HowWarning{}}
	if state, err := s.store.FindSolutionWorkingStateForSession(ctx, input.Workspace, input.PrincipalID, input.SessionID, s.now().UTC()); err == nil {
		result.CurrentState = &state
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	keywords := taskKeywords(input.Task)
	summaries, err := s.store.ListCurrentSolutionSummaries(ctx, input.Workspace, input.MaxCandidates)
	if err != nil {
		return result, err
	}
	for _, candidate := range summaries {
		feedback, err := s.store.HowRetrievalFeedbackOutcome(ctx, input.Workspace, string(HowTargetPath), candidate.Summary.ID)
		if err != nil {
			return result, err
		}
		if feedback == core.FeedbackHarmful || feedback == core.FeedbackRejected || candidate.Summary.Validation == core.SolutionValidationRejected {
			continue
		}
		evidenceQuality, warnings := howEvidenceQuality(candidate.Summary.Evidence)
		confidence := howPathConfidence(candidate.Summary.Validation, evidenceQuality)
		score := keywordOverlap(keywords, candidate.Summary.Summary)*0.5 + evidenceQuality*0.1 + howRecencyScore(candidate.Summary.CreatedAt, s.now().UTC())
		switch candidate.Summary.Outcome {
		case core.OutcomeSuccess:
			score += 1.0
		case core.OutcomePartial:
			score += .65
		default:
			score += .25
		}
		if candidate.Summary.Validation == core.SolutionValidationVerified {
			score += .2
		}
		score += howFeedbackAdjustment(feedback)
		if candidate.Summary.Outcome == core.OutcomeFailure {
			result.Warnings = append(result.Warnings, HowWarning{candidate.Summary.ID, candidate.Summary.Summary, score})
			continue
		}
		result.Paths = append(result.Paths, HowPathHit{
			Summary: candidate.Summary, Score: score, Confidence: confidence, Recency: candidate.Summary.CreatedAt,
			EvidenceQuality: evidenceQuality,
			Provenance: HowPathProvenance{EpisodeID: candidate.Summary.EpisodeID, DecisiveStepIDs: candidate.Summary.DecisiveStepIDs,
				UsefulFailureStepIDs: candidate.Summary.UsefulFailureStepIDs, Evidence: candidate.Summary.Evidence},
			Warnings: warnings,
		})
	}
	sort.SliceStable(result.Paths, func(i, j int) bool {
		if result.Paths[i].Summary.Outcome != result.Paths[j].Summary.Outcome {
			return result.Paths[i].Summary.Outcome == core.OutcomeSuccess
		}
		return result.Paths[i].Score > result.Paths[j].Score
	})
	lessons, err := s.store.ListCurrentSolutionToolLessons(ctx, input.Workspace, input.MaxCandidates)
	if err != nil {
		return result, err
	}
	for _, lesson := range lessons {
		feedback, err := s.store.HowRetrievalFeedbackOutcome(ctx, input.Workspace, string(HowTargetToolLesson), lesson.ID)
		if err != nil {
			return result, err
		}
		if feedback == core.FeedbackHarmful || lesson.Validation == core.SolutionValidationRejected {
			continue
		}
		score := keywordOverlap(keywords, lesson.Capability+" "+lesson.ToolName)*.5 + lesson.Confidence*.3 + howFeedbackAdjustment(feedback)
		warnings := []string{}
		if lesson.Validation != core.SolutionValidationVerified {
			warnings = append(warnings, "Tool lesson is not verified.")
		} else {
			score += .4
		}
		result.ToolLessons = append(result.ToolLessons, HowToolLessonHit{lesson, score, warnings})
	}
	sort.SliceStable(result.ToolLessons, func(i, j int) bool { return result.ToolLessons[i].Score > result.ToolLessons[j].Score })
	memories, err := s.store.ListMemoriesByWorkspace(ctx, input.Workspace)
	if err != nil {
		return result, err
	}
	for _, memory := range memories {
		if memory.Type != core.ProceduralMemory || memory.SuppressionScore >= .8 {
			continue
		}
		feedback, err := s.store.HowRetrievalFeedbackOutcome(ctx, input.Workspace, string(HowTargetProcedure), memory.ID)
		if err != nil {
			return result, err
		}
		if feedback == core.FeedbackHarmful {
			continue
		}
		result.Procedures = append(result.Procedures, HowProcedureHit{memory, keywordOverlap(keywords, memory.Content) + howFeedbackAdjustment(feedback)})
	}
	sort.SliceStable(result.Procedures, func(i, j int) bool { return result.Procedures[i].Score > result.Procedures[j].Score })
	skills, err := s.store.ListDistilledSkillMetadata(ctx, input.Workspace, input.MaxCandidates)
	if err != nil {
		return result, err
	}
	for _, skill := range skills {
		feedback, err := s.store.HowRetrievalFeedbackOutcome(ctx, input.Workspace, string(HowTargetSkill), skill.ID)
		if err != nil {
			return result, err
		}
		if feedback == core.FeedbackHarmful {
			continue
		}
		result.Skills = append(result.Skills, HowSkillHit{skill, keywordOverlap(keywords, skill.Name) + howFeedbackAdjustment(feedback)})
	}
	sort.SliceStable(result.Skills, func(i, j int) bool { return result.Skills[i].Score > result.Skills[j].Score })
	result.ContextBlock, result.TokensUsed = assembleHowRecall(input.Task, result, input.TokenBudget)
	_ = s.store.LogRetrievalRequest(ctx, result.RequestID, input.Workspace, "how", input.Task)
	return result, nil
}

func (s *HowRecallService) Feedback(ctx context.Context, input HowFeedbackInput) error {
	switch input.TargetKind {
	case HowTargetPath:
		summary, err := s.store.GetSolutionSummary(ctx, input.TargetID)
		if err != nil {
			return err
		}
		episode, err := s.store.GetSolutionEpisode(ctx, summary.EpisodeID)
		if err != nil || episode.Workspace != input.Workspace {
			return errors.New("how feedback target not authorized")
		}
	case HowTargetToolLesson:
		lesson, err := s.store.GetSolutionToolLesson(ctx, input.TargetID)
		if err != nil || lesson.Workspace != input.Workspace {
			return errors.New("how feedback target not authorized")
		}
	case HowTargetProcedure:
		memory, err := s.store.GetMemory(ctx, input.TargetID)
		if err != nil || memory.Workspace != input.Workspace {
			return errors.New("how feedback target not authorized")
		}
	case HowTargetSkill:
		if _, err := s.store.GetDistilledSkillMetadata(ctx, input.Workspace, input.TargetID); err != nil {
			return errors.New("how feedback target not authorized")
		}
	default:
		return errors.New("invalid how feedback target kind")
	}
	return s.store.PutHowRetrievalFeedback(ctx, input.Workspace, string(input.TargetKind), input.TargetID, input.Outcome, s.now().UTC())
}

func howFeedbackAdjustment(feedback core.RetrievalFeedback) float64 {
	switch feedback {
	case core.FeedbackHelpful:
		return .2
	case core.FeedbackRejected:
		return -.4
	default:
		return 0
	}
}

func howPathConfidence(validation core.SolutionValidationState, evidenceQuality float64) float64 {
	base := .55
	if validation == core.SolutionValidationVerified {
		base = .85
	}
	return base*.75 + evidenceQuality*.25
}

func howRecencyScore(createdAt, now time.Time) float64 {
	age := now.Sub(createdAt)
	if age < 0 {
		age = 0
	}
	switch {
	case age <= 7*24*time.Hour:
		return .15
	case age <= 30*24*time.Hour:
		return .1
	case age <= 180*24*time.Hour:
		return .05
	default:
		return 0
	}
}

func howEvidenceQuality(evidence []core.SolutionReference) (float64, []string) {
	if len(evidence) == 0 {
		return .5, []string{"No explicit evidence is linked."}
	}
	verified := 0
	warnings := []string{}
	for _, item := range evidence {
		if item.Resolution == core.SolutionReferenceVerified {
			verified++
		}
		if item.Resolution == core.SolutionReferenceTombstoned {
			warnings = append(warnings, "Some referenced evidence was deleted.")
		}
	}
	return float64(verified) / float64(len(evidence)), warnings
}

func assembleHowRecall(task string, result HowRecallResult, budget int) (string, int) {
	var builder strings.Builder
	used := 0
	appendText := func(text string) bool {
		tokens := len(strings.Fields(text))
		if used+tokens > budget {
			return false
		}
		builder.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			builder.WriteByte('\n')
		}
		used += tokens
		return true
	}
	appendText("## Task\n" + strings.TrimSpace(task) + "\n")
	if result.CurrentState != nil {
		appendText("## Current Work\nGoal: " + result.CurrentState.GoalSummary + "\nNext: " + result.CurrentState.NextAction + "\n")
	}
	appendText("## Prior Solution Paths\n")
	for _, hit := range result.Paths {
		if !appendText(fmt.Sprintf("- [%s evidence=%.2f] %s\n", hit.Summary.Outcome, hit.EvidenceQuality, hit.Summary.Summary)) {
			break
		}
	}
	appendText("## Tool Lessons\n")
	for _, hit := range result.ToolLessons {
		if !appendText("- " + hit.Lesson.ToolName + ": " + hit.Lesson.Capability + "; fallback: " + hit.Lesson.Fallback + "\n") {
			break
		}
	}
	appendText("## Procedures and Skills\n")
	for _, hit := range result.Procedures {
		if !appendText("- Procedure: " + hit.Memory.Content + "\n") {
			break
		}
	}
	for _, hit := range result.Skills {
		if !appendText("- Skill: " + hit.Skill.Name + "\n") {
			break
		}
	}
	appendText("## Failed-Approach Warnings\n")
	for _, warning := range result.Warnings {
		if !appendText("- " + warning.Message + "\n") {
			break
		}
	}
	return builder.String(), used
}
