package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/publicbetagateevidence"
)

func TestRunReportsAggregateReadyAndUnready(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, string, string, string, string, string, string, string, time.Time) (publicbetagateevidence.Receipt, error) {
				return publicbetagateevidence.Receipt{Input: publicbetagateevidence.Input{Ready: ready}, Schema: publicbetagateevidence.ReceiptSchemaV1, CheckCount: 9, PassedCount: 9}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(stdout.String(), "evidence_sha256") || strings.Contains(stdout.String(), "gate_review_id") {
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
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(string, string, string, string, string, string, string, string, string, time.Time) (publicbetagateevidence.Receipt, error) {
		return publicbetagateevidence.Receipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
func arguments(receipt string) []string {
	return []string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--billing", "b", "--beta-slo", "s", "--beta-operations", "o", "--beta-integrity", "g", "--input", "e", "--receipt", receipt}
}
