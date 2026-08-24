package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/backupexpiry"
)

func TestRunPublishesAggregateReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "receipt.json")
	code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{
		now: func() time.Time { return now },
		collect: func(_, _, _, _, _ string, at time.Time) (backupexpiry.Receipt, error) {
			if !at.Equal(now) {
				t.Fatal("collection time not forwarded")
			}
			return backupexpiry.Receipt{Schema: backupexpiry.ReceiptSchemaV1, Ready: true, CheckCount: 7, PassedCount: 7}, nil
		},
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Ready || !result.ReceiptWritten || result.CheckCount != 7 || result.PassedCount != 7 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRunReturnsThreeForValidUnreadyAndSeparatesFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "receipt.json")
	code := runWithDependencies(arguments(path), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (backupexpiry.Receipt, error) {
		return backupexpiry.Receipt{Schema: backupexpiry.ReceiptSchemaV1, Ready: false, CheckCount: 7, PassedCount: 6, FailedCount: 1}, nil
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
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{collect: func(_, _, _, _, _ string, _ time.Time) (backupexpiry.Receipt, error) {
		return backupexpiry.Receipt{}, errors.New("malformed evidence")
	}}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("failure code=%d stdout=%s", code, stdout.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--retention-inventory", "retention.json", "--drill", "drill.json", "--receipt", receipt}
}
