package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchstate"
)

func TestRunPublishesAggregateOnlyReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(receiptPath), &stdout, &stderr, dependencies{
		now: func() time.Time { return now }, postgresURL: func() string { return "postgres://secret@example/private" },
		collect: func(_ context.Context, _, _, _, _, connectionURL string, at time.Time) (launchstate.Receipt, error) {
			if connectionURL == "" || !at.Equal(now) {
				t.Fatal("collector dependencies not forwarded")
			}
			return launchstate.Receipt{Schema: launchstate.ReceiptSchemaV1, Ready: true, Phase: "internal_alpha"}, nil
		},
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Schema != reportSchemaV1 || !result.Ready || !result.ReceiptWritten || result.Phase != "internal_alpha" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("secret")) || bytes.Contains(stdout.Bytes(), []byte("inventory.json")) {
		t.Fatal("aggregate output leaked configuration or paths")
	}
}

func TestRunReturnsThreeForValidUnreadyAndSeparatesFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{
		postgresURL: func() string { return "postgres://configured" },
		collect: func(context.Context, string, string, string, string, string, time.Time) (launchstate.Receipt, error) {
			return launchstate.Receipt{Schema: launchstate.ReceiptSchemaV1, Phase: "private_beta"}, nil
		},
	})
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
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{postgresURL: func() string { return "" }}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("configuration code=%d stdout=%s", code, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies(arguments(filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, dependencies{
		postgresURL: func() string { return "postgres://configured" },
		collect: func(context.Context, string, string, string, string, string, time.Time) (launchstate.Receipt, error) {
			return launchstate.Receipt{}, errors.New("unsafe evidence")
		},
	}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("failure code=%d stdout=%s", code, stdout.String())
	}
}

func arguments(receipt string) []string {
	return []string{"--inventory", "inventory.json", "--plan", "plan.json", "--change", "change.json", "--release", "release.json", "--receipt", receipt}
}
