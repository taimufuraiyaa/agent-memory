package replay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestImporterReportsPartialResultsAndResumes(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := "{\"session_id\":\"s1\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"user\",\"message\":\"fix queue\"}\nnot-json\n{\"session_id\":\"s1\",\"timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"tool_result\",\"tool_name\":\"go test\",\"content\":\"passed\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	importer := NewImporter(store)
	first, err := importer.Import(ctx, ImportOptions{Workspace: "ws", Path: path})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if first.Imported != 2 || first.Malformed != 1 || first.CheckpointLine != 3 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	second, err := importer.Import(ctx, ImportOptions{Workspace: "ws", Path: path})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if second.Imported != 0 || second.ResumedFrom != 3 {
		t.Fatalf("unexpected resumed result: %+v", second)
	}
}

func TestImporterRedactsSecretMarkersFromReplay(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(t.TempDir(), "session.jsonl")
	marker := "sk-INTEGRATIONFIXTURE000000000"
	line := "{\"session_id\":\"secure\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"user\",\"message\":\"" + marker + "\"}\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImporter(store).Import(ctx, ImportOptions{Workspace: "ws", Path: path}); err != nil {
		t.Fatal(err)
	}
	events, err := store.LoadReplayEvents(ctx, "ws", "secure", 10, "")
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if strings.Contains(events[0].Summary, marker) {
		t.Fatalf("secret leaked: %q", events[0].Summary)
	}
}

func TestImporterRejectsSymlinksAndSensitivePaths(t *testing.T) {
	store, _ := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "memory.db"))
	t.Cleanup(func() { _ = store.Close() })
	real := filepath.Join(t.TempDir(), "real.jsonl")
	_ = os.WriteFile(real, []byte("{}\n"), 0o600)
	link := filepath.Join(t.TempDir(), "link.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := NewImporter(store).Import(context.Background(), ImportOptions{Workspace: "ws", Path: link}); err == nil {
		t.Fatal("expected symlink rejection")
	}
	secret := filepath.Join(t.TempDir(), "credentials.jsonl")
	_ = os.WriteFile(secret, []byte("{}\n"), 0o600)
	if _, err := NewImporter(store).Import(context.Background(), ImportOptions{Workspace: "ws", Path: secret}); err == nil {
		t.Fatal("expected sensitive-path rejection")
	}
}
