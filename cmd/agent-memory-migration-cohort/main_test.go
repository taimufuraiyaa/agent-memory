package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/migrationcohortevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	receipt := migrationcohortevidence.Receipt{Ready: true, FormatCoverageComplete: true, SizeCoverageComplete: true, ReconciliationComplete: true, AccountCount: 3, LibraryCount: 4, SourceCount: 12, ExpectedItemCount: 40, FailedItemCount: 0, CheckCount: 9, PassedCount: 9}
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{now: func() time.Time { return time.Unix(1, 0) }, collect: func(_, _, _, _, _ string, _ time.Time) (migrationcohortevidence.Receipt, error) { return receipt, nil }})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) || strings.Contains(stdout.String(), "cohort_id") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnreadyEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (migrationcohortevidence.Receipt, error) {
		return migrationcohortevidence.Receipt{Ready: false, CheckCount: 9, PassedCount: 8, FailedCount: 1}, nil
	}})
	if code != 3 || !strings.Contains(stdout.String(), `"ready":false`) || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunRejectsMissingArgumentsAndCollectionFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithDependencies(nil, &stdout, &stderr, dependencies{}); code != 2 {
		t.Fatalf("missing argument code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (migrationcohortevidence.Receipt, error) {
		return migrationcohortevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "invalid evidence") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	t.Helper()
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--release", "release.json", "--input", "cohort.json", "--receipt", t.TempDir() + "/receipt.json"}
}
