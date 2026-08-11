package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchscopeevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	receipt := launchscopeevidence.Receipt{Ready: true, LaunchCountryCount: 2, SupportLanguageCount: 1, NoticeJurisdictionCount: 2, LegalPositionCount: 6, LegalPassedCount: 6, CheckCount: 8, PassedCount: 8}
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_ string, _ time.Time) (launchscopeevidence.Receipt, error) { return receipt, nil }})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) || strings.Contains(stdout.String(), "scope_decision_id") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnreadyEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_ string, _ time.Time) (launchscopeevidence.Receipt, error) {
		return launchscopeevidence.Receipt{Ready: false, LegalPositionCount: 6, LegalPassedCount: 5, LegalFailedCount: 1, CheckCount: 8, PassedCount: 7, FailedCount: 1}, nil
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
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_ string, _ time.Time) (launchscopeevidence.Receipt, error) {
		return launchscopeevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "invalid evidence") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--input", "scope.json", "--receipt", t.TempDir() + "/receipt.json"}
}
