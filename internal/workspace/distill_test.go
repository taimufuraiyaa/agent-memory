package workspace

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

func TestManagerDistill(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()

	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	initOut, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "distill-proj",
		NoRule:      true,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Open the sqlite DB directly and insert memories
	store, err := sqlite.Open(context.Background(), initOut.DBPath)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	defer func() { _ = store.Close() }()

	m1 := &core.MemoryEntry{
		ID:        "mem-1",
		Workspace: "distill-proj",
		Type:      core.ProceduralMemory,
		Content:   "Run 'go test' to verify all test suites",
		CreatedAt: time.Now(),
	}
	m2 := &core.MemoryEntry{
		ID:        "mem-2",
		Workspace: "distill-proj",
		Type:      core.SemanticMemory,
		Content:   "SQLite database file is located in ~/.agent-memory",
		CreatedAt: time.Now(),
	}
	m3 := &core.MemoryEntry{
		ID:        "mem-3",
		Workspace: "distill-proj",
		Type:      core.OutcomeMemory,
		Content:   "Implemented TurboQuant with Walsh-Hadamard Transform (result: success, approach: Pure Go)",
		CreatedAt: time.Now(),
	}

	if err := store.UpsertMemory(context.Background(), m1); err != nil {
		t.Fatalf("upsert procedural: %v", err)
	}
	if err := store.UpsertMemory(context.Background(), m2); err != nil {
		t.Fatalf("upsert semantic: %v", err)
	}
	if err := store.UpsertMemory(context.Background(), m3); err != nil {
		t.Fatalf("upsert outcome: %v", err)
	}

	// Run Distill
	res, err := mgr.Distill(context.Background(), cwd, DistillOptions{
		Workspace:   "distill-proj",
		SkillName:   "memory-distiller",
		Description: "A skill distilled from tests",
		Force:       false,
	})
	if err != nil {
		t.Fatalf("distill failed: %v", err)
	}

	if res == nil {
		t.Fatalf("expected DistillResult, got nil")
	}

	// Verify file was written
	if _, err := os.Stat(res.SkillPath); err != nil {
		t.Fatalf("skill file missing at %s: %v", res.SkillPath, err)
	}

	b, err := os.ReadFile(res.SkillPath)
	if err != nil {
		t.Fatalf("failed to read skill file: %v", err)
	}
	s := string(b)

	// Verify frontmatter and content structure
	if !strings.Contains(s, "name: memory-distiller") {
		t.Fatalf("expected skill name in frontmatter, got: %s", s)
	}
	if !strings.Contains(s, "description: A skill distilled from tests") {
		t.Fatalf("expected description in frontmatter, got: %s", s)
	}
	if !strings.Contains(s, "## Workflows & Checklists") || !strings.Contains(s, "Run 'go test'") {
		t.Fatalf("expected Workflows section, got: %s", s)
	}
	if !strings.Contains(s, "## System Constraints & Facts") || !strings.Contains(s, "SQLite database file") {
		t.Fatalf("expected System Constraints section, got: %s", s)
	}
	if !strings.Contains(s, "## Attempt Outcomes & Learnings") || !strings.Contains(s, "Implemented TurboQuant") {
		t.Fatalf("expected Attempt Outcomes section, got: %s", s)
	}
}
