package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/programapprovalevidence"
)

func TestRunWritesContentFreeReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	receipt := programapprovalevidence.Receipt{Ready: true, BlockerCategoryCount: 4, DeferredBlockerCount: 4, StaffingDomainCount: 3, CoveredStaffingDomainCount: 3, BetaAccountCap: 100, CheckCount: 10, PassedCount: 10}
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _ string, _ time.Time) (programapprovalevidence.Receipt, error) { return receipt, nil }})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) || strings.Contains(stdout.String(), "review_id") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnreadyEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _ string, _ time.Time) (programapprovalevidence.Receipt, error) {
		return programapprovalevidence.Receipt{Ready: false, BlockerCategoryCount: 4, OpenBlockerCount: 1, StaffingDomainCount: 3, CoveredStaffingDomainCount: 3, CheckCount: 10, PassedCount: 9, FailedCount: 1}, nil
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
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _, _, _ string, _ time.Time) (programapprovalevidence.Receipt, error) {
		return programapprovalevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "invalid evidence") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--inventory", "inventory.json", "--launch-scope-receipt", "scope.json", "--integration-receipt", "integration.json", "--input", "review.json", "--receipt", t.TempDir() + "/receipt.json"}
}
