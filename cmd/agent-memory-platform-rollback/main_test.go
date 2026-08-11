package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestRunCollectsReadyContentFreeRollbackReceipt(t *testing.T) {
	baseline, attempt := writeReleasePair(t)
	receiptPath := filepath.Join(t.TempDir(), "rollback.json")
	runner := &fakeKubectl{}
	var stdout, stderr bytes.Buffer
	code := runWithRunner(arguments(baseline, attempt, receiptPath), &stdout, &stderr, runner)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	if !receipt.Ready || len(receipt.Deployments) != 3 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.ReceiptWritten || result.RestoredCount != 3 || result.DeploymentCount != 3 {
		t.Fatalf("unexpected report: %+v", result)
	}
	contents, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"registry.example", "sha256:aaaaaaaa", "raw-secret", "token", "customer"} {
		if strings.Contains(string(contents), forbidden) || strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("rollback evidence leaked %q", forbidden)
		}
	}
	assertBoundedQueries(t, runner.calls)
}

func TestRunWritesUnreadyReceiptForLiveMismatch(t *testing.T) {
	baseline, attempt := writeReleasePair(t)
	receiptPath := filepath.Join(t.TempDir(), "rollback.json")
	runner := &fakeKubectl{mismatch: "agent-memory-api"}
	var stdout, stderr bytes.Buffer
	code := runWithRunner(arguments(baseline, attempt, receiptPath), &stdout, &stderr, runner)
	if code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	if receipt.Ready || receipt.Deployments[0].Outcome != platformrollback.OutcomeImageMismatch {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestRunClassifiesOmittedZeroReadyReplicasAsNotReady(t *testing.T) {
	baseline, attempt := writeReleasePair(t)
	receiptPath := filepath.Join(t.TempDir(), "rollback.json")
	runner := &fakeKubectl{zeroReady: "agent-memory-worker"}
	var stdout, stderr bytes.Buffer
	code := runWithRunner(arguments(baseline, attempt, receiptPath), &stdout, &stderr, runner)
	if code != 3 {
		t.Fatalf("code=%d, want 3; stderr=%s", code, stderr.String())
	}
	receipt := loadReceipt(t, receiptPath)
	if receipt.Deployments[1].Outcome != platformrollback.OutcomeNotReady {
		t.Fatalf("omitted zero readyReplicas outcome=%s, want not_ready", receipt.Deployments[1].Outcome)
	}
}

func TestRunRejectsArgumentsInvalidPairAndContextFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithRunner(nil, &stdout, &stderr, &fakeKubectl{}); code != 2 {
		t.Fatalf("missing args code=%d, want 2", code)
	}

	baseline, attempt := writeReleasePair(t)
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithRunner(arguments(invalid, attempt, filepath.Join(t.TempDir(), "receipt.json")), &stdout, &stderr, &fakeKubectl{}); code != 1 {
		t.Fatalf("invalid pair code=%d, want 1", code)
	}

	stdout.Reset()
	stderr.Reset()
	receiptPath := filepath.Join(t.TempDir(), "rollback.json")
	if code := runWithRunner(arguments(baseline, attempt, receiptPath), &stdout, &stderr, &fakeKubectl{failContext: true}); code != 1 {
		t.Fatalf("context failure code=%d, want 1", code)
	}
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("collector failure unexpectedly published a receipt")
	}
}

type fakeKubectl struct {
	mismatch    string
	zeroReady   string
	failContext bool
	calls       [][]string
}

func (fake *fakeKubectl) Run(_ context.Context, args ...string) (string, error) {
	fake.calls = append(fake.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	if joined == "config current-context" {
		if fake.failContext {
			return "", errors.New("untrusted collector error")
		}
		return "staging-context", nil
	}
	name := deploymentName(args)
	if name == "" {
		return "", errors.New("unexpected query")
	}
	switch {
	case strings.Contains(joined, "containers[?(@.name==") && strings.Contains(joined, ")].image"):
		container := strings.TrimPrefix(name, "agent-memory-")
		if !strings.Contains(joined, `name=="`+container+`"`) {
			return "", errors.New("wrong container selector")
		}
		digest := strings.Repeat("a", 64)
		if fake.mismatch == name {
			digest = strings.Repeat("b", 64)
		}
		return "registry.example/" + name + "@sha256:" + digest, nil
	case strings.Contains(joined, "revision}"):
		return "8", nil
	case strings.Contains(joined, "spec.replicas"):
		if name == "agent-memory-reconciler" {
			return "1", nil
		}
		return "2", nil
	case strings.Contains(joined, "status.readyReplicas"):
		if fake.zeroReady == name {
			return "", nil
		}
		if name == "agent-memory-reconciler" {
			return "1", nil
		}
		return "2", nil
	default:
		return "", errors.New("unexpected query")
	}
}

func arguments(baseline, attempt, receipt string) []string {
	return []string{"--baseline", baseline, "--failed-attempt", attempt, "--receipt", receipt}
}

func writeReleasePair(t *testing.T) (string, string) {
	t.Helper()
	baseline := releaseJSON("baseline-release", "2026-08-09T17:00:00Z", "2026-08-09T17:10:00Z", "passed", false, false, "a")
	attempt := releaseJSON("failed-release", "2026-08-09T17:15:00Z", "2026-08-09T17:30:00Z", "failed", true, true, "b")
	return writeFile(t, "baseline.json", baseline), writeFile(t, "attempt.json", attempt)
}

func releaseJSON(id, started, completed, outcome string, attempted, succeeded bool, digestCharacter string) string {
	digest := strings.Repeat(digestCharacter, 64)
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest }
	rollout := "healthy"
	if outcome == "failed" {
		rollout = "failed"
	}
	value := map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging",
		"namespace": "agent-memory-staging", "kubernetes_context": "staging-context",
		"release_id": id, "started_at": started, "completed_at": completed, "outcome": outcome,
		"images":    map[string]string{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")},
		"migration": map[string]string{"outcome": "complete"}, "rollouts": map[string]string{"outcome": rollout},
		"deployments": []map[string]string{{"name": "agent-memory-api", "revision": "7"}, {"name": "agent-memory-worker", "revision": "7"}, {"name": "agent-memory-reconciler", "revision": "7"}},
		"rollback":    map[string]bool{"attempted": attempted, "succeeded": succeeded},
	}
	contents, _ := json.Marshal(value)
	return string(contents)
}

func writeFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadReceipt(t *testing.T, path string) platformrollback.Receipt {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt platformrollback.Receipt
	if err := json.Unmarshal(contents, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func deploymentName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "deployment/") {
			return strings.TrimPrefix(arg, "deployment/")
		}
	}
	return ""
}

func assertBoundedQueries(t *testing.T, calls [][]string) {
	t.Helper()
	for _, args := range calls {
		joined := strings.Join(args, " ")
		for _, forbidden := range []string{"secret", "configmap", "pod", "logs", "events", "-o json ", "-o yaml", "env"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("collector used forbidden query %q", joined)
			}
		}
		if strings.Contains(joined, "get deployment/") && !strings.Contains(joined, "jsonpath=") {
			t.Fatalf("deployment query was not bounded: %q", joined)
		}
	}
}
