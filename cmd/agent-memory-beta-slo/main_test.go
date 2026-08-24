package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
)

func TestRunReportsReadyAndUnreadyWithoutSensitiveFields(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			receiptPath := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies([]string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "e", "--receipt", receiptPath}, &stdout, &stderr, dependencies{
				now: func() time.Time { return time.Unix(1, 0) },
				collect: func(string, string, string, string, string, time.Time) (betasloevidence.Receipt, error) {
					return betasloevidence.Receipt{Input: betasloevidence.Input{Ready: ready}, Schema: betasloevidence.ReceiptSchemaV1, CoverageComplete: ready, ObservationDurationSeconds: 86_400, MetricResults: make([]betasloevidence.MetricResult, 6), CheckCount: 6, PassedCount: 6}, nil
				},
			})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "evidence_sha256") || strings.Contains(stdout.String(), "observation_id") {
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
	code := runWithDependencies([]string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "e", "--receipt", "x"}, &stdout, &stderr, dependencies{
		collect: func(string, string, string, string, string, time.Time) (betasloevidence.Receipt, error) {
			return betasloevidence.Receipt{}, errors.New("failed")
		},
	})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
