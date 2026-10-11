package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillCandidateRepositoryPersistsProvenanceAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "candidate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	candidate := core.SkillCandidate{ID: "candidate-1", Workspace: "ws", Kind: core.SkillCandidateCreate,
		Summary: "Recurring verified workflow", ExpectedBenefit: "Reduce repeated work", RiskTier: core.SkillRiskLow, Confidence: .9,
		State: core.SkillCandidateProposed, SourceEpisodeIDs: []string{"episode-1", "episode-2"}, SourceToolLessonIDs: []string{"lesson-1"},
		DeduplicationHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedBy: "scheduler", CreatedAt: now, UpdatedAt: now}
	stored, replay, err := store.PutSkillCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if replay || stored.ID != candidate.ID {
		t.Fatalf("unexpected first insert: %+v replay=%v", stored, replay)
	}
	stored, replay, err = store.PutSkillCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !replay || len(stored.SourceEpisodeIDs) != 2 || len(stored.SourceToolLessonIDs) != 1 {
		t.Fatalf("candidate replay lost provenance: %+v", stored)
	}
	loaded, err := store.GetSkillCandidate(ctx, "ws", candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeduplicationHash != candidate.DeduplicationHash {
		t.Fatalf("candidate mismatch: %+v", loaded)
	}
}
