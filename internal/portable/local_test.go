package portable

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestBuildLocalExportsMemoryAndNoteWithoutImplicitSourceBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, ctx)
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{ID: "memory-1", Type: core.SemanticMemory, Content: "portable fact", Workspace: "ws", Source: core.MemorySource{Type: core.SourceUserInput}, Confidence: 0.9, StorageTier: core.TierVector}); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if _, err := store.CreateNote(ctx, core.CreateNoteInput{Workspace: "ws", Path: "plan.md", Body: "# Plan\n\nPortable note."}); err != nil {
		t.Fatalf("create note: %v", err)
	}

	bundle, err := BuildLocal(ctx, store, Selection{Workspace: "ws", ExportedAt: time.Unix(100, 0)})
	if err != nil {
		t.Fatalf("build local bundle: %v", err)
	}
	if len(bundle.Memories) != 1 || len(bundle.Notes) != 1 {
		t.Fatalf("unexpected content counts: memories=%d notes=%d", len(bundle.Memories), len(bundle.Notes))
	}
	if bundle.SourceBytesIncluded || len(bundle.SourceObjects) != 0 {
		t.Fatal("source bytes must be excluded unless explicitly selected")
	}
	if err := bundle.VerifyManifest(); err != nil {
		t.Fatalf("verify manifest: %v", err)
	}
}

func TestBuildLocalIncludesOnlySelectedCatalogSourceWithMatchingFingerprint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, ctx)
	dir := t.TempDir()
	body := []byte("# Owned copy\n\nSelected explicitly.")
	sourcePath := filepath.Join(dir, "owned.md")
	if err := os.WriteFile(sourcePath, body, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	seedSource(t, ctx, store, "asset-b", body)

	bundle, err := BuildLocal(ctx, store, Selection{Workspace: "ws", SourceFiles: map[string]string{"asset-b": sourcePath}})
	if err != nil {
		t.Fatalf("build local bundle: %v", err)
	}
	if !bundle.SourceBytesIncluded || len(bundle.Sources) != 1 || len(bundle.SourceObjects) != 1 {
		t.Fatalf("unexpected source export: %+v", bundle)
	}
	decoded, err := base64.StdEncoding.DecodeString(bundle.SourceObjects[0].BytesBase64)
	if err != nil || string(decoded) != string(body) {
		t.Fatalf("source bytes mismatch: body=%q err=%v", decoded, err)
	}
	if err := bundle.VerifyManifest(); err != nil {
		t.Fatalf("verify manifest: %v", err)
	}

	if err := os.WriteFile(sourcePath, []byte("changed after cataloging"), 0o600); err != nil {
		t.Fatalf("change source: %v", err)
	}
	if _, err := BuildLocal(ctx, store, Selection{Workspace: "ws", SourceFiles: map[string]string{"asset-b": sourcePath}}); err == nil {
		t.Fatal("expected changed source fingerprint to be rejected")
	}
}

func openTestStore(t *testing.T, ctx context.Context) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "portable.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedSource(t *testing.T, ctx context.Context, store *sqlite.Store, assetID string, body []byte) {
	t.Helper()
	work := library.BookWork{ID: "work-" + assetID, Title: "Owned copy", NormalizedTitle: "owned copy"}
	edition := library.BookEdition{ID: "edition-" + assetID, WorkID: work.ID, Label: "User copy", Language: "en", ContentFingerprint: core.FingerprintText(string(body))}
	asset := library.SourceAsset{ID: assetID, EditionID: edition.ID, Format: library.FormatMarkdown, ByteFingerprint: core.FingerprintText(string(body)), NormalizedFingerprint: core.FingerprintText(string(body)), ParserVersion: "markdown-v1", Policy: core.SourcePolicy{Retention: core.RetentionRetained, StoreOriginal: true, StoreNormalized: true, AllowSearch: true}, ImportedAt: time.Now().UTC()}
	if err := store.PutBookWork(ctx, work); err != nil {
		t.Fatalf("put work: %v", err)
	}
	if err := store.PutBookEdition(ctx, edition); err != nil {
		t.Fatalf("put edition: %v", err)
	}
	if err := store.PutSourceAsset(ctx, asset); err != nil {
		t.Fatalf("put source asset: %v", err)
	}
}
