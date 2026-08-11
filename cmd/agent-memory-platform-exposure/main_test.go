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
	code := run(canonicalArguments(t, canonicalPath(t, "production-private-authority-exposure.example.json")), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready || result.Environment != "production" || result.TargetCount != 7 || result.BlockedCount != 7 || result.ReachableCount != 0 || result.InconclusiveCount != 0 {
		t.Fatalf("unexpected report: %+v", result)
	}
	for _, forbidden := range []string{"replace-with", "sha256", "scanner", "postgres", "address", "/Users/"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("report leaked exposure detail %q", forbidden)
		}
	}
}

func TestRunUsesDistinctExitForValidUnreadyExposure(t *testing.T) {
	contents, err := os.ReadFile(canonicalPath(t, "production-private-authority-exposure.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	reachable := strings.Replace(string(contents), `"outcome": "blocked"`, `"outcome": "reachable"`, 1)
	path := filepath.Join(t.TempDir(), "exposure.json")
	if err := os.WriteFile(path, []byte(reachable), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(canonicalArguments(t, path), &stdout, &stderr); code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.ReachableCount != 1 || result.BlockedCount != 6 {
		t.Fatalf("unexpected unready report: %+v", result)
	}
}

func TestRunRejectsInvalidArgumentsAndReceipt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing arguments code=%d, want 2", code)
	}

	invalid := filepath.Join(t.TempDir(), "exposure.json")
	if err := os.WriteFile(invalid, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(canonicalArguments(t, invalid), &stdout, &stderr); code != 1 {
		t.Fatalf("invalid receipt code=%d, want 1", code)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "replace-with-production-site") {
		t.Fatalf("invalid result leaked detail: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func canonicalArguments(t *testing.T, exposurePath string) []string {
	t.Helper()
	return []string{
		"--inventory", canonicalPath(t, "self-managed-platform-inventory.production.example.json"),
		"--plan", canonicalPath(t, "self-managed-infrastructure-plan.production.example.json"),
		"--change", canonicalPath(t, "self-managed-infrastructure-change.production.example.json"),
		"--exposure", exposurePath,
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
