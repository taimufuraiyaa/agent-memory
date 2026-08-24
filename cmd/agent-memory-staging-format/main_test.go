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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/stagingformat"
)

func TestRunPublishesAggregateOnlyReadyFormatReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	releasePath, inputPath, secrets := writeCLIFixtures(t, now, true)
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	var stdout, stderr bytes.Buffer
	code := runWithDependencies([]string{"--release", releasePath, "--input", inputPath, "--receipt", receiptPath}, &stdout, &stderr, dependencies{now: func() time.Time { return now }})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Schema != reportSchemaV1 || !result.Ready || !result.ReceiptWritten || result.FormatCount != 4 || result.CheckCount != 28 || result.PassedCount != 28 || result.FailedCount != 0 {
		t.Fatalf("report=%+v err=%v", result, err)
	}
	for _, secret := range secrets {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("aggregate output leaked %q", secret)
		}
	}
	info, err := os.Stat(receiptPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestRunReturnsThreeForValidUnreadyFormatReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	releasePath, inputPath, _ := writeCLIFixtures(t, now, false)
	var stdout, stderr bytes.Buffer
	code := runWithDependencies([]string{"--release", releasePath, "--input", inputPath, "--receipt", filepath.Join(t.TempDir(), "receipt.json")}, &stdout, &stderr, dependencies{now: func() time.Time { return now }})
	if code != 3 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result report
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Ready || result.FailedCount != 1 || !result.ReceiptWritten {
		t.Fatalf("report=%+v", result)
	}
}

func TestRunSeparatesUsageAndMalformedEvidence(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"filename":"private.pdf"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--release", bad, "--input", bad, "--receipt", filepath.Join(t.TempDir(), "receipt.json")}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("malformed code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func writeCLIFixtures(t *testing.T, now time.Time, ready bool) (string, string, []string) {
	t.Helper()
	directory := t.TempDir()
	releaseID := "release-format-cli-20260810"
	imageDigest := strings.Repeat("a", 64)
	release := map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging",
		"kubernetes_context": "staging-context", "release_id": releaseID, "started_at": now.Add(-5 * time.Hour), "completed_at": now.Add(-4 * time.Hour), "outcome": "passed",
		"images":    map[string]string{"api": "registry.local/api@sha256:" + imageDigest, "worker": "registry.local/worker@sha256:" + imageDigest, "reconciler": "registry.local/reconciler@sha256:" + imageDigest, "migrate": "registry.local/migrate@sha256:" + imageDigest},
		"migration": map[string]string{"outcome": "complete"}, "rollouts": map[string]string{"outcome": "healthy"},
		"deployments": []map[string]string{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}},
		"rollback":    map[string]bool{"attempted": false, "succeeded": false},
	}
	releasePath := writeCLIJSON(t, directory, "release.json", release)
	releaseBytes, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	releaseDigest := fmt.Sprintf("%x", sha256.Sum256(releaseBytes))
	formats := []struct {
		format stagingformat.Format
		media  string
	}{{stagingformat.FormatPDF, "application/pdf"}, {stagingformat.FormatEPUB, "application/epub+zip"}, {stagingformat.FormatMarkdown, "text/markdown"}, {stagingformat.FormatText, "text/plain"}}
	runs := make([]stagingformat.Run, 0, 4)
	secrets := []string{releaseID, releaseDigest}
	for index, value := range formats {
		name := string(value.format)
		sourceReceipt, jobReceipt := cliDigest(name+"-source"), cliDigest(name+"-job")
		fulltextReceipt, vectorReceipt := cliDigest(name+"-fulltext"), cliDigest(name+"-vector")
		checks := []stagingformat.Check{
			{ID: stagingformat.CheckUploadAccepted, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: cliDigest(name + "-upload")},
			{ID: stagingformat.CheckSourceVersionPublished, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: sourceReceipt},
			{ID: stagingformat.CheckIngestionJobSucceeded, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: jobReceipt},
			{ID: stagingformat.CheckFullTextProjectionReady, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: fulltextReceipt},
			{ID: stagingformat.CheckVectorProjectionReady, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: vectorReceipt},
			{ID: stagingformat.CheckSourceReady, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: cliDigest(name + "-ready")},
			{ID: stagingformat.CheckSourceDeleted, Outcome: stagingformat.OutcomePassed, EvidenceSHA256: cliDigest(name + "-deleted")},
		}
		sourceID, jobID := uuid.NewString(), uuid.NewString()
		traceID := fmt.Sprintf("%032x", index+1)
		completed := now.Add(time.Duration(-90+index*10) * time.Minute)
		runs = append(runs, stagingformat.Run{
			Format: value.format, MediaType: value.media, SourceID: sourceID, SourceVersion: 1, SourceVersionReceiptSHA256: sourceReceipt,
			IngestionJobID: jobID, IngestionJobReceiptSHA256: jobReceipt, TraceID: traceID, StartedAt: completed.Add(-20 * time.Minute), CompletedAt: completed, Ready: true,
			FullTextProjection: stagingformat.Projection{Version: "fulltext-v1", DocumentCount: 2, ReceiptSHA256: fulltextReceipt},
			VectorProjection:   stagingformat.Projection{Version: "vector-v1", DocumentCount: 2, ReceiptSHA256: vectorReceipt}, Checks: checks,
		})
		secrets = append(secrets, sourceID, jobID, traceID)
	}
	input := stagingformat.Input{Schema: stagingformat.InputSchemaV1, Classification: "staging_external", Environment: "staging", ReleaseID: releaseID, ReleaseReceiptSHA256: releaseDigest, Ready: true, GeneratedAt: now.Add(-30 * time.Minute), Runs: runs}
	if !ready {
		input.Ready = false
		input.Runs[0].Ready = false
		input.Runs[0].Checks[6].Outcome = stagingformat.OutcomeFailed
	}
	return releasePath, writeCLIJSON(t, directory, "input.json", input), secrets
}

func cliDigest(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

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
