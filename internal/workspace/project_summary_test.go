package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectListCountsOnlyItsWorkspaceWithoutMigrating(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "shared ? #.db")
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE memories (workspace TEXT NOT NULL); INSERT INTO memories VALUES ('alpha'), ('beta'), ('beta')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manager := writeSummaryRegistry(t, base, []Project{{Name: "alpha", DBPath: path}, {Name: "beta", DBPath: path}})
	items, err := manager.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for i, count := range []int{1, 2} {
		if items[i].MemoryCount != count || summaryStatus(t, items[i]) != "ready" {
			t.Errorf("project summary=%+v", items[i])
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("project discovery changed the database")
	}
}

func TestProjectListUnavailableDoesNotCreateMissingDatabase(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "missing.db")
	manager := writeSummaryRegistry(t, base, []Project{{Name: "missing", DBPath: path}, {Name: "directory", DBPath: base}})
	items, err := manager.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	for _, item := range items {
		if summaryStatus(t, item) != "unavailable" {
			t.Errorf("unreadable database represented as a known zero: %+v", item)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database was created: %v", err)
	}
}

func TestProjectListHonorsCallerCancellation(t *testing.T) {
	manager := writeSummaryRegistry(t, t.TempDir(), []Project{{Name: "missing", DBPath: "missing.db"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery err=%v", err)
	}
}

func writeSummaryRegistry(t *testing.T, base string, projects []Project) *Manager {
	t.Helper()
	raw, err := json.Marshal(Registry{Projects: projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "workspaces.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func summaryStatus(t *testing.T, item ListItem) any {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result["count_status"]
}
