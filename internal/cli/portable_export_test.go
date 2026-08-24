package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestPortableExportWritesPrivateEncryptedBundleWithoutSourcesByDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.UpsertMemory(ctx, &core.MemoryEntry{ID: "memory-1", Type: core.SemanticMemory, Content: "portable", Workspace: "ws", Source: core.MemorySource{Type: core.SourceUserInput}, Confidence: 0.9, StorageTier: core.TierVector}); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	outPath := filepath.Join(dir, "workspace.ampb")
	command := newExportCommand()
	command.SetIn(strings.NewReader("a sufficiently long secret\n"))
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"--workspace", "ws", "--db", dbPath, "--export-format", "portable", "--out", outPath, "--passphrase-stdin"})
	if err := command.Execute(); err != nil {
		t.Fatalf("portable export: %v", err)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat bundle: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle permissions=%#o, want 0600", info.Mode().Perm())
	}
	sealed, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	plain, err := exportservice.DecryptPortable("a sufficiently long secret", sealed)
	if err != nil {
		t.Fatalf("decrypt bundle: %v", err)
	}
	var bundle exportservice.Bundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if len(bundle.Memories) != 1 || bundle.SourceBytesIncluded {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
	if err := bundle.VerifyManifest(); err != nil {
		t.Fatalf("verify bundle: %v", err)
	}
}

func TestPortableSourceSelectionParserRejectsAmbiguity(t *testing.T) {
	t.Parallel()
	selected, err := parsePortableSourceSelections([]string{"asset-1=/tmp/owned.pdf"})
	if err != nil || selected["asset-1"] != "/tmp/owned.pdf" {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	for _, values := range [][]string{{"missing-path"}, {"asset-1=/a", "asset-1=/b"}, {"=/tmp/file"}} {
		if _, err := parsePortableSourceSelections(values); err == nil {
			t.Fatalf("expected invalid selection %v to fail", values)
		}
	}
}
