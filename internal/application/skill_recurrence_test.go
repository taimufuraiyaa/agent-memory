package application

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillRecurrenceDetectorCreatesOnlyFromDistinctValidatedEpisodes(t *testing.T) {
	repository := &skillRecurrenceRepository{}
	detector := NewSkillRecurrenceDetector(repository, SkillRecurrencePolicy{MinimumDistinctEpisodes: 2, MinimumConfidence: .7})
	evidence := []SkillRecurrenceEvidence{
		recurrenceEvidence("lesson-1", "ep-1", "artifact verifier", "verify release artifact"),
		recurrenceEvidence("lesson-2", "ep-2", "artifact verifier", "verify release artifact"),
	}
	result, err := detector.Detect(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", Evidence: evidence, CreatedBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Kind != core.SkillCandidateCreate {
		t.Fatalf("unexpected candidates: %+v", result.Candidates)
	}
	if len(result.Candidates[0].SourceEpisodeIDs) != 2 || len(result.Candidates[0].SourceToolLessonIDs) != 2 {
		t.Fatalf("candidate provenance is incomplete: %+v", result.Candidates[0])
	}

	result, err = detector.Detect(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", Evidence: evidence, CreatedBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || !result.Candidates[0].Deduplicated {
		t.Fatalf("expected deterministic candidate replay: %+v", result.Candidates)
	}
}

func TestSkillRecurrenceDetectorRejectsRepetitionOnlyAndUnsafeEvidence(t *testing.T) {
	tests := []struct {
		name     string
		evidence []SkillRecurrenceEvidence
	}{
		{"same episode", []SkillRecurrenceEvidence{recurrenceEvidence("l1", "ep-1", "tool", "repeat action"), recurrenceEvidence("l2", "ep-1", "tool", "repeat action")}},
		{"unauthorized", mutateRecurrence([]SkillRecurrenceEvidence{recurrenceEvidence("l1", "ep-1", "tool", "repeat action"), recurrenceEvidence("l2", "ep-2", "tool", "repeat action")}, func(e *SkillRecurrenceEvidence) { e.Authorized = false })},
		{"suppressed", mutateRecurrence([]SkillRecurrenceEvidence{recurrenceEvidence("l1", "ep-1", "tool", "repeat action"), recurrenceEvidence("l2", "ep-2", "tool", "repeat action")}, func(e *SkillRecurrenceEvidence) { e.Suppressed = true })},
		{"low confidence", mutateRecurrence([]SkillRecurrenceEvidence{recurrenceEvidence("l1", "ep-1", "tool", "repeat action"), recurrenceEvidence("l2", "ep-2", "tool", "repeat action")}, func(e *SkillRecurrenceEvidence) { e.Confidence = .2 })},
		{"not task verified", mutateRecurrence([]SkillRecurrenceEvidence{recurrenceEvidence("l1", "ep-1", "tool", "repeat action"), recurrenceEvidence("l2", "ep-2", "tool", "repeat action")}, func(e *SkillRecurrenceEvidence) { e.TaskVerified = false })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewSkillRecurrenceDetector(&skillRecurrenceRepository{}, SkillRecurrencePolicy{}).Detect(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", Evidence: test.evidence, CreatedBy: "scheduler"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Candidates) != 0 {
				t.Fatalf("unsafe repetition produced candidate: %+v", result.Candidates)
			}
		})
	}
}

func TestSkillRecurrenceDetectorClassifiesReviseMergeAndSplit(t *testing.T) {
	tests := []struct {
		name     string
		skills   []core.LogicalSkill
		evidence []SkillRecurrenceEvidence
		want     core.SkillCandidateKind
	}{
		{"revise", []core.LogicalSkill{recurrenceSkill("skill-1", "artifact verifier", "verify release artifact")}, []SkillRecurrenceEvidence{recurrenceEvidence("l1", "e1", "artifact verifier", "verify release artifact"), recurrenceEvidence("l2", "e2", "artifact verifier", "verify release artifact")}, core.SkillCandidateRevise},
		{"merge", []core.LogicalSkill{recurrenceSkill("skill-1", "artifact verifier", "verify release artifact"), recurrenceSkill("skill-2", "release verifier", "verify release artifact")}, []SkillRecurrenceEvidence{recurrenceEvidence("l1", "e1", "verify", "verify release artifact"), recurrenceEvidence("l2", "e2", "verify", "verify release artifact")}, core.SkillCandidateMerge},
		{"split", []core.LogicalSkill{recurrenceSkill("skill-1", "release operations", "verify release artifact rollback deployment")}, []SkillRecurrenceEvidence{
			recurrenceEvidence("l1", "e1", "verify", "verify release artifact"), recurrenceEvidence("l2", "e2", "verify", "verify release artifact"),
			recurrenceEvidence("l3", "e3", "rollback", "rollback release deployment"), recurrenceEvidence("l4", "e4", "rollback", "rollback release deployment"),
		}, core.SkillCandidateSplit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &skillRecurrenceRepository{skills: test.skills}
			result, err := NewSkillRecurrenceDetector(repository, SkillRecurrencePolicy{MatchThreshold: .3}).Detect(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", Evidence: test.evidence, CreatedBy: "scheduler"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Candidates) != 1 || result.Candidates[0].Kind != test.want {
				t.Fatalf("got %+v, want %s", result.Candidates, test.want)
			}
		})
	}
}

func TestSkillRecurrenceSchedulerAuthorizesSourcesAndBoundsWork(t *testing.T) {
	now := time.Now().UTC()
	repository := &skillRecurrenceSchedulerRepository{
		lessons: []core.SolutionToolLesson{{ID: "lesson-1", Workspace: "ws", ToolName: "safe-tool", Capability: "verify artifact", Confidence: .9,
			Validation: core.SolutionValidationVerified, SourceEpisodeIDs: []string{"ep-1", "ep-2"}, SourceEventIDs: []string{"event-1", "event-2"}, SourceStepIDs: []string{"step-1"}, SuccessCount: 2, Version: 1, CreatedAt: now}},
		episodes: map[string]core.SolutionEpisode{
			"ep-1": {ID: "ep-1", Workspace: "ws", PrincipalID: "agent"}, "ep-2": {ID: "ep-2", Workspace: "ws", PrincipalID: "agent"},
		},
		events: map[string]core.SolutionToolInvocationRecord{
			"event-1": {Kind: core.SolutionToolResult, ResultClass: core.SolutionToolResultSuccess, TaskVerified: true},
			"event-2": {Kind: core.SolutionToolResult, ResultClass: core.SolutionToolResultSuccess, TaskVerified: true},
		},
	}
	scheduler := NewSkillRecurrenceScheduler(repository, SkillRecurrencePolicy{})
	result, err := scheduler.Run(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", CreatedBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("authorized recurrence not detected: %+v", result)
	}
	result, err = scheduler.Run(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "other", CreatedBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("unauthorized recurrence produced candidate: %+v", result)
	}

	evidence := make([]SkillRecurrenceEvidence, 0, 40)
	for index := 0; index < 40; index++ {
		tool := "tool-" + string(rune('a'+index%20))
		episode := "episode-" + string(rune('A'+index))
		evidence = append(evidence, recurrenceEvidence("lesson-"+episode, episode, tool, "repeat capability "+tool))
	}
	bounded, err := NewSkillRecurrenceDetector(&skillRecurrenceRepository{}, SkillRecurrencePolicy{MaximumEvidence: 20, MaximumCandidates: 3}).Detect(context.Background(), SkillRecurrenceInput{Workspace: "ws", PrincipalID: "agent", Evidence: evidence, CreatedBy: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded.Truncated || bounded.ScannedEvidence != 20 || len(bounded.Candidates) > 3 {
		t.Fatalf("scan was not bounded: %+v", bounded)
	}
}

type skillRecurrenceRepository struct {
	skills     []core.LogicalSkill
	candidates map[string]core.SkillCandidate
}

func (r *skillRecurrenceRepository) ListLogicalSkills(context.Context, string, int) ([]core.LogicalSkill, error) {
	return r.skills, nil
}
func (r *skillRecurrenceRepository) PutSkillCandidate(_ context.Context, candidate core.SkillCandidate) (core.SkillCandidate, bool, error) {
	if r.candidates == nil {
		r.candidates = map[string]core.SkillCandidate{}
	}
	if existing, ok := r.candidates[candidate.DeduplicationHash]; ok {
		return existing, true, nil
	}
	r.candidates[candidate.DeduplicationHash] = candidate
	return candidate, false, nil
}

func recurrenceEvidence(id, episode, tool, capability string) SkillRecurrenceEvidence {
	return SkillRecurrenceEvidence{ID: id, Workspace: "ws", ToolLessonID: id, ToolName: tool, Capability: capability, EpisodeIDs: []string{episode}, Validated: true, Authorized: true, TaskVerified: true, Confidence: .9, OccurredAt: time.Now().UTC()}
}
func mutateRecurrence(items []SkillRecurrenceEvidence, mutate func(*SkillRecurrenceEvidence)) []SkillRecurrenceEvidence {
	for index := range items {
		mutate(&items[index])
	}
	return items
}
func recurrenceSkill(id, name, capability string) core.LogicalSkill {
	now := time.Now().UTC()
	return core.LogicalSkill{ID: id, Workspace: "ws", Name: name, Description: capability, Capabilities: []string{capability}, TriggerConditions: []string{capability}, RiskTier: core.SkillRiskLow, OwnerGroup: "platform", Status: core.SkillStatusActive, Generation: 1, CreatedAt: now, UpdatedAt: now}
}

type skillRecurrenceSchedulerRepository struct {
	skillRecurrenceRepository
	lessons  []core.SolutionToolLesson
	events   map[string]core.SolutionToolInvocationRecord
	episodes map[string]core.SolutionEpisode
}

func (r *skillRecurrenceSchedulerRepository) ListCurrentSolutionToolLessons(context.Context, string, int) ([]core.SolutionToolLesson, error) {
	return r.lessons, nil
}
func (r *skillRecurrenceSchedulerRepository) GetSolutionToolEvent(_ context.Context, id string) (core.SolutionToolInvocationRecord, error) {
	return r.events[id], nil
}
func (r *skillRecurrenceSchedulerRepository) GetSolutionEpisode(_ context.Context, id string) (core.SolutionEpisode, error) {
	return r.episodes[id], nil
}
