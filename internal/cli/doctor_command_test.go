package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorReturnsAllChecksAsJSONWithoutRepairing(t *testing.T) {
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", t.TempDir(), "--workspace", "missing", "--service-url", "http://127.0.0.1:1", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	data := envelope["data"].(map[string]any)
	checks := data["checks"].([]any)
	if len(checks) < 9 {
		t.Fatalf("expected full diagnostic set: %+v", data)
	}
	if data["repaired"] != false {
		t.Fatalf("doctor must be read-only by default: %+v", data)
	}
}

func TestDoctorRepairCreatesWritableDataRootIdempotently(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data")
	for range 2 {
		cmd := NewRootCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", dataDir, "--workspace", "missing", "--service-url", "http://127.0.0.1:1", "--format", "json", "--repair"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("repair: %v", err)
		}
	}
	info, err := os.Stat(dataDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("expected writable repaired root: info=%v err=%v", info, err)
	}
}
