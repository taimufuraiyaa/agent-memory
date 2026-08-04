package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	summary, ok := data["summary"].(map[string]any)
	if !ok || summary["total"] != float64(len(checks)) {
		t.Fatalf("expected aggregate summary: %+v", data)
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

func TestDoctorFixCreatesSafeDataLayoutAndReportsBeforeAfter(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data")
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", dataDir, "--workspace", "missing", "--service-url", "http://127.0.0.1:1", "--format", "json", "--fix"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fix: %v", err)
	}
	for _, subdir := range []string{"", "models", "logs", "onnxruntime"} {
		info, err := os.Stat(filepath.Join(dataDir, subdir))
		if err != nil || !info.IsDir() || info.Mode().Perm()&0o700 != 0o700 {
			t.Fatalf("expected repaired directory %q: info=%v err=%v", subdir, info, err)
		}
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	data := envelope["data"].(map[string]any)
	if data["before_summary"] == nil || data["summary"] == nil {
		t.Fatalf("expected before/after summaries: %+v", data)
	}
	if len(data["repairs_applied"].([]any)) != 4 {
		t.Fatalf("expected four applied directory repairs: %+v", data)
	}
}

func TestDoctorFixDryRunPlansWithoutWriting(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data")
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", dataDir, "--workspace", "missing", "--service-url", "http://127.0.0.1:1", "--format", "json", "--fix", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fix dry-run: %v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created data directory: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	data := envelope["data"].(map[string]any)
	if len(data["repairs_planned"].([]any)) != 4 || len(data["repairs_applied"].([]any)) != 0 {
		t.Fatalf("unexpected dry-run repair report: %+v", data)
	}
}

func TestDoctorDryRunRequiresFixMode(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", t.TempDir(), "--dry-run"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--dry-run requires --fix or --repair") {
		t.Fatalf("expected dry-run validation error, got %v", err)
	}
}
