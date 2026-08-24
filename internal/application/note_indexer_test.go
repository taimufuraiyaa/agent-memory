package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestChunkNoteUsesHeadingsAndStableLineRanges(t *testing.T) {
	body := "# Launch\n\nOpening context.\n\n## Decision\n\nUse the notebook model.\n\n## Risks\n\nKeep provenance."
	first := ChunkNote(body, 80)
	second := ChunkNote(body, 80)
	if len(first) < 2 {
		t.Fatalf("expected heading-oriented chunks, got %+v", first)
	}
	if len(first) != len(second) {
		t.Fatalf("chunking must be deterministic: %+v vs %+v", first, second)
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("chunk %d changed between identical inputs", index)
		}
		if first[index].StartLine < 1 || first[index].EndLine < first[index].StartLine {
			t.Fatalf("invalid line range: %+v", first[index])
		}
	}
}

func TestNoteIndexingReplacesPriorRevisionChunks(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	writer := engine.NewWritePipeline(store)
	service := NewNoteService(store, writer)

	note, err := service.Create(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Launch.md",
		Title:     "Launch",
		Body:      "# Launch\n\nThe original launch decision uses blue.",
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if err := service.IndexLatest(ctx, "ws", note.ID); err != nil {
		t.Fatalf("index first revision: %v", err)
	}
	firstMappings, err := store.ListActiveNoteChunks(ctx, "ws", note.ID)
	if err != nil || len(firstMappings) == 0 {
		t.Fatalf("first active mappings: %+v, %v", firstMappings, err)
	}

	updated, err := service.Update(ctx, core.UpdateNoteInput{
		Workspace:        "ws",
		NoteID:           note.ID,
		ExpectedRevision: note.Revision,
		Path:             note.Path,
		Title:            note.Title,
		Body:             "# Launch\n\nThe approved launch decision uses green.",
	})
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if err := service.IndexLatest(ctx, "ws", note.ID); err != nil {
		t.Fatalf("index second revision: %v", err)
	}
	secondMappings, err := store.ListActiveNoteChunks(ctx, "ws", note.ID)
	if err != nil || len(secondMappings) == 0 {
		t.Fatalf("second active mappings: %+v, %v", secondMappings, err)
	}
	for _, mapping := range secondMappings {
		if mapping.Revision != updated.Revision {
			t.Fatalf("stale revision remained active: %+v", mapping)
		}
	}
	for _, old := range firstMappings {
		if _, err := store.GetMemory(ctx, old.MemoryID); err == nil {
			t.Fatalf("old derived memory %s still exists", old.MemoryID)
		}
	}
	indexed, err := service.Get(ctx, "ws", note.ID)
	if err != nil {
		t.Fatalf("get indexed note: %v", err)
	}
	if indexed.IndexState != core.NoteIndexReady || indexed.IndexedRevision != updated.Revision {
		t.Fatalf("unexpected index state: %+v", indexed)
	}

	memories, err := store.ListMemoriesByWorkspace(ctx, "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	for _, memory := range memories {
		if strings.Contains(memory.Content, "original launch decision") {
			t.Fatalf("stale note content remained retrievable: %+v", memory)
		}
		if memory.Source.NoteID != note.ID || memory.Source.NoteRevision != updated.Revision {
			t.Fatalf("note provenance missing: %+v", memory.Source)
		}
	}
}
