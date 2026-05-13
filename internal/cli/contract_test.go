package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
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
