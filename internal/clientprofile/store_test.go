package clientprofile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLifecyclePersistsAcrossRestart(t *testing.T) {
	baseDir := t.TempDir()
	now := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	store, err := Open(baseDir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	created, err := store.Create(Input{ID: "codex-main", DisplayName: "Codex", ClientKind: KindCodex, ToolProfile: ProfileDefault})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Revision != 1 || !created.CreatedAt.Equal(now) || !created.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected created record: %#v", created)
	}

	now = now.Add(time.Minute)
	updated, err := store.Update("codex-main", created.Revision, Input{DisplayName: "Codex Desktop", ClientKind: KindCodex, ToolProfile: ProfileExpanded})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Revision != 2 || updated.ToolProfile != ProfileExpanded || !updated.CreatedAt.Equal(created.CreatedAt) || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected updated record: %#v", updated)
	}

	reopened, err := Open(baseDir, time.Now)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Get("codex-main")
	if err != nil {
		t.Fatalf("get reopened record: %v", err)
	}
	if got != updated {
		t.Fatalf("reopened record mismatch: got %#v want %#v", got, updated)
	}

	info, err := os.Stat(filepath.Join(baseDir, RegistryFilename))
	if err != nil {
		t.Fatalf("stat registry: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("registry permissions = %o, want 600", info.Mode().Perm())
	}

	if err := reopened.Delete("codex-main", updated.Revision); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := reopened.Get("codex-main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted record error = %v, want ErrNotFound", err)
	}
}

func TestStoreSortsAndRejectsConflicts(t *testing.T) {
	store, err := Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, input := range []Input{
		{ID: "cursor", DisplayName: "Cursor", ClientKind: KindCursor, ToolProfile: ProfileExpanded},
		{ID: "claude", DisplayName: "Claude", ClientKind: KindClaude, ToolProfile: ProfileDefault},
		{ID: "kiro", DisplayName: "Kiro", ClientKind: KindKiro, ToolProfile: ProfileDefault},
	} {
		if _, err := store.Create(input); err != nil {
			t.Fatalf("create %#v: %v", input, err)
		}
	}
	profiles := store.List()
	if len(profiles) != 3 || profiles[0].ID != "claude" || profiles[1].ID != "cursor" || profiles[2].ID != "kiro" {
		t.Fatalf("unexpected order: %#v", profiles)
	}
	if _, err := store.Create(Input{ID: "cursor", DisplayName: "Duplicate", ClientKind: KindOther, ToolProfile: ProfileDefault}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create error = %v, want ErrConflict", err)
	}
	if _, err := store.Update("cursor", 9, Input{DisplayName: "Cursor", ClientKind: KindCursor, ToolProfile: ProfileDefault}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale update error = %v, want ErrRevisionConflict", err)
	}
	if err := store.Delete("cursor", 9); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale delete error = %v, want ErrRevisionConflict", err)
	}
}

func TestStoreValidatesAndPreservesMalformedRegistry(t *testing.T) {
	baseDir := t.TempDir()
	for _, input := range []Input{
		{ID: "UPPER", DisplayName: "Bad", ClientKind: KindCodex, ToolProfile: ProfileDefault},
		{ID: "ok", DisplayName: "", ClientKind: KindCodex, ToolProfile: ProfileDefault},
		{ID: "ok", DisplayName: "Bad kind", ClientKind: "browser", ToolProfile: ProfileDefault},
		{ID: "ok", DisplayName: "Bad profile", ClientKind: KindOther, ToolProfile: "admin"},
	} {
		store, err := Open(baseDir, time.Now)
		if err != nil {
			t.Fatalf("open empty store: %v", err)
		}
		if _, err := store.Create(input); !errors.Is(err, ErrValidation) {
			t.Fatalf("create %#v error = %v, want ErrValidation", input, err)
		}
	}

	path := filepath.Join(baseDir, RegistryFilename)
	malformed := []byte(`{"schema_version":1,"profiles":[`)
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatalf("write malformed registry: %v", err)
	}
	if _, err := Open(baseDir, time.Now); !errors.Is(err, ErrStorage) {
		t.Fatalf("open malformed registry error = %v, want ErrStorage", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read malformed registry: %v", err)
	}
	if string(got) != string(malformed) {
		t.Fatalf("malformed evidence was changed: %q", got)
	}
}
