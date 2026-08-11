package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/retentioninventory"
)

func TestRunPublishesAggregateOnlyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	var stdout, stderr bytes.Buffer
	code := runWithDependencies([]string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--receipt", receiptPath}, &stdout, &stderr, dependencies{
		now:         func() time.Time { return now },
		postgresURL: func() string { return "postgres://secret@example/private" },
		collect: func(_ context.Context, _, _, _, connectionURL string, collectedAt time.Time) (retentioninventory.Receipt, error) {
			if connectionURL == "" || !collectedAt.Equal(now) {
				t.Fatal("collector dependencies were not forwarded")
			}
			return retentioninventory.Receipt{Schema: retentioninventory.ReceiptSchemaV1, Ready: true, PolicyCount: 12}, nil
		},
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Schema != reportSchemaV1 || !result.Ready || !result.ReceiptWritten || result.PolicyCount != 12 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("secret")) || bytes.Contains(stdout.Bytes(), []byte("inventory.json")) {
		t.Fatal("aggregate output leaked sensitive configuration or paths")
	}
}

func TestRunSeparatesUsageConfigurationAndOperationalFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
	args := []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--receipt", filepath.Join(t.TempDir(), "receipt.json")}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies(args, &stdout, &stderr, dependencies{postgresURL: func() string { return "" }}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("missing configuration code=%d stdout=%s", code, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies(args, &stdout, &stderr, dependencies{
		postgresURL: func() string { return "postgres://configured" },
		collect: func(context.Context, string, string, string, string, time.Time) (retentioninventory.Receipt, error) {
			return retentioninventory.Receipt{}, errors.New("database unavailable")
		},
	}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("operational code=%d stdout=%s", code, stdout.String())
	}
}
