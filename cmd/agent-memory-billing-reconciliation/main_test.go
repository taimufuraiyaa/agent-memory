package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billingreconciliation"
	"path/filepath"
	"testing"
	"time"
)

func TestRunPublishesAggregateReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "receipt.json")
	code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return now }, collect: func(_, _, _, _, _ string, at time.Time) (billingreconciliation.Receipt, error) {
		if !at.Equal(now) {
			t.Fatal("time not forwarded")
		}
		return billingreconciliation.Receipt{Schema: billingreconciliation.ReceiptSchemaV1, Input: billingreconciliation.Input{Ready: true, TenantSampleCount: 2, ProcessorInvoiceCount: 2, MatchedInvoiceCount: 2, ProcessorSettlementCount: 2, MatchedSettlementCount: 2, UsageSampleCount: 4, MatchedUsageSampleCount: 4}, CoverageComplete: true, CheckCount: 8, PassedCount: 8}, nil
	}})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Ready || !result.ReceiptWritten || !result.CoverageComplete || result.TenantSampleCount != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("inventory.json")) {
		t.Fatal("path leaked")
	}
}
func TestRunReturnsThreeForUnreadyAndSeparatesFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (billingreconciliation.Receipt, error) {
		return billingreconciliation.Receipt{VarianceBreachCount: 1}, nil
	}})
	if code != 3 {
		t.Fatalf("unready=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("usage=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (billingreconciliation.Receipt, error) {
		return billingreconciliation.Receipt{}, errors.New("unsafe")
	}}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("failure=%d stdout=%s", code, stdout.String())
	}
}
func arguments(receipt string) []string {
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--release", "release.json", "--input", "billing.json", "--receipt", receipt}
}
