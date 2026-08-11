package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/supportevidence"
)

func TestRunReportsReadyAndUnreadyWithoutSensitiveFields(t *testing.T) {
	for name, ready := range map[string]bool{"ready": true, "unready": false} {
		t.Run(name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			path := filepath.Join(t.TempDir(), "receipt.json")
			code := runWithDependencies([]string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "e", "--receipt", path}, &out, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(string, string, string, string, string, time.Time) (supportevidence.Receipt, error) {
				return supportevidence.Receipt{Input: supportevidence.Input{Ready: ready, RequiredCoverageMinutes: 60, PrimaryCoveredMinutes: 60, BackupCoveredMinutes: 60, PrimarySlotCount: 1, BackupSlotCount: 1}, Schema: supportevidence.ReceiptSchemaV1, CoverageComplete: ready, DrillResults: make([]supportevidence.DrillResult, 2), CheckCount: 6, PassedCount: 6}, nil
			}})
			want := 0
			if !ready {
				want = 3
			}
			if code != want || stderr.Len() != 0 || strings.Contains(out.String(), "channel") || strings.Contains(out.String(), "owner_slot") {
				t.Fatalf("code=%d out=%s err=%s", code, out.String(), stderr.String())
			}
		})
	}
}
func TestRunRejectsMissingArgsAndCollectorFailure(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := run(nil, &out, &stderr); code != 2 {
		t.Fatalf("code=%d", code)
	}
	stderr.Reset()
	code := runWithDependencies([]string{"--inventory", "i", "--plan", "p", "--change", "c", "--release", "r", "--input", "e", "--receipt", "x"}, &out, &stderr, dependencies{collect: func(string, string, string, string, string, time.Time) (supportevidence.Receipt, error) {
		return supportevidence.Receipt{}, errors.New("failed")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "failed") {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
}
