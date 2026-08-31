package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestListCurrentVerifiedSolutionToolLessonsAfterIsBoundedAndFiltersState(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	lessons := []core.SolutionToolLesson{
		{ID: "lesson-a", Workspace: "ws", ToolName: "tool-a", Capability: "capability", Confidence: .9, Validation: core.SolutionValidationVerified, SourceEpisodeIDs: []string{"ep-a"}, SourceEventIDs: []string{"event-a"}, SourceStepIDs: []string{"step-a"}, Version: 1, CreatedAt: now},
		{ID: "lesson-b", Workspace: "ws", ToolName: "tool-b", Capability: "capability", Confidence: .5, Validation: core.SolutionValidationProposed, SourceEpisodeIDs: []string{"ep-b"}, SourceEventIDs: []string{"event-b"}, SourceStepIDs: []string{"step-b"}, Version: 1, CreatedAt: now},
		{ID: "lesson-c", Workspace: "ws", ToolName: "tool-c", Capability: "capability", Confidence: .9, Validation: core.SolutionValidationVerified, SourceEpisodeIDs: []string{"ep-c"}, SourceEventIDs: []string{"event-c"}, SourceStepIDs: []string{"step-c"}, Version: 1, CreatedAt: now},
	}
	for _, lesson := range lessons {
		if _, _, err := store.PutSolutionToolLesson(ctx, lesson); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListCurrentVerifiedSolutionToolLessonsAfter(ctx, "ws", "", 1)
	if err != nil || len(first) != 1 || first[0].ID != "lesson-a" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := store.ListCurrentVerifiedSolutionToolLessonsAfter(ctx, "ws", first[0].ID, 1)
	if err != nil || len(second) != 1 || second[0].ID != "lesson-c" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}
