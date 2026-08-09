package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresOutOfBandApprovalInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"--gate", "ga"}, func(name string) string {
		if name == "AGENT_MEMORY_POSTGRES_URL" {
			return "postgres://unused"
		}
		return ""
	}, &stdout, &stderr)
	if exitCode != 2 || !strings.Contains(stderr.String(), "approver keys") || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestRunRejectsMalformedTrustBeforeDatabaseAccess(t *testing.T) {
	directory := t.TempDir()
	trust := filepath.Join(directory, "trust.json")
	if err := os.WriteFile(trust, []byte(`{"schema":"wrong","keys":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	approvals := filepath.Join(directory, "approvals")
	if err := os.Mkdir(approvals, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"--gate", "ga", "--approver-keys", trust, "--approvals-dir", approvals}, func(string) string { return "postgres://must-not-be-used" }, &stdout, &stderr)
	if exitCode != 1 || !strings.Contains(stderr.String(), "trust bundle") || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}
