package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphChangeCommittedWithCanonicalWriteAndCoalesces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	memory := graphChangeMemory("memory-1", "day one")
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMemory(ctx, memory); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM graph_change_journal WHERE workspace=? AND subject_id=?`, memory.Workspace, memory.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("graph changes=%d, want one coalesced event", count)
	}
}

func TestGraphChangeRolledBackWithCanonicalWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	configuration := graphConfigurationFixture()
	if err := store.UpsertGraphConfiguration(ctx, configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_memory_terms BEFORE INSERT ON memory_terms BEGIN SELECT RAISE(ABORT, 'forced rollback'); END`); err != nil {
		t.Fatal(err)
	}
	memory := graphChangeMemory("memory-rollback", "must roll back")
	memory.Keywords = []core.MemoryTerm{{Term: "rollback", Display: "rollback", Source: core.TermSourceExplicit, NormalizationVersion: "v1", ExtractorVersion: "v1"}}
	if err := store.UpsertMemory(ctx, memory); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}

	var memories, changes int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM memories WHERE id=?`, memory.ID).Scan(&memories); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM graph_change_journal WHERE subject_id=?`, memory.ID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if memories != 0 || changes != 0 {
		t.Fatalf("rolled-back write left memories=%d changes=%d", memories, changes)
	}
}

func TestGraphChangeTracksSupersedeAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphControlStore(t)
	if err := store.UpsertGraphConfiguration(ctx, graphConfigurationFixture()); err != nil {
		t.Fatal(err)
	}
	for _, memory := range []*core.MemoryEntry{graphChangeMemory("old", "old fact"), graphChangeMemory("new", "new fact")} {
		if err := store.UpsertMemory(ctx, memory); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkSuperseded(ctx, []string{"old"}, "new"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteByIDs(ctx, []string{"new"}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.db.QueryContext(ctx, `SELECT subject_id,change_kind FROM graph_change_journal WHERE change_kind IN ('supersede','delete') ORDER BY subject_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			t.Fatal(err)
		}
		got[id] = kind
	}
	if got["old"] != "supersede" || got["new"] != "delete" {
		t.Fatalf("lifecycle graph changes=%v", got)
	}
}

func graphChangeMemory(id, content string) *core.MemoryEntry {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	return &core.MemoryEntry{ID: id, Type: core.SemanticMemory, Content: content, Workspace: "workspace-a", Source: core.MemorySource{Type: core.SourceAgentObservation}, Confidence: .9, StorageTier: core.TierVector, CreatedAt: now}
}
