package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gascorecardevidence"
)

func TestRunReportsOnlyAggregateReadyAndUnreadyState(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, string, string, string, time.Time) (gascorecardevidence.Receipt, error) {
				return gascorecardevidence.Receipt{Input: gascorecardevidence.Input{Ready: ready}, Schema: gascorecardevidence.ReceiptSchemaV1, ObservationDurationSeconds: 7_776_000, CoverageComplete: ready, RetentionPassed: ready, CheckCount: 7, PassedCount: 7}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "evidence_sha256") || strings.Contains(stdout.String(), "scorecard_id") {
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
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(string, string, string, string, string, time.Time) (gascorecardevidence.Receipt, error) {
		return gascorecardevidence.Receipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "g", "--receipt", receipt}
}
