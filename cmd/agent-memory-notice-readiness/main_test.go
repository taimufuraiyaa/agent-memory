package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/noticereadinessevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	receipt := noticereadinessevidence.Receipt{Ready: true, RouteCount: 2, CoveredRouteCount: 2, StaffingDomainCount: 3, CoveredStaffingDomainCount: 3, ScenarioCount: 4, PassedScenarioCount: 4, CheckCount: 10, PassedCount: 10}
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (noticereadinessevidence.Receipt, error) { return receipt, nil }})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) || strings.Contains(stdout.String(), "review_id") || strings.Contains(stdout.String(), "jurisdiction_ref") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForCompleteUnreadyEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (noticereadinessevidence.Receipt, error) {
		return noticereadinessevidence.Receipt{Ready: false, RouteCount: 2, CoveredRouteCount: 1, StaffingDomainCount: 3, CoveredStaffingDomainCount: 3, ScenarioCount: 4, PassedScenarioCount: 3, FailedScenarioCount: 1, CheckCount: 10, PassedCount: 8, FailedCount: 2}, nil
	}})
	if code != 3 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":false`) {
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
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (noticereadinessevidence.Receipt, error) {
		return noticereadinessevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "invalid evidence") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--launch-scope-receipt", "scope.json", "--input", "review.json", "--receipt", t.TempDir() + "/receipt.json"}
}
