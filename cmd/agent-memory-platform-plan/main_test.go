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
	if code := run([]string{"--inventory", canonicalPath(t, "self-managed-platform-inventory.example.json"), "--plan", canonicalPath(t, "self-managed-infrastructure-plan.example.json")}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready || result.Environment != "staging" || result.CapabilityCount != 21 || result.ToolCount != 2 || result.Actions.NoChange != 21 {
		t.Fatalf("unexpected report: %+v", result)
	}
	for _, forbidden := range []string{"replace-with-plan-id", "replace-with-inventory-id", strings.Repeat("a", 64), "source_revision", "failure_domain"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("report leaked plan detail %q", forbidden)
		}
	}
}

func TestRunUsesDistinctExitForDestructivePlan(t *testing.T) {
	contents, err := os.ReadFile(canonicalPath(t, "self-managed-infrastructure-plan.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	destructive := strings.Replace(string(contents), `"id": "postgres", "action": "no_change"`, `"id": "postgres", "action": "replace"`, 1)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(destructive), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--inventory", canonicalPath(t, "self-managed-platform-inventory.example.json"), "--plan", path}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.Actions.Replace != 1 || result.Actions.NoChange != 20 {
		t.Fatalf("unexpected destructive report: %+v", result)
	}
}

func TestRunRejectsInvalidArgumentsAndPlan(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing arguments code=%d, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	invalid := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(invalid, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--inventory", canonicalPath(t, "self-managed-platform-inventory.example.json"), "--plan", invalid}, &stdout, &stderr); code != 1 {
		t.Fatalf("invalid plan code=%d, want 1", code)
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "replace-with-site-id") {
		t.Fatalf("invalid result leaked detail: stdout=%q stderr=%q", stdout.String(), stderr.String())
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
