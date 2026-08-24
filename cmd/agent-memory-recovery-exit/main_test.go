package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/recoveryexitevidence"
)

func TestRunReportsReadyAndUnreadyWithoutPrivateMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		receipt recoveryexitevidence.Receipt
		code    int
	}{
		{"ready", recoveryexitevidence.Receipt{Ready: true, SubjectCount: 11, OperationCount: 44, PassedOperationCount: 44, CheckCount: 8, PassedCount: 8}, 0},
		{"unready", recoveryexitevidence.Receipt{Ready: false, SubjectCount: 11, OperationCount: 44, PassedOperationCount: 43, FailedOperationCount: 1, CheckCount: 8, PassedCount: 7, FailedCount: 1}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (recoveryexitevidence.Receipt, error) { return tc.receipt, nil }})
			if code != tc.code || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"receipt_written":true`) || strings.Contains(stdout.String(), "review_id") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRejectsUsageAndInvalidEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithDependencies(nil, &stdout, &stderr, dependencies{}); code != 2 {
		t.Fatalf("code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (recoveryexitevidence.Receipt, error) {
		return recoveryexitevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid evidence") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--inventory", "inventory.json", "--input", "review.json", "--receipt", t.TempDir() + "/receipt.json"}
}
