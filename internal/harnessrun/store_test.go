package harnessrun

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleRun(t *testing.T, suffix string) Run {
	t.Helper()
	budget, _ := Budget{}.Normalize()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	return Run{SchemaVersion: schemaVersion, ID: "run_" + strings.Repeat(suffix, 32), State: StateQueued, Generation: 1, Budget: budget, NextSeq: 2,
		Owner:  Owner{ClientID: "claude-desktop", Workspace: "agent-memory", GrantID: strings.Repeat("b", 32), GrantRevision: 1},
		Events: []Event{{Seq: 1, At: now, Kind: "state", State: StateQueued, Code: "queued"}},
		Chunks: []Chunk{{ID: "goal", Kind: "goal", Text: "goal"}}, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
}

func TestStoreRoundTripPermissionsAndAtomicity(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	run := sampleRun(t, "a")
	if err := store.Create(run); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(run); err == nil {
		t.Fatal("Create overwrote an existing run")
	}
	run.State, run.Generation = StateRunning, 2
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(run.ID)
	if err != nil || loaded.State != StateRunning || loaded.Generation != 2 {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(dir, "harness"), 0o700}, {filepath.Join(dir, "harness", "runs"), 0o700}, {filepath.Join(dir, "harness", "idem"), 0o700},
		{filepath.Join(dir, "harness", "runs", run.ID+".json"), 0o600}, {filepath.Join(dir, "harness", "cursor.key"), 0o600},
	} {
		info, err := os.Stat(p.path)
		if err != nil || info.Mode().Perm() != p.mode {
			t.Errorf("%s mode = %v, %v", p.path, info.Mode().Perm(), err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "harness", "runs"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	ids, _ := store.IDs()
	if len(ids) != 1 || ids[0] != run.ID {
		t.Fatalf("ids = %v", ids)
	}
	if err := store.Delete(run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(run.ID); err != ErrNotFound {
		t.Fatalf("after delete = %v", err)
	}
}

func TestStoreRejectsTraversalAndUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(dir)
	for _, id := range []string{"../etc/passwd", "run_x", "", "run_" + strings.Repeat("A", 32), "run_" + strings.Repeat("a", 31)} {
		if _, err := store.Load(id); err != ErrNotFound {
			t.Errorf("Load(%q) = %v", id, err)
		}
		if err := store.Save(Run{ID: id}); err != ErrNotFound {
			t.Errorf("Save(%q) = %v", id, err)
		}
	}
	run := sampleRun(t, "b")
	if err := store.Create(run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "harness", "runs", run.ID+".json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(run.ID); err == nil || err == ErrNotFound {
		t.Fatalf("loose permissions = %v", err)
	}
	_ = os.Chmod(path, 0o600)
	original, _ := os.ReadFile(path)
	for name, content := range map[string]string{
		"truncated":     string(original[:len(original)/2]),
		"unknown field": strings.Replace(string(original), `"state"`, `"extra":1,"state"`, 1),
		"trailing data": string(original) + `{"x":1}`,
		"wrong ID":      strings.Replace(string(original), run.ID, "run_"+strings.Repeat("c", 32), 1),
		"empty":         "",
	} {
		_ = os.Remove(path)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(run.ID); err == nil || err == ErrNotFound {
			t.Errorf("%s = %v", name, err)
		}
	}
	_ = os.Remove(path)
	elsewhere := filepath.Join(t.TempDir(), "x.json")
	_ = os.WriteFile(elsewhere, original, 0o600)
	if err := os.Symlink(elsewhere, path); err == nil {
		if _, err := store.Load(run.ID); err == nil || err == ErrNotFound {
			t.Errorf("symlinked run file = %v", err)
		}
	}
}

func TestOpenStoreRejectsUnsafeDirectories(t *testing.T) {
	loose := t.TempDir()
	if err := os.MkdirAll(filepath.Join(loose, "harness", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(filepath.Join(loose, "harness"), 0o755)
	if _, err := OpenStore(loose); err == nil {
		t.Fatal("accepted a group-readable run directory")
	}
	linked := t.TempDir()
	real := filepath.Join(t.TempDir(), "real")
	_ = os.MkdirAll(real, 0o700)
	if err := os.Symlink(real, filepath.Join(linked, "harness")); err == nil {
		if _, err := OpenStore(linked); err == nil {
			t.Fatal("accepted a symlinked run directory")
		}
	}
	if _, err := OpenStore(""); err == nil {
		t.Fatal("accepted an empty data directory")
	}
}

func TestQuarantineMovesADamagedRunAside(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(dir)
	id := "run_" + strings.Repeat("d", 32)
	path := filepath.Join(dir, "harness", "runs", id+".json")
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Quarantine(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("quarantined file missing: %v", err)
	}
	if ids, _ := store.IDs(); len(ids) != 0 {
		t.Fatalf("quarantined run still listed: %v", ids)
	}
	if err := store.Quarantine(id); err != nil {
		t.Fatalf("quarantine is not idempotent: %v", err)
	}
}

func TestCursorsAreOpaqueSignedAndBoundToRunAndClient(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(dir)
	cursor := store.encodeCursor("run_"+strings.Repeat("a", 32), "claude-desktop", 42)
	if seq, err := store.decodeCursor("run_"+strings.Repeat("a", 32), "claude-desktop", cursor); err != nil || seq != 42 {
		t.Fatalf("decode = %d, %v", seq, err)
	}
	if seq, err := store.decodeCursor("run_x", "claude-desktop", ""); err != nil || seq != 0 {
		t.Fatalf("empty cursor = %d, %v", seq, err)
	}
	for name, args := range map[string][3]string{
		"other run":    {"run_" + strings.Repeat("b", 32), "claude-desktop", cursor},
		"other client": {"run_" + strings.Repeat("a", 32), "codex-cli", cursor},
		"garbage":      {"run_" + strings.Repeat("a", 32), "claude-desktop", "not-base64!"},
		"truncated":    {"run_" + strings.Repeat("a", 32), "claude-desktop", cursor[:10]},
		"flipped":      {"run_" + strings.Repeat("a", 32), "claude-desktop", flipFirst(cursor)},
	} {
		if _, err := store.decodeCursor(args[0], args[1], args[2]); err != ErrInvalidCursor {
			t.Errorf("%s = %v", name, err)
		}
	}
	// The signing key survives a reopen, so cursors outlive a restart.
	again, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if seq, err := again.decodeCursor("run_"+strings.Repeat("a", 32), "claude-desktop", cursor); err != nil || seq != 42 {
		t.Fatalf("cursor after reopen = %d, %v", seq, err)
	}
}

func flipFirst(s string) string {
	if s[0] == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}

func TestIdempotencyIndexHasASingleWinnerUnderContention(t *testing.T) {
	store, _ := OpenStore(t.TempDir())
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := idemEntry{RunID: "run_" + strings.Repeat(string(rune('a'+i%6)), 32), RequestSHA: strings.Repeat("0", 64), CreatedAt: time.Now()}
			_, created, err := store.PutIdem(strings.Repeat("f", 64), entry)
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
}

func TestSweepIdemRemovesStaleAndOrphanedRecords(t *testing.T) {
	store, _ := OpenStore(t.TempDir())
	now := time.Now()
	live := "run_" + strings.Repeat("1", 32)
	gone := "run_" + strings.Repeat("2", 32)
	for name, entry := range map[string]idemEntry{
		strings.Repeat("a", 64): {RunID: live, RequestSHA: strings.Repeat("0", 64), CreatedAt: now},
		strings.Repeat("b", 64): {RunID: gone, RequestSHA: strings.Repeat("0", 64), CreatedAt: now},
		strings.Repeat("c", 64): {RunID: live, RequestSHA: strings.Repeat("0", 64), CreatedAt: now.Add(-48 * time.Hour)},
	} {
		if _, created, err := store.PutIdem(name, entry); err != nil || !created {
			t.Fatal(err)
		}
	}
	removed := store.SweepIdem(now.Add(-24*time.Hour), func(id string) bool { return id == live })
	if removed != 2 {
		t.Fatalf("removed = %d", removed)
	}
	if _, err := store.GetIdem(strings.Repeat("a", 64)); err != nil {
		t.Fatalf("live record was removed: %v", err)
	}
}
