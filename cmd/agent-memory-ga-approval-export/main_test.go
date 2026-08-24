package main

import (
	"bytes"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/approvalexportevidence"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunReportsOnlyAggregateState(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, string, string, string, string, time.Time) (approvalexportevidence.GAReceipt, error) {
				return approvalexportevidence.GAReceipt{GAInput: approvalexportevidence.GAInput{Ready: ready}, Schema: approvalexportevidence.GAReceiptSchemaV1, ApprovalArtifactCount: 5, RequiredControlCount: 5, VerifiedControlCount: 5, CheckCount: 8, PassedCount: 8}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "key_id") || strings.Contains(stdout.String(), "signature") || strings.Contains(stdout.String(), "evidence_sha256") {
				t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}
func TestRunRejectsUsageAndCollectorFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d", code)
	}
	stderr.Reset()
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "r.json")), &stdout, &stderr, dependencies{collect: func(string, string, string, string, string, string, time.Time) (approvalexportevidence.GAReceipt, error) {
		return approvalexportevidence.GAReceipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
func arguments(receipt string) []string {
	return []string{"--ga-scorecard", "s", "--ga-drills", "d", "--approver-keys", "t", "--approvals-dir", "a", "--export-manifest", "m", "--input", "i", "--receipt", receipt}
}
