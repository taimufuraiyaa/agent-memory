package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrievalrisk"
)

func TestRunPublishesAggregateReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "receipt.json")
	code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return now }, collect: func(_, _, _, _, _ string, at time.Time) (retrievalrisk.Receipt, error) {
		if !at.Equal(now) {
			t.Fatal("collection time not forwarded")
		}
		return retrievalrisk.Receipt{Schema: retrievalrisk.ReceiptSchemaV1, Ready: true, DomainCount: 7, PassedCount: 7, TenantCount: 2, CaseCount: 500, TimingSampleCountPerClass: 80, MaximumTimingDeltaMicroseconds: 10000, ObservedTimingDeltaMicroseconds: 4500}, nil
	}})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Ready || !result.ReceiptWritten || result.DomainCount != 7 || result.CaseCount != 500 || result.ObservedTimingDeltaMicroseconds != 4500 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("inventory.json")) {
		t.Fatal("aggregate output leaked a path")
	}
}

func TestRunReturnsThreeForUnreadyAndSeparatesUsageAndEvidenceFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (retrievalrisk.Receipt, error) {
		return retrievalrisk.Receipt{Schema: retrievalrisk.ReceiptSchemaV1, DomainCount: 7, PassedCount: 6, FailedCount: 1, RiskBreachCount: 1}, nil
	}})
	if code != 3 {
		t.Fatalf("unready code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (retrievalrisk.Receipt, error) {
		return retrievalrisk.Receipt{}, errors.New("unsafe evidence")
	}}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("failure code=%d stdout=%s", code, stdout.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--release", "release.json", "--review", "review.json", "--receipt", receipt}
}
