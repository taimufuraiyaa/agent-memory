package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/stagingjourney"
)

func TestRunPublishesAggregateOnlyReadyJourneyBundle(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	release, human, agent, secretValues := writeCLIJourneyFixtures(t, directory, now, true)
	receipt := filepath.Join(directory, "bundle.json")
	var stdout, stderr bytes.Buffer

	code := runWithDependencies([]string{
		"--release", release, "--human-journey", human, "--agent-journey", agent, "--receipt", receipt,
	}, &stdout, &stderr, dependencies{now: func() time.Time { return now }})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Schema != reportSchemaV1 || !report.Ready || !report.ReceiptWritten || report.ClientCount != 2 || report.CheckCount != 10 || report.PassedCount != 10 || report.FailedCount != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	for _, secret := range secretValues {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("aggregate output leaked journey value %q", secret)
		}
	}
	info, err := os.Stat(receipt)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestRunReturnsThreeForValidFailedJourney(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	release, human, agent, _ := writeCLIJourneyFixtures(t, directory, now, false)
	var stdout, stderr bytes.Buffer
	code := runWithDependencies([]string{
		"--release", release, "--human-journey", human, "--agent-journey", agent,
		"--receipt", filepath.Join(directory, "bundle.json"),
	}, &stdout, &stderr, dependencies{now: func() time.Time { return now }})
	if code != 3 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Ready || result.FailedCount != 1 || !result.ReceiptWritten {
		t.Fatalf("report=%+v", result)
	}
}

func TestRunRejectsMissingArgumentsAndMalformedEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithDependencies(nil, &stdout, &stderr, dependencies{}); code != 2 {
		t.Fatalf("missing argument code=%d", code)
	}
	directory := t.TempDir()
	bad := filepath.Join(directory, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"content":"forbidden"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithDependencies([]string{"--release", bad, "--human-journey", bad, "--agent-journey", bad, "--receipt", filepath.Join(directory, "out.json")}, &stdout, &stderr, dependencies{}); code != 1 || stdout.Len() != 0 {
		t.Fatalf("malformed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func writeCLIJourneyFixtures(t *testing.T, directory string, now time.Time, ready bool) (string, string, string, []string) {
	t.Helper()
	releaseID := "release-cli-20260810"
	imageDigest := strings.Repeat("b", 64)
	releaseValue := map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging",
		"kubernetes_context": "staging-context", "release_id": releaseID, "started_at": now.Add(-2 * time.Hour),
		"completed_at": now.Add(-90 * time.Minute), "outcome": "passed",
		"images":    map[string]string{"api": "registry.local/api@sha256:" + imageDigest, "worker": "registry.local/worker@sha256:" + imageDigest, "reconciler": "registry.local/reconciler@sha256:" + imageDigest, "migrate": "registry.local/migrate@sha256:" + imageDigest},
		"migration": map[string]string{"outcome": "complete"}, "rollouts": map[string]string{"outcome": "healthy"},
		"deployments": []map[string]string{{"name": "agent-memory-api", "revision": "2"}, {"name": "agent-memory-worker", "revision": "2"}, {"name": "agent-memory-reconciler", "revision": "2"}},
		"rollback":    map[string]bool{"attempted": false, "succeeded": false},
	}
	release := writeCLIJSON(t, directory, "release.json", releaseValue)
	releaseBytes, err := os.ReadFile(release)
	if err != nil {
		t.Fatal(err)
	}
	releaseDigest := fmt.Sprintf("%x", sha256.Sum256(releaseBytes))
	human := cliJourney(stagingjourney.HumanWeb, releaseID, releaseDigest, now.Add(-30*time.Minute), "0123456789abcdef0123456789abcdef")
	agent := cliJourney(stagingjourney.ScopedAgent, releaseID, releaseDigest, now.Add(-15*time.Minute), "abcdef0123456789abcdef0123456789")
	if !ready {
		agent.Ready = false
		agent.Checks[3].Outcome = stagingjourney.OutcomeFailed
	}
	return release,
		writeCLIJSON(t, directory, "human.json", human),
		writeCLIJSON(t, directory, "agent.json", agent),
		[]string{releaseID, releaseDigest, human.TraceID, agent.TraceID, human.Checks[0].RequestID, agent.Checks[0].RequestID}
}

func cliJourney(kind stagingjourney.ClientKind, releaseID, releaseDigest string, completed time.Time, traceID string) stagingjourney.Journey {
	ids := []stagingjourney.CheckID{
		stagingjourney.CheckAuthenticated, stagingjourney.CheckMemoryWriteAudited,
		stagingjourney.CheckMemorySearchAudit, stagingjourney.CheckExportReadyAudited, stagingjourney.CheckClientCleanup,
	}
	checks := make([]stagingjourney.Check, 0, len(ids))
	for _, id := range ids {
		checks = append(checks, stagingjourney.Check{ID: id, Outcome: stagingjourney.OutcomePassed, RequestID: uuid.NewString()})
	}
	return stagingjourney.Journey{
		Schema: stagingjourney.JourneySchemaV1, Classification: "staging_external", Environment: "staging",
		ReleaseID: releaseID, ReleaseReceiptSHA256: releaseDigest, ClientKind: kind, Ready: true,
		TraceID: traceID, StartedAt: completed.Add(-5 * time.Minute), CompletedAt: completed, Checks: checks,
	}
}

func writeCLIJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
