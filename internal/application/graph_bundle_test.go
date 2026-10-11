package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphBundleCreatesPrivateImmutableSignedBundle(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store, err := NewLocalGraphBundleStore(root, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	bundle, err := store.Create(context.Background(), GraphBundleInput{
		Scope: core.GraphScope{WorkspaceID: "workspace-a"}, RevisionID: "revision-a",
		Projection: []byte("{\"id\":\"memory-a\"}\n"), Correlation: []byte("{\"opaque\":\"token\"}\n"),
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyGraphBundleManifest(bundle.Manifest, publicKey); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"projection.jsonl", "correlation.jsonl", "manifest.json"} {
		info, err := os.Stat(filepath.Join(bundle.Path, name))
		if err != nil || info.Mode().Perm() != 0o400 {
			t.Fatalf("%s mode=%v err=%v", name, info.Mode().Perm(), err)
		}
		if err := os.WriteFile(filepath.Join(bundle.Path, name), []byte("replace"), 0o600); err == nil {
			t.Fatalf("immutable file %s was replaced", name)
		}
	}
	if _, err := store.Create(context.Background(), GraphBundleInput{Scope: core.GraphScope{WorkspaceID: "workspace-a"}, RevisionID: "revision-a", Projection: []byte("again"), Correlation: []byte("again"), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err == nil {
		t.Fatal("finalized bundle was replaced")
	}
	removed, err := store.CleanupExpired(context.Background(), now.Add(25*time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("cleanup removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(bundle.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired bundle still exists: %v", err)
	}
}

func TestGraphBundleRejectsSymlinkedWorkspaceAndUnsafeExpiry(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bundles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "bundles", "workspace-a")); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalGraphBundleStore(root, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = store.Create(context.Background(), GraphBundleInput{Scope: core.GraphScope{WorkspaceID: "workspace-a"}, RevisionID: "revision-a", Projection: []byte("x"), Correlation: []byte("y"), CreatedAt: now, ExpiresAt: now.Add(25 * time.Hour)})
	if err == nil {
		t.Fatal("unsafe bundle accepted")
	}
}
