package connectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/hooks"
)

type fakeEmitter struct {
	mu     sync.Mutex
	events []hooks.Event
	fail   bool
}

func (e *fakeEmitter) Emit(_ context.Context, event hooks.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail {
		return errors.New("backpressure")
	}
	e.events = append(e.events, event)
	return nil
}

type fakeCheckpoints struct {
	mu    sync.Mutex
	cp    Checkpoint
	saves int
}

func (s *fakeCheckpoints) Load(context.Context, string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cp.ConnectorID == "" {
		return Checkpoint{}, errors.New("missing")
	}
	return s.cp, nil
}
func (s *fakeCheckpoints) Save(_ context.Context, cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cp = cp
	s.saves++
	return nil
}

func TestFilesystemCreateModifyDeleteRestartAndRedaction(t *testing.T) {
	root := t.TempDir()
	emitter := &fakeEmitter{}
	checkpoints := &fakeCheckpoints{}
	connector := NewFilesystem(FilesystemConfig{ID: "files", Workspace: "test", Roots: []string{root}, Ignore: []string{"ignored/*"}, PreviewBytes: 128})
	if err := connector.Validate(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("token sk-12345678901234567890"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := connector.Scan(context.Background(), emitter, checkpoints); err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 1 || emitter.events[0].Kind != "filesystem.create" {
		t.Fatalf("events=%+v", emitter.events)
	}
	if emitter.events[0].Summary == "" || contains(emitter.events[0].Summary, "sk-123") {
		t.Fatalf("secret leaked: %q", emitter.events[0].Summary)
	}
	if err := connector.Scan(context.Background(), emitter, checkpoints); err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("unchanged file duplicated: %d", len(emitter.events))
	}
	time.Sleep(time.Millisecond)
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := connector.Scan(context.Background(), emitter, checkpoints); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := connector.Scan(context.Background(), emitter, checkpoints); err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 3 || emitter.events[1].Kind != "filesystem.modify" || emitter.events[2].Kind != "filesystem.delete" {
		t.Fatalf("events=%+v", emitter.events)
	}
	restarted := NewFilesystem(connector.cfg)
	if err := restarted.Scan(context.Background(), emitter, checkpoints); err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 3 {
		t.Fatal("restart duplicated unchanged state")
	}
}

func TestFilesystemCheckpointAdvancesOnlyAfterAcceptance(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0600)
	emitter := &fakeEmitter{fail: true}
	checkpoints := &fakeCheckpoints{}
	connector := NewFilesystem(FilesystemConfig{ID: "files", Workspace: "test", Roots: []string{root}})
	if err := connector.Scan(context.Background(), emitter, checkpoints); err == nil {
		t.Fatal("expected backpressure error")
	}
	if len(checkpoints.cp.State) != 0 {
		t.Fatalf("checkpoint advanced: %+v", checkpoints.cp.State)
	}
	emitter.fail = false
	if err := connector.Scan(context.Background(), emitter, checkpoints); err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("events=%d", len(emitter.events))
	}
}

func TestManagerIsolatesInvalidConnector(t *testing.T) {
	root := t.TempDir()
	valid := NewFilesystem(FilesystemConfig{ID: "ok", Workspace: "w", Roots: []string{root}})
	invalid := NewFilesystem(FilesystemConfig{ID: "bad", Workspace: "w"})
	errs := NewManager(invalid, valid).Start(context.Background(), &fakeEmitter{}, &fakeCheckpoints{})
	if errs["bad"] == nil || errs["ok"] != nil {
		t.Fatalf("errors=%v", errs)
	}
	_ = valid.Stop(context.Background())
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
