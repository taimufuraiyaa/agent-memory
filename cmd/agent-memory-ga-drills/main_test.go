package main

import (
	"bytes"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/gadrillevidence"
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
			code := runWithDependencies([]string{"--ga-scorecard", "s", "--input", "i", "--receipt", path}, &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, time.Time) (gadrillevidence.Receipt, error) {
				return gadrillevidence.Receipt{Input: gadrillevidence.Input{Ready: ready}, Schema: gadrillevidence.ReceiptSchemaV1, DrillCount: 8, ScenarioCount: 4, PassedDrillCount: 8, CheckCount: 7, PassedCheckCount: 7}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "drill_id") || strings.Contains(stdout.String(), "evidence_sha256") {
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
	code := runWithDependencies([]string{"--ga-scorecard", "s", "--input", "i", "--receipt", filepath.Join(t.TempDir(), "r.json")}, &stdout, &stderr, dependencies{collect: func(string, string, time.Time) (gadrillevidence.Receipt, error) {
		return gadrillevidence.Receipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
