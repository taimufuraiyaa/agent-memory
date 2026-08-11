package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrievalload"
)

func TestRunPublishesAggregateReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "receipt.json")
	code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return now }, collect: func(_, _, _, _, _ string, at time.Time) (retrievalload.Receipt, error) {
		if !at.Equal(now) {
			t.Fatal("collection time not forwarded")
		}
		return retrievalload.Receipt{Schema: retrievalload.ReceiptSchemaV1, Ready: true, CheckCount: 8, PassedCount: 8, CorpusSourceCount: 100, CorpusPassageCount: 20000, RequestCount: 5000, Concurrency: 32, ModelCallCount: 5000, P50LatencyMicroseconds: 110000, P95LatencyMicroseconds: 620000, P99LatencyMicroseconds: 760000, SearchP95TargetMicroseconds: 800000, MaximumModelCostMicroUSDPer1000Requests: 250000, ObservedModelCostMicroUSDPer1000Requests: 180000}, nil
	}})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Ready || !result.ReceiptWritten || result.CheckCount != 8 || result.RequestCount != 5000 || result.P95LatencyMicroseconds != 620000 || result.ObservedModelCostMicroUSDPer1000Requests != 180000 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("inventory.json")) {
		t.Fatal("aggregate output leaked a path")
	}
}

func TestRunReturnsThreeForUnreadyAndSeparatesUsageAndEvidenceFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (retrievalload.Receipt, error) {
		return retrievalload.Receipt{Schema: retrievalload.ReceiptSchemaV1, CheckCount: 8, PassedCount: 7, FailedCount: 1, MetricBreachCount: 1}, nil
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
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (retrievalload.Receipt, error) {
		return retrievalload.Receipt{}, errors.New("unsafe evidence")
	}}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("failure code=%d stdout=%s", code, stdout.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--release", "release.json", "--input", "load.json", "--receipt", receipt}
}
