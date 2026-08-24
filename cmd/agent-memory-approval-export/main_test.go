package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/approvalexportevidence"
)

func TestRunReportsOnlyAggregateReadyAndUnreadyState(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, string, string, string, string, time.Time) (approvalexportevidence.Receipt, error) {
				return approvalexportevidence.Receipt{Input: approvalexportevidence.Input{Ready: ready}, Schema: approvalexportevidence.ReceiptSchemaV1, ApprovalArtifactCount: 6, RequiredControlCount: 6, VerifiedControlCount: 6, CheckCount: 8, PassedCount: 8}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "evidence_sha256") || strings.Contains(stdout.String(), "key_id") || strings.Contains(stdout.String(), "signature") {
				t.Fatalf("code=%d out=%s err=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRejectsMissingArgumentsAndCollectorFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d", code)
	}
	stderr.Reset()
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(string, string, string, string, string, string, time.Time) (approvalexportevidence.Receipt, error) {
		return approvalexportevidence.Receipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--launch-assets", "l", "--public-beta-gate", "g", "--approver-keys", "t", "--approvals-dir", "a", "--export-manifest", "m", "--input", "i", "--receipt", receipt}
}
