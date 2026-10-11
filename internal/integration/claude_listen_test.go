package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeListenStoreScopesAndRevokes(t *testing.T) {
	dataDir := t.TempDir()
	root := t.TempDir()
	other := t.TempDir()
	child := filepath.Join(root, "sub")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewClaudeListenStore(dataDir)
	if store.Allows("ws", root, child) {
		t.Fatal("listener defaulted on")
	}
	if _, err := store.Enable("ws", root); err != nil {
		t.Fatal(err)
	}
	if !store.Allows("ws", root, child) || store.Allows("ws", root, other) || store.Allows("ws", other, child) {
		t.Fatal("listener escaped project scope")
	}
	if err := store.Disable("ws"); err != nil {
		t.Fatal(err)
	}
	if store.Allows("ws", root, child) {
		t.Fatal("listener remained enabled")
	}
}

func TestClaudeJevConsentIsSeparateAndRootBound(t *testing.T) {
	store := NewClaudeListenStore(t.TempDir())
	root, other := t.TempDir(), t.TempDir()
	if _, err := store.SetJev("ws", root, true); err == nil {
		t.Fatal("Jev enabled without local listen")
	}
	if _, err := store.Enable("ws", root); err != nil {
		t.Fatal(err)
	}
	if store.AllowsJev("ws", root, root) {
		t.Fatal("Jev defaulted on")
	}
	if _, err := store.SetJev("ws", other, true); err == nil {
		t.Fatal("cross-root Jev consent accepted")
	}
	if _, err := store.SetJev("ws", root, true); err != nil {
		t.Fatal(err)
	}
	if !store.AllowsJev("ws", root, root) || store.AllowsJev("ws", root, other) {
		t.Fatal("Jev consent escaped root")
	}
	if err := store.Disable("ws"); err != nil {
		t.Fatal(err)
	}
	if store.AllowsJev("ws", root, root) {
		t.Fatal("listen-off left Jev enabled")
	}
}
