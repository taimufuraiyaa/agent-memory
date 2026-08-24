package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunEmitsContentFreeReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(canonicalArguments(t, canonicalPath(t, "self-managed-infrastructure-change.example.json")), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready || result.Environment != "staging" || result.Apply != "succeeded" || result.Drift != "clean" || result.CapabilityCount != 21 || result.ResourceCount != 42 {
		t.Fatalf("unexpected report: %+v", result)
	}
	for _, forbidden := range []string{"replace-with-change-id", "replace-with-plan-id", "replace-with-inventory-id", "sha256", "source_revision", "failure_domain", "/Users/"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("report leaked change detail %q", forbidden)
		}
	}
}

func TestRunUsesDistinctExitForValidUnreadyDrift(t *testing.T) {
	contents, err := os.ReadFile(canonicalPath(t, "self-managed-infrastructure-change.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	drifted := strings.Replace(string(contents), `"outcome": "clean"`, `"outcome": "drift_detected"`, 1)
	path := filepath.Join(t.TempDir(), "change.json")
	if err := os.WriteFile(path, []byte(drifted), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(canonicalArguments(t, path), &stdout, &stderr)
	if code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.Drift != "drift_detected" {
		t.Fatalf("unexpected unready report: %+v", result)
	}
}

func TestRunRejectsInvalidArgumentsAndReceipt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing arguments code=%d, want 2", code)
	}

	invalid := filepath.Join(t.TempDir(), "change.json")
	if err := os.WriteFile(invalid, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(canonicalArguments(t, invalid), &stdout, &stderr); code != 1 {
		t.Fatalf("invalid receipt code=%d, want 1", code)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "replace-with-site-id") {
		t.Fatalf("invalid result leaked detail: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func canonicalArguments(t *testing.T, changePath string) []string {
	t.Helper()
	return []string{
		"--inventory", canonicalPath(t, "self-managed-platform-inventory.example.json"),
		"--plan", canonicalPath(t, "self-managed-infrastructure-plan.example.json"),
		"--change", changePath,
	}
}

func canonicalPath(t *testing.T, name string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Join(filepath.Dir(current), "..", "..", "docs", "saas", name)
}
