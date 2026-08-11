package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformpreflight"
)

func TestRunCollectsReadyContentFreeReceipt(t *testing.T) {
	runner := &fakeKubectl{}
	receiptPath := filepath.Join(t.TempDir(), "preflight.json")
	var stdout, stderr bytes.Buffer
	code := runWithRunner([]string{
		"--inventory", canonicalInventory(t),
		"--environment", "staging",
		"--receipt", receiptPath,
	}, &stdout, &stderr, runner)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	inventory, err := platforminventory.Load(canonicalInventory(t))
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Namespace != "agent-memory-staging" || receipt.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || len(receipt.Checks) != 8 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if strings.TrimSpace(stdout.String()) != `{"ready":true,"receipt_written":true}` {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	assertBoundedQueries(t, runner.calls)
	for _, forbidden := range []string{"registry.local", "raw-secret", "platform_operations", "replace-with-site-id"} {
		contents, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("receipt leaked %q", forbidden)
		}
	}
}

func TestRunWritesFailedReceiptAndUsesDistinctUnreadyExit(t *testing.T) {
	runner := &fakeKubectl{missing: "secret/agent-memory-worker-secrets"}
	receiptPath := filepath.Join(t.TempDir(), "preflight.json")
	var stdout, stderr bytes.Buffer
	code := runWithRunner([]string{
		"--inventory", canonicalInventory(t),
		"--environment", "staging",
		"--receipt", receiptPath,
	}, &stdout, &stderr, runner)
	if code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	if receipt.Ready || checkOutcome(receipt, "secret_contracts") != platformpreflight.OutcomeFailed {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestRunRejectsArgumentsAndCollectorFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithRunner(nil, &stdout, &stderr, &fakeKubectl{}); code != 2 {
		t.Fatalf("missing args code=%d, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	receiptPath := filepath.Join(t.TempDir(), "preflight.json")
	if code := runWithRunner([]string{
		"--inventory", canonicalInventory(t),
		"--environment", "staging",
		"--receipt", receiptPath,
	}, &stdout, &stderr, &fakeKubectl{failContext: true}); code != 1 {
		t.Fatalf("collector failure code=%d, want 1", code)
	}
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("collector failure unexpectedly published a receipt")
	}
}

type fakeKubectl struct {
	missing     string
	failContext bool
	calls       [][]string
}

func (fake *fakeKubectl) Run(_ context.Context, args ...string) (string, error) {
	fake.calls = append(fake.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	if joined == "config current-context" {
		if fake.failContext {
			return "", errors.New("untrusted raw collector failure")
		}
		return "kind-agent-memory", nil
	}
	if fake.missing != "" && strings.Contains(joined, fake.missing) {
		return "", errors.New("not found")
	}
	if strings.Contains(joined, "get namespace/agent-memory-staging") {
		return "namespace/agent-memory-staging", nil
	}
	if strings.Contains(joined, "get serviceaccount/") || strings.Contains(joined, "get secret/") || strings.Contains(joined, "get networkpolicy/") {
		return "present", nil
	}
	if strings.Contains(joined, "get service/agent-memory-api") {
		return "ClusterIP", nil
	}
	if strings.Contains(joined, "get deployment/") {
		name := deploymentName(args)
		switch {
		case strings.Contains(joined, "serviceAccountName"):
			return name, nil
		case strings.Contains(joined, "containers[0].image"):
			return "registry.local/" + name + "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
		case strings.Contains(joined, "spec.replicas"):
			if name == "agent-memory-reconciler" {
				return "1", nil
			}
			return "2", nil
		case strings.Contains(joined, "status.readyReplicas"):
			if name == "agent-memory-reconciler" {
				return "1", nil
			}
			return "2", nil
		}
	}
	return "", errors.New("unexpected bounded query")
}

func deploymentName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "deployment/") {
			return strings.TrimPrefix(arg, "deployment/")
		}
	}
	return ""
}

func canonicalInventory(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Join(filepath.Dir(current), "..", "..", "docs", "saas", "self-managed-platform-inventory.example.json")
}

func loadReceipt(t *testing.T, path string) platformpreflight.Receipt {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt platformpreflight.Receipt
	if err := json.Unmarshal(contents, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func checkOutcome(receipt platformpreflight.Receipt, id string) platformpreflight.Outcome {
	for _, check := range receipt.Checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}

func assertBoundedQueries(t *testing.T, calls [][]string) {
	t.Helper()
	for _, args := range calls {
		joined := strings.Join(args, " ")
		for _, forbidden := range []string{"logs", "events", "pod/", "configmap", "-o json ", "-o yaml", "-o go-template"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("collector used forbidden query %q", joined)
			}
		}
		if strings.Contains(joined, "get secret/") && !strings.HasSuffix(joined, "-o name") {
			t.Fatalf("secret query was not name-only: %q", joined)
		}
	}
}
