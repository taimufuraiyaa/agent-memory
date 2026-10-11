package jevconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenStoreSaveReplaceLoadAndClear(t *testing.T) {
	base := filepath.Join(t.TempDir(), "agent-memory")
	store := NewTokenStore(base)
	ctx := context.Background()
	configured, err := store.Configured(ctx)
	if err != nil || configured {
		t.Fatalf("initial status = %t, %v", configured, err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("status created directory: %v", err)
	}
	if err := store.Save(ctx, "jev-first-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "jev-replaced-token"); err != nil {
		t.Fatal(err)
	}
	configured, err = store.Configured(ctx)
	if err != nil || !configured {
		t.Fatalf("configured = %t, %v", configured, err)
	}
	token, found, err := store.Load(ctx)
	if err != nil || !found || token != "jev-replaced-token" {
		t.Fatalf("load found=%t err=%v token matched=%t", found, err, token == "jev-replaced-token")
	}
	dirInfo, err := os.Stat(filepath.Join(base, credentialDir))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("credential directory permission = %v, %v", dirInfo, err)
	}
	fileInfo, err := os.Stat(filepath.Join(base, credentialDir, tokenFilename))
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("credential file permission = %v, %v", fileInfo, err)
	}
	if err := store.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	configured, err = store.Configured(ctx)
	if err != nil || configured {
		t.Fatalf("status after clear = %t, %v", configured, err)
	}
}

func TestTokenStoreRejectsUnsafeInputsAndPaths(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), "agent-memory")
	store := NewTokenStore(base)
	for _, token := range []string{"", "contains space", "line\nbreak", strings.Repeat("a", maxTokenBytes+1)} {
		if err := store.Save(ctx, token); err == nil || strings.Contains(err.Error(), token) && token != "" {
			t.Fatal("invalid token accepted or echoed")
		}
	}
	if err := store.Save(ctx, "safe-token"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, credentialDir, tokenFilename)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Configured(ctx); err == nil {
		t.Fatal("accepted loose file permissions")
	}
	if err := store.Save(ctx, "replacement"); err == nil {
		t.Fatal("replaced unsafe file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("other-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.Configured(ctx); err == nil {
		t.Fatal("accepted symlinked credential")
	}
	if err := store.Save(ctx, "replacement"); err == nil {
		t.Fatal("replaced symlinked credential")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(base, credentialDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "replacement"); err == nil {
		t.Fatal("accepted loose directory permissions")
	}
}

func TestTokenStoreHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewTokenStore(filepath.Join(t.TempDir(), "agent-memory"))
	if err := store.Save(ctx, "safe-token"); err == nil {
		t.Fatal("saved after cancellation")
	}
}
