package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestWriteCommandJSONEnvelopeParseable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"write",
		"--db", dbPath,
		"--workspace", "ws",
		"--type", "semantic",
		"--content", "hello memory",
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute write: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout must be one parseable JSON object: %v, raw=%q", err, out.String())
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("expected ok=true envelope, got: %s", out.String())
	}
	if command, _ := payload["command"].(string); command != "write" {
		t.Fatalf("expected command=write, got %q", command)
	}
}

func TestExecuteJSONErrorEnvelopeAndExitCode(t *testing.T) {
	oldArgs := os.Args
	oldStdout := os.Stdout
	defer func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	os.Stdout = w
	os.Args = []string{"agent-memory", "write", "--format", "json"}
	code := Execute()
	_ = w.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if code != 2 {
		t.Fatalf("expected usage exit code 2, got %d", code)
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("expected parseable JSON error envelope, got %q: %v", string(b), err)
	}
	if ok, _ := payload["ok"].(bool); ok {
		t.Fatalf("expected ok=false envelope")
	}
}

// TestMapExitCodeTypedSentinels verifies every typed sentinel maps to the correct exit code.
func TestMapExitCodeTypedSentinels(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: bad input", core.ErrInvalidInput), 3},
		{fmt.Errorf("%w: not here", core.ErrNotFound), 4},
		{fmt.Errorf("%w: already there", core.ErrAlreadyExists), 5},
		{core.ErrInvalidInput, 3},
		{core.ErrNotFound, 4},
		{core.ErrAlreadyExists, 5},
		{fmt.Errorf("some random error"), 1},
	}
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			got := mapExitCode(tc.err)
			if got != tc.want {
				t.Errorf("mapExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestInitOnExistingProjectExitsConflict verifies that running init on an
// already-initialized project returns CONFLICT exit code (5).
func TestInitOnExistingProjectExitsConflict(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	dataDir := t.TempDir()
	cwd := t.TempDir()

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// First init succeeds.
	os.Args = []string{"agent-memory", "init", "--base-dir", dataDir, "--no-rule", "--project-name", "test-proj"}
	code := Execute()
	if code != 0 {
		t.Fatalf("first init should succeed, got code %d", code)
	}

	// Second init on same project without --reuse or --force should be CONFLICT.
	os.Args = []string{"agent-memory", "init", "--base-dir", dataDir, "--no-rule", "--project-name", "test-proj"}
	code = Execute()
	if code != 5 {
		t.Fatalf("second init should return CONFLICT (5), got %d", code)
	}
}

// TestTooManyKeywordsExitsValidation verifies that a write with >3 keywords
// exits with VALIDATION error code (3).
func TestTooManyKeywordsExitsValidation(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	dbPath := filepath.Join(t.TempDir(), "memory.db")

	os.Args = []string{
		"agent-memory", "write",
		"--db", dbPath,
		"--workspace", "test-ws",
		"--content", "test content",
		"--keyword", "one",
		"--keyword", "two",
		"--keyword", "three",
		"--keyword", "four",
		"--format", "json",
	}
	code := Execute()
	if code != 3 {
		t.Fatalf("expected VALIDATION (3) for >3 keywords, got %d", code)
	}
}
