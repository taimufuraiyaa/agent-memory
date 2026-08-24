package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// expectDoctorUnhealthy asserts the doctor command signaled failure because
// the workspace is unhealthy. The report is still written; only the exit
// status must be non-zero.
func expectDoctorUnhealthy(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("doctor: expected non-nil error for unhealthy workspace")
	}
	if !strings.Contains(err.Error(), "doctor: workspace unhealthy") {
		t.Fatalf("doctor: unexpected error: %v", err)
	}
}

func TestDoctorReturnsAllChecksAsJSONWithoutRepairing(t *testing.T) {
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", t.TempDir(), "--workspace", "missing", "--service-url", "http://127.0.0.1:1", "--format", "json"})
	err := cmd.Execute()
	expectDoctorUnhealthy(t, err)
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
		err := cmd.Execute()
		expectDoctorUnhealthy(t, err)
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
	err := cmd.Execute()
	expectDoctorUnhealthy(t, err)
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
	err := cmd.Execute()
	expectDoctorUnhealthy(t, err)
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

func TestDoctorHealthyWorkspaceExitsZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // hermetic: no user config or env file leakage
	root := t.TempDir()
	dataDir := t.TempDir()
	workspace := "healthy-ws"

	dbPath := filepath.Join(dataDir, workspace+".db")
	if err := os.WriteFile(dbPath, []byte("sqlite file"), 0o600); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "workspaces.json"), []byte(fmt.Sprintf(`{"projects":[{"name":%q,"db_path":%q}]}`, workspace, dbPath)), 0o600); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	modelDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("model"), 0o600); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"service_mode":"multi_workspace","registered_workspaces":1}}`))
	}))
	defer srv.Close()

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"doctor", "--root", root, "--data-dir", dataDir, "--workspace", workspace, "--service-url", srv.URL, "--model-dir", modelDir, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("healthy doctor must exit zero: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	data := envelope["data"].(map[string]any)
	summary, ok := data["summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected summary in report: %+v", data)
	}
	if healthy, _ := summary["healthy"].(bool); !healthy {
		t.Fatalf("expected healthy summary: %+v", data)
	}
}

func TestDoctorUnhealthyWorkspaceExitsNonZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"doctor", "--root", t.TempDir(), "--data-dir", t.TempDir(), "--workspace", "missing", "--service-url", "http://127.0.0.1:1", "--format", "json"})
	err := cmd.Execute()
	expectDoctorUnhealthy(t, err)
	// The JSON report must still be written before the failure is signaled.
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	data := envelope["data"].(map[string]any)
	summary, ok := data["summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected summary in report: %+v", data)
	}
	if healthy, _ := summary["healthy"].(bool); healthy {
		t.Fatalf("expected unhealthy summary: %+v", data)
	}
}
