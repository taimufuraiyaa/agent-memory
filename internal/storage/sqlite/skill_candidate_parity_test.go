package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestListBuildableSkillCandidatesAfterFiltersAndPaginates(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	now := time.Now().UTC()
	for _, item := range []struct {
		id    string
		state core.SkillCandidateState
	}{
		{"candidate-a", core.SkillCandidateProposed}, {"candidate-b", core.SkillCandidateRejected}, {"candidate-c", core.SkillCandidateAccepted},
	} {
		candidate := core.SkillCandidate{ID: item.id, Workspace: "ws", Kind: core.SkillCandidateCreate,
			Summary: "Recurring verified workflow", ExpectedBenefit: "Reduce work", RiskTier: core.SkillRiskLow,
			Confidence: .9, State: item.state, SourceEpisodeIDs: []string{"episode-1", "episode-2"},
			DeduplicationHash: "sha256:" + strings.Repeat(string(item.id[len(item.id)-1]), 64), CreatedBy: "scheduler", CreatedAt: now, UpdatedAt: now}
		if _, _, err := store.PutSkillCandidate(context.Background(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListBuildableSkillCandidatesAfter(context.Background(), "ws", "", 1)
	if err != nil || len(first) != 1 || first[0].ID != "candidate-a" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := store.ListBuildableSkillCandidatesAfter(context.Background(), "ws", first[0].ID, 1)
	if err != nil || len(second) != 1 || second[0].ID != "candidate-c" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}
