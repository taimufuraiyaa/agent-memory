package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestImportExistingSkillsCreatesActiveRevisionOneWithProvenance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	allowBundleCleanup(t, root)
	skillDir := filepath.Join(root, ".agents", "skills", "safe-release")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: safe-release\ndescription: Run release safety checks\n---\n# Safe release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "check.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutDistilledSkillMetadata(ctx, core.DistilledSkillMetadata{
		ID: "old-metadata", Workspace: "ws", Name: "safe-release", Path: filepath.Join(skillDir, "SKILL.md"),
		MemoryIDs: []string{"memory-1"}, ToolLessonIDs: []string{"lesson-1"}, EpisodeIDs: []string{"episode-1"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := ImportExistingSkills(ctx, store, "ws", root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Skipped != 0 || len(result.Issues) != 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	skills, err := store.ListLogicalSkills(ctx, "ws", 10)
	if err != nil || len(skills) != 1 {
		t.Fatalf("expected one logical skill: %+v %v", skills, err)
	}
	revisions, err := store.ListSkillRevisions(ctx, "ws", skills[0].ID, 10)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("expected one revision: %+v %v", revisions, err)
	}
	if revisions[0].Number != 1 || revisions[0].State != core.SkillRevisionActive || len(revisions[0].Files) != 2 {
		t.Fatalf("unexpected imported revision: %+v", revisions[0])
	}
	if len(revisions[0].SourceMemoryIDs) != 1 || revisions[0].SourceMemoryIDs[0] != "memory-1" || len(revisions[0].SourceEpisodeIDs) != 1 {
		t.Fatalf("expected distilled provenance on revision: %+v", revisions[0])
	}
	activation, err := store.GetSkillActivation(ctx, "ws", SkillDefaultEnvironment, skills[0].ID)
	if err != nil || activation.ActiveRevisionID != revisions[0].ID || activation.Materialization != core.SkillMaterializationReady {
		t.Fatalf("unexpected activation: %+v %v", activation, err)
	}

	replay, err := ImportExistingSkills(ctx, store, "ws", root, time.Now)
	if err != nil || replay.Imported != 0 || replay.Skipped != 1 || len(replay.Issues) != 0 {
		t.Fatalf("expected idempotent replay, got %+v %v", replay, err)
	}
}

func TestImportExistingSkillsReportsUnsafeAndOversizeBundlesWithoutMutation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("unsafe"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkDir := filepath.Join(skillsDir, "linked-skill")
	if err := os.MkdirAll(symlinkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(symlinkDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	largeDir := filepath.Join(skillsDir, "large-skill")
	if err := os.MkdirAll(largeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(largeDir, "SKILL.md"), []byte("---\nname: large-skill\ndescription: large\n---\n"+strings.Repeat("x", 12_001)), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	result, err := ImportExistingSkills(ctx, store, "ws", root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Skipped != 2 || len(result.Issues) != 2 {
		t.Fatalf("expected both unsafe skills to be reported: %+v", result)
	}
	skills, err := store.ListLogicalSkills(ctx, "ws", 10)
	if err != nil || len(skills) != 0 {
		t.Fatalf("unsafe import mutated registry: %+v %v", skills, err)
	}
}
