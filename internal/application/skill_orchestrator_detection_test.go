package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillDetectionAdapterRunsQueuedRecurrenceAndPreservesDeduplication(t *testing.T) {
	now := time.Now().UTC()
	configuration := testLessonSignalConfiguration()
	lessons := []core.SolutionToolLesson{
		testVerifiedToolLesson("lesson-1", "ep-1", "event-1", now),
		testVerifiedToolLesson("lesson-2", "ep-2", "event-2", now.Add(time.Second)),
	}
	signal, err := SkillLifecycleSignalForLesson(lessons[0], configuration)
	if err != nil {
		t.Fatal(err)
	}
	routeRepository := &skillSignalRouteRepository{}
	routed, err := NewSkillSignalRouter(routeRepository).Route(context.Background(), signal)
	if err != nil {
		t.Fatal(err)
	}
	repository := &detectionAdapterRepository{
		skillRecurrenceSchedulerRepository: skillRecurrenceSchedulerRepository{
			lessons:  lessons,
			episodes: map[string]core.SolutionEpisode{"ep-1": {ID: "ep-1", Workspace: "ws", PrincipalID: "agent"}, "ep-2": {ID: "ep-2", Workspace: "ws", PrincipalID: "agent"}},
			events: map[string]core.SolutionToolInvocationRecord{
				"event-1": {Kind: core.SolutionToolResult, ResultClass: core.SolutionToolResultSuccess, TaskVerified: true},
				"event-2": {Kind: core.SolutionToolResult, ResultClass: core.SolutionToolResultSuccess, TaskVerified: true},
			},
		},
		workflow: routed.Workflow, lessonsByID: map[string]core.SolutionToolLesson{"lesson-1": lessons[0]},
	}
	adapter, err := NewSkillDetectionAdapter(repository, SkillRecurrencePolicy{}, configuration)
	if err != nil {
		t.Fatal(err)
	}
	first, err := adapter.Execute(context.Background(), routed.Job)
	if err != nil || first.ResultKind != core.SkillJobResultSucceeded || len(first.References) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := adapter.Execute(context.Background(), routed.Job)
	if err != nil || len(second.References) != 1 || second.References[0] != first.References[0] || len(repository.candidates) != 1 {
		t.Fatalf("second=%+v candidates=%d err=%v", second, len(repository.candidates), err)
	}
}

func TestSkillDetectionAdapterFailsClosedForStaleOrUnauthorizedLesson(t *testing.T) {
	now := time.Now().UTC()
	configuration := testLessonSignalConfiguration()
	lesson := testVerifiedToolLesson("lesson-1", "ep-1", "event-1", now)
	signal, _ := SkillLifecycleSignalForLesson(lesson, configuration)
	routed, _ := NewSkillSignalRouter(&skillSignalRouteRepository{}).Route(context.Background(), signal)
	repository := &detectionAdapterRepository{
		skillRecurrenceSchedulerRepository: skillRecurrenceSchedulerRepository{episodes: map[string]core.SolutionEpisode{"ep-1": {ID: "ep-1", Workspace: "other", PrincipalID: "agent"}}},
		workflow:                           routed.Workflow, lessonsByID: map[string]core.SolutionToolLesson{"lesson-1": lesson},
	}
	adapter, _ := NewSkillDetectionAdapter(repository, SkillRecurrencePolicy{}, configuration)
	if _, err := adapter.Execute(context.Background(), routed.Job); err == nil {
		t.Fatal("expected unauthorized source rejection")
	}
	repository.episodes["ep-1"] = core.SolutionEpisode{ID: "ep-1", Workspace: "ws", PrincipalID: "agent"}
	stale := routed.Job
	stale.InputDigest = "sha256:" + strings.Repeat("f", 64)
	if _, err := adapter.Execute(context.Background(), stale); err == nil {
		t.Fatal("expected stale digest rejection")
	}
	lesson.SupersededBy = "lesson-2"
	if _, err := SkillLifecycleSignalForLesson(lesson, configuration); err == nil {
		t.Fatal("expected superseded lesson suppression")
	}
}

func TestSkillLessonParitySweepRepairsMissingAndSkipsExistingJobs(t *testing.T) {
	now := time.Now().UTC()
	lessons := []core.SolutionToolLesson{testVerifiedToolLesson("lesson-a", "ep-1", "event-1", now), testVerifiedToolLesson("lesson-b", "ep-2", "event-2", now)}
	repository := &lessonParityRepository{lessons: lessons}
	routeRepository := &skillSignalRouteRepository{}
	sweep, err := NewSkillLessonParitySweep(repository, NewSkillSignalRouter(routeRepository), testLessonSignalConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	request := SkillReconciliationRequest{Scope: core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}, Domain: core.SkillReconcileLifecycleJobParity, Limit: 10, Now: now}
	first, err := sweep.Sweep(context.Background(), request)
	if err != nil || first.Counters.Scanned != 2 || first.Counters.Repaired != 2 || !first.Complete {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := sweep.Sweep(context.Background(), request)
	if err != nil || second.Counters.Skipped != 2 || routeRepository.routeCount != 2 {
		t.Fatalf("second=%+v routes=%d err=%v", second, routeRepository.routeCount, err)
	}
	repository.err = errors.New("storage unavailable")
	if _, err := sweep.Sweep(context.Background(), request); err == nil {
		t.Fatal("expected source failure")
	}
}

type detectionAdapterRepository struct {
	skillRecurrenceSchedulerRepository
	workflow    core.SkillWorkflow
	lessonsByID map[string]core.SolutionToolLesson
}

func (r *detectionAdapterRepository) GetSkillWorkflow(context.Context, core.SkillOrchestratorScope, string) (core.SkillWorkflow, error) {
	return r.workflow, nil
}
func (r *detectionAdapterRepository) GetSolutionToolLesson(_ context.Context, id string) (core.SolutionToolLesson, error) {
	lesson, ok := r.lessonsByID[id]
	if !ok {
		return core.SolutionToolLesson{}, errors.New("not found")
	}
	return lesson, nil
}

type lessonParityRepository struct {
	lessons []core.SolutionToolLesson
	err     error
}

func (r *lessonParityRepository) ListCurrentVerifiedSolutionToolLessonsAfter(context.Context, string, string, int) ([]core.SolutionToolLesson, error) {
	return r.lessons, r.err
}

func testVerifiedToolLesson(id, episodeID, eventID string, now time.Time) core.SolutionToolLesson {
	return core.SolutionToolLesson{ID: id, Workspace: "ws", ToolName: "safe-tool", Capability: "verify artifact", Confidence: .9,
		Validation: core.SolutionValidationVerified, SourceEpisodeIDs: []string{episodeID}, SourceEventIDs: []string{eventID},
		SourceStepIDs: []string{"step-" + id}, SuccessCount: 1, Version: 1, CreatedAt: now}
}

func testLessonSignalConfiguration() SkillLessonSignalConfiguration {
	return SkillLessonSignalConfiguration{Environment: "production", ConfigurationVersion: 1, PolicyVersion: 1, PolicyDigest: "sha256:" + strings.Repeat("b", 64)}
}
