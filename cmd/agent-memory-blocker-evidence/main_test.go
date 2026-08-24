package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/blockerevidence"
)

func TestRunPublishesAggregateReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "receipt.json")
	code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{now: func() time.Time { return now }, collect: func(_, _, _, _, _ string, at time.Time) (blockerevidence.Receipt, error) {
		if !at.Equal(now) {
			t.Fatal("time not forwarded")
		}
		return blockerevidence.Receipt{Schema: blockerevidence.ReceiptSchemaV1, Input: blockerevidence.Input{Ready: true, OpenFindingCount: 3, OpenIncidentCount: 2, ReviewedOpenItemCount: 5}, OpenItemCount: 5, ReviewCoverageComplete: true, CheckCount: 5, PassedCount: 5}, nil
	}})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Ready || !result.ReceiptWritten || result.OpenItemCount != 5 || !result.ReviewCoverageComplete {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("inventory.json")) {
		t.Fatal("path leaked")
	}
}

func TestRunReturnsThreeForUnreadyAndSeparatesFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (blockerevidence.Receipt, error) {
		return blockerevidence.Receipt{BlockerCount: 1}, nil
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
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (blockerevidence.Receipt, error) {
		return blockerevidence.Receipt{}, errors.New("unsafe")
	}}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("failure code=%d stdout=%s", code, stdout.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--release", "release.json", "--input", "blockers.json", "--receipt", receipt}
}
