package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestNoteLifecyclePreservesRevisionsAndUsesOptimisticConcurrency(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	created, err := store.CreateNote(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Projects/Launch plan.md",
		Title:     "Launch plan",
		Body:      "# Launch plan\n\nInitial decision.",
		Properties: map[string]any{
			"status": "draft",
			"tags":   []any{"launch", "planning"},
		},
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if created.Revision != 1 || created.IndexState != core.NoteIndexPending {
		t.Fatalf("unexpected created note: %+v", created)
	}

	updated, err := store.UpdateNote(ctx, core.UpdateNoteInput{
		Workspace:        "ws",
		NoteID:           created.ID,
		ExpectedRevision: 1,
		Path:             "Projects/Launch plan.md",
		Title:            "Launch plan",
		Body:             "# Launch plan\n\nApproved decision.",
		Properties:       created.Properties,
	})
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if updated.Revision != 2 || updated.Body == created.Body {
		t.Fatalf("unexpected updated note: %+v", updated)
	}

	_, err = store.UpdateNote(ctx, core.UpdateNoteInput{
		Workspace:        "ws",
		NoteID:           created.ID,
		ExpectedRevision: 1,
		Path:             "Projects/Launch plan.md",
		Title:            "Launch plan",
		Body:             "stale edit",
	})
	if !errors.Is(err, ErrNoteRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	revisions, err := store.ListNoteRevisions(ctx, "ws", created.ID)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Revision != 2 || revisions[1].Revision != 1 {
		t.Fatalf("unexpected revisions: %+v", revisions)
	}
}

func TestNoteTitleFollowsTheFirstBodyLineWithoutRenamingItsPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	created, err := store.CreateNote(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Untitled.md",
		Title:     "Ignored request title",
		Body:      "# Project north star\n\nInitial notes.",
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if created.Title != "Project north star" {
		t.Fatalf("created title = %q, want first body line", created.Title)
	}

	updated, err := store.UpdateNote(ctx, core.UpdateNoteInput{
		Workspace:        "ws",
		NoteID:           created.ID,
		ExpectedRevision: created.Revision,
		Path:             created.Path,
		Title:            created.Title,
		Body:             "Customer discovery\n\nUpdated notes.",
	})
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if updated.Title != "Customer discovery" {
		t.Fatalf("updated title = %q, want first body line", updated.Title)
	}
	if updated.Path != "Untitled.md" {
		t.Fatalf("updated path = %q, want stable path", updated.Path)
	}
}

func TestNotePathUniquenessTrashAndRestore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	first, err := store.CreateNote(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Meeting.md",
		Title:     "Meeting",
		Body:      "first",
	})
	if err != nil {
		t.Fatalf("create first note: %v", err)
	}
	if _, err := store.CreateNote(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Meeting.md",
		Title:     "Duplicate",
		Body:      "duplicate",
	}); !errors.Is(err, ErrNotePathConflict) {
		t.Fatalf("expected path conflict, got %v", err)
	}

	trashed, err := store.TrashNote(ctx, "ws", first.ID)
	if err != nil {
		t.Fatalf("trash note: %v", err)
	}
	if trashed.DeletedAt == nil || trashed.IndexState != core.NoteIndexRetired {
		t.Fatalf("unexpected trashed note: %+v", trashed)
	}

	active, err := store.ListNotes(ctx, "ws", false)
	if err != nil {
		t.Fatalf("list active notes: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active notes, got %+v", active)
	}

	restored, err := store.RestoreNote(ctx, "ws", first.ID)
	if err != nil {
		t.Fatalf("restore note: %v", err)
	}
	if restored.DeletedAt != nil || restored.IndexState != core.NoteIndexPending {
		t.Fatalf("unexpected restored note: %+v", restored)
	}
}

func TestNoteLinksAreReplacedWithEachRevision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	target, err := store.CreateNote(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Target.md",
		Title:     "Target",
		Body:      "# Target",
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	source, err := store.CreateNote(ctx, core.CreateNoteInput{
		Workspace: "ws",
		Path:      "Source.md",
		Title:     "Source",
		Body:      "See [[Target]] and [[Missing]].",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	backlinks, err := store.ListNoteBacklinks(ctx, "ws", target.ID)
	if err != nil {
		t.Fatalf("list backlinks: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].SourceNoteID != source.ID {
		t.Fatalf("unexpected backlinks: %+v", backlinks)
	}

	if _, err := store.UpdateNote(ctx, core.UpdateNoteInput{
		Workspace:        "ws",
		NoteID:           source.ID,
		ExpectedRevision: 1,
		Path:             source.Path,
		Title:            source.Title,
		Body:             "The link has been removed.",
	}); err != nil {
		t.Fatalf("update source: %v", err)
	}
	backlinks, err = store.ListNoteBacklinks(ctx, "ws", target.ID)
	if err != nil {
		t.Fatalf("list backlinks after update: %v", err)
	}
	if len(backlinks) != 0 {
		t.Fatalf("expected old backlinks to be removed, got %+v", backlinks)
	}
}
