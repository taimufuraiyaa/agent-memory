package main

import (
	"bytes"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/securityclosureevidence"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunReportsOnlyAggregateReadyAndUnreadyState(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, string, string, string, time.Time) (securityclosureevidence.Receipt, error) {
				return securityclosureevidence.Receipt{Input: securityclosureevidence.Input{Ready: ready}, Schema: securityclosureevidence.ReceiptSchemaV1, CoverageComplete: ready, ExpectedTargetCount: 40, ObservedTargetCount: 40, FindingCount: 3, BlockingFindingCount: 1, SourceCount: 4, CheckCount: 7, PassedCount: 7}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "fingerprint") || strings.Contains(stdout.String(), "evidence_sha256") || strings.Contains(stdout.String(), "review_id") {
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
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "r.json")), &stdout, &stderr, dependencies{collect: func(string, string, string, string, string, time.Time) (securityclosureevidence.Receipt, error) {
		return securityclosureevidence.Receipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
func arguments(receipt string) []string {
	return []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "s", "--receipt", receipt}
}
