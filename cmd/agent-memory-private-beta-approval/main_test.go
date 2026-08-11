package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/approvalexportevidence"
)

func TestRunReportsReadyAndValidUnreadyAggregates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		receipt approvalexportevidence.PrivateBetaReceipt
		code    int
	}{{"ready", approvalexportevidence.PrivateBetaReceipt{PrivateBetaInput: approvalexportevidence.PrivateBetaInput{Ready: true}, ApprovalArtifactCount: 5, RequiredControlCount: 5, VerifiedControlCount: 5, CheckCount: 9, PassedCount: 9}, 0}, {"unready", approvalexportevidence.PrivateBetaReceipt{PrivateBetaInput: approvalexportevidence.PrivateBetaInput{Ready: false}, ApprovalArtifactCount: 4, RequiredControlCount: 5, VerifiedControlCount: 4, MissingControlCount: 1, CheckCount: 9, PassedCount: 7, FailedCount: 1, InconclusiveCount: 1}, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _, _, _, _ string, _ time.Time) (approvalexportevidence.PrivateBetaReceipt, error) {
				return tc.receipt, nil
			}})
			if code != tc.code || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"receipt_written":true`) || strings.Contains(stdout.String(), "review_id") || strings.Contains(stdout.String(), "owner") {
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
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _, _, _, _ string, _ time.Time) (approvalexportevidence.PrivateBetaReceipt, error) {
		return approvalexportevidence.PrivateBetaReceipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid evidence") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--security-closure", "security.json", "--alert-routing", "alert.json", "--blocker-review", "blocker.json", "--capacity-economics", "capacity.json", "--approver-keys", "trust.json", "--approvals-dir", "approvals", "--export-manifest", "manifest.json", "--input", "input.json", "--receipt", t.TempDir() + "/receipt.json"}
}
