package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func TestLibraryMigrationAndIdentityRoundTrip(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "library.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	work := library.BookWork{ID: "work-1", Title: "The Book", NormalizedTitle: "the book"}
	edition := library.BookEdition{ID: "edition-1", WorkID: work.ID, Label: "First edition", Language: "en", ContentFingerprint: "sha256:text"}
	asset := library.SourceAsset{
		ID: "asset-1", EditionID: edition.ID, Format: library.FormatMarkdown,
		ByteFingerprint: "sha256:bytes", NormalizedFingerprint: "sha256:text", ParserVersion: "markdown-v1",
		Policy:     core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true},
		ImportedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	nodes := []library.StructuralNode{{ID: "chapter-1", EditionID: edition.ID, Kind: library.NodeChapter, Ordinal: 0, Title: "Chapter 1"}}

	if err := store.PutBookWork(ctx, work); err != nil {
		t.Fatalf("put work: %v", err)
	}
	if err := store.PutBookEdition(ctx, edition); err != nil {
		t.Fatalf("put edition: %v", err)
	}
	if err := store.PutSourceAsset(ctx, asset); err != nil {
		t.Fatalf("put asset: %v", err)
	}
	if err := store.ReplaceStructuralNodes(ctx, edition.ID, nodes); err != nil {
		t.Fatalf("put nodes: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store, err = Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = store.Close() }()

	gotEdition, err := store.GetBookEdition(ctx, edition.ID)
	if err != nil || gotEdition != edition {
		t.Fatalf("edition round trip: got=%+v err=%v", gotEdition, err)
	}
	gotNodes, err := store.ListStructuralNodes(ctx, edition.ID)
	if err != nil || len(gotNodes) != 1 || gotNodes[0].ID != nodes[0].ID {
		t.Fatalf("nodes round trip: got=%+v err=%v", gotNodes, err)
	}
}

func TestLibraryForeignKeysRejectUnknownEdition(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "library-fk.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	asset := library.SourceAsset{
		ID: "asset-1", EditionID: "missing", Format: library.FormatMarkdown,
		ByteFingerprint: "sha256:bytes", NormalizedFingerprint: "sha256:text", ParserVersion: "markdown-v1",
		Policy: core.SourcePolicy{Retention: core.RetentionRetained}, ImportedAt: time.Now().UTC(),
	}
	if err := store.PutSourceAsset(ctx, asset); err == nil {
		t.Fatal("expected unknown edition foreign key to fail")
	}
}
