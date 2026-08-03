package sqlite_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestReadingProgressIsEditionSpecific(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.PutBookWork(ctx, library.BookWork{ID: "work", Title: "Book", NormalizedTitle: "book"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"edition-a", "edition-b"} {
		if err := store.PutBookEdition(ctx, library.BookEdition{ID: id, WorkID: "work", Label: id, Language: "en", ContentFingerprint: "fingerprint-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	locator := core.SourceLocator{Kind: core.LocatorMarkdown, Display: "Chapter 1", ParserVersion: "markdown-v1", NormalizationVersion: "text-v1", Text: &core.TextLocator{SourceStart: 0, SourceEnd: 10, NormalizedStart: 0, NormalizedEnd: 10}}
	progress := library.ReadingProgress{PrincipalID: "reader", EditionID: "edition-a", State: library.ReadingStudied, Locator: locator, UpdatedAt: time.Now().UTC()}
	if err := store.PutReadingProgress(ctx, progress); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetReadingProgress(ctx, "reader", "edition-a")
	if err != nil || got.State != library.ReadingStudied {
		t.Fatalf("unexpected progress %+v err=%v", got, err)
	}
	if _, err := store.GetReadingProgress(ctx, "reader", "edition-b"); err == nil {
		t.Fatal("new edition silently inherited progress")
	}
	for _, state := range []library.ReadingState{library.ReadingSeen, library.ReadingStudied, library.ReadingMastered, library.ReadingCompleted} {
		progress.State = state
		if err := progress.Validate(); err != nil {
			t.Fatalf("state %s rejected: %v", state, err)
		}
	}
}
