package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/externalintegrationevidence"
)

func TestRunWritesAggregateReadyReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	receipt := externalintegrationevidence.Receipt{Ready: true, IntegrationCount: 3, DisabledCount: 3, PassedIntegrationCount: 3, CheckCount: 7, PassedCount: 7}
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (externalintegrationevidence.Receipt, error) { return receipt, nil }})
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) || strings.Contains(stdout.String(), "review_id") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunReturnsThreeForValidUnreadyEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (externalintegrationevidence.Receipt, error) {
		return externalintegrationevidence.Receipt{Ready: false, IntegrationCount: 3, EnabledCount: 1, DisabledCount: 2, PassedIntegrationCount: 2, InconclusiveIntegrationCount: 1, CheckCount: 7, PassedCount: 5, InconclusiveCount: 2}, nil
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
	code := runWithDependencies(validArgs(t), &stdout, &stderr, dependencies{collect: func(_, _ string, _ time.Time) (externalintegrationevidence.Receipt, error) {
		return externalintegrationevidence.Receipt{}, errors.New("invalid evidence")
	}})
	if code != 1 || !strings.Contains(stderr.String(), "invalid evidence") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func validArgs(t *testing.T) []string {
	return []string{"--inventory", "inventory.json", "--input", "review.json", "--receipt", t.TempDir() + "/receipt.json"}
}
