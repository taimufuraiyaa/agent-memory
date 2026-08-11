package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/migrationacceptanceevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	receipt := migrationacceptanceevidence.Receipt{Ready: true, CheckCount: 8, PassedCount: 8}
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _ string, _ time.Time) (migrationacceptanceevidence.Receipt, error) { return receipt, nil }})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) || strings.Contains(stdout.String(), "acceptance_id") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnreadyEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _ string, _ time.Time) (migrationacceptanceevidence.Receipt, error) {
		return migrationacceptanceevidence.Receipt{Ready: false, CheckCount: 8, PassedCount: 7, FailedCount: 1}, nil
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
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _ string, _ time.Time) (migrationacceptanceevidence.Receipt, error) {
		return migrationacceptanceevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "invalid evidence") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--cohort", "cohort.json", "--parity", "parity.json", "--input", "acceptance.json", "--receipt", t.TempDir() + "/receipt.json"}
}
