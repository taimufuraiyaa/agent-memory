package stagingformat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCanonicalExampleShapeRemainsValid(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "docs", "saas", "staging-format-ingestion.example.json")
	var input Input
	if _, err := decodeStrictRegular(path, &input); err != nil {
		t.Fatal(err)
	}
	runs, err := validateInput(input, "replace-with-release-id", strings.Repeat("a", 64), time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC), time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC))
	if err != nil || len(runs) != 4 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
}

func TestCollectBindsFourReadyFormatsToPassedRelease(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-4*time.Hour))
	input := readyInput(releaseID, releaseDigest, now)
	for left, right := 0, len(input.Runs)-1; left < right; left, right = left+1, right-1 {
		input.Runs[left], input.Runs[right] = input.Runs[right], input.Runs[left]
	}
	receipt, err := Collect(releasePath, writeJSON(t, directory, "formats.json", input), now)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if !receipt.Ready || receipt.Schema != ReceiptSchemaV1 || receipt.ReleaseID != releaseID || receipt.ReleaseReceiptSHA256 != releaseDigest ||
		assessment.FormatCount != 4 || assessment.CheckCount != 28 || assessment.PassedCount != 28 || assessment.FailedCount != 0 {
		t.Fatalf("receipt=%+v assessment=%+v", receipt, assessment)
	}
	for index, format := range requiredFormats {
		if receipt.Runs[index].Format != format || !digestPattern.MatchString(receipt.Runs[index].SourceVersionReceiptSHA256) || !uuidV4(receipt.Runs[index].SourceID) {
			t.Fatalf("run[%d]=%+v", index, receipt.Runs[index])
		}
	}
}

func TestCollectKeepsFailedCleanupValidButUnready(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-4*time.Hour))
	input := readyInput(releaseID, releaseDigest, now)
	input.Ready = false
	input.Runs[1].Ready = false
	input.Runs[1].Checks[6].Outcome = OutcomeFailed
	receipt, err := Collect(releasePath, writeJSON(t, directory, "formats.json", input), now)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if assessment.Ready || assessment.PassedCount != 27 || assessment.FailedCount != 1 {
		t.Fatalf("assessment=%+v", assessment)
	}
}

func TestCollectRejectsUnsafeContradictoryOrContentBearingEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	tests := map[string]func(*Input){
		"local classification": func(input *Input) { input.Classification = "local_development" },
		"release mismatch":     func(input *Input) { input.ReleaseReceiptSHA256 = strings.Repeat("f", 64) },
		"duplicate format": func(input *Input) {
			input.Runs[3].Format = input.Runs[0].Format
			input.Runs[3].MediaType = input.Runs[0].MediaType
		},
		"missing format":                 func(input *Input) { input.Runs = input.Runs[:3] },
		"media mismatch":                 func(input *Input) { input.Runs[0].MediaType = "text/plain" },
		"duplicate source id":            func(input *Input) { input.Runs[1].SourceID = input.Runs[0].SourceID },
		"duplicate job id":               func(input *Input) { input.Runs[1].IngestionJobID = input.Runs[0].IngestionJobID },
		"source id reused as job id":     func(input *Input) { input.Runs[1].IngestionJobID = input.Runs[0].SourceID },
		"duplicate trace id":             func(input *Input) { input.Runs[1].TraceID = input.Runs[0].TraceID },
		"non v4 source id":               func(input *Input) { input.Runs[0].SourceID = "00000000-0000-1000-8000-000000000000" },
		"missing source version":         func(input *Input) { input.Runs[0].SourceVersion = 0 },
		"duplicate check":                func(input *Input) { input.Runs[0].Checks[1].ID = input.Runs[0].Checks[0].ID },
		"unknown check":                  func(input *Input) { input.Runs[0].Checks[0].ID = "content_indexed" },
		"missing evidence hash":          func(input *Input) { input.Runs[0].Checks[0].EvidenceSHA256 = "" },
		"source receipt hash mismatch":   func(input *Input) { input.Runs[0].Checks[1].EvidenceSHA256 = strings.Repeat("a", 64) },
		"projection hash mismatch":       func(input *Input) { input.Runs[0].Checks[3].EvidenceSHA256 = strings.Repeat("b", 64) },
		"contradictory run readiness":    func(input *Input) { input.Runs[0].Ready = false },
		"contradictory bundle readiness": func(input *Input) { input.Ready = false },
		"zero ready projection":          func(input *Input) { input.Runs[0].FullTextProjection.DocumentCount = 0 },
		"downstream success after failed job": func(input *Input) {
			input.Ready = false
			input.Runs[0].Ready = false
			input.Runs[0].Checks[2].Outcome = OutcomeFailed
		},
		"pre release run": func(input *Input) { input.Runs[0].StartedAt = now.Add(-5 * time.Hour) },
		"oversize run": func(input *Input) {
			input.Runs[0].StartedAt = input.Runs[0].CompletedAt.Add(-6*time.Hour - time.Second)
		},
		"future generation":    func(input *Input) { input.GeneratedAt = now.Add(time.Second) },
		"stale generation":     func(input *Input) { input.GeneratedAt = now.Add(-25 * time.Hour) },
		"generated before run": func(input *Input) { input.GeneratedAt = input.Runs[3].CompletedAt.Add(-time.Second) },
		"bundle span too long": func(input *Input) {
			input.Runs[0].StartedAt = input.Runs[3].CompletedAt.Add(-24*time.Hour - time.Second)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-4*time.Hour))
			input := readyInput(releaseID, releaseDigest, now)
			mutate(&input)
			if _, err := Collect(releasePath, writeJSON(t, directory, "formats.json", input), now); err == nil {
				t.Fatal("unsafe or contradictory format evidence was accepted")
			}
		})
	}

	directory := t.TempDir()
	releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-4*time.Hour))
	path := writeJSON(t, directory, "formats.json", readyInput(releaseID, releaseDigest, now))
	link := filepath.Join(directory, "formats-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(releasePath, link, now); err == nil {
		t.Fatal("symlink input was accepted")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(contents), `"schema":`, `"filename":"private-book.pdf","schema":`, 1)
	unknownPath := filepath.Join(directory, "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(releasePath, unknownPath, now); err == nil {
		t.Fatal("content-bearing unknown field was accepted")
	}
}

func TestPublishIsPrivateCreateOnlyAndRejectsSymlinkDestination(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(filepath.Join(directory, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, Receipt{}); err == nil {
		t.Fatal("symlink destination was accepted")
	}
}

func readyInput(releaseID, releaseDigest string, now time.Time) Input {
	runs := make([]Run, 0, len(requiredFormats))
	for index, format := range requiredFormats {
		formatName := string(format)
		completed := now.Add(time.Duration(-90+index*10) * time.Minute)
		sourceReceipt := digestFor(formatName + "-source")
		jobReceipt := digestFor(formatName + "-job")
		fulltextReceipt := digestFor(formatName + "-fulltext")
		vectorReceipt := digestFor(formatName + "-vector")
		checks := []Check{
			{ID: CheckUploadAccepted, Outcome: OutcomePassed, EvidenceSHA256: digestFor(formatName + "-upload")},
			{ID: CheckSourceVersionPublished, Outcome: OutcomePassed, EvidenceSHA256: sourceReceipt},
			{ID: CheckIngestionJobSucceeded, Outcome: OutcomePassed, EvidenceSHA256: jobReceipt},
			{ID: CheckFullTextProjectionReady, Outcome: OutcomePassed, EvidenceSHA256: fulltextReceipt},
			{ID: CheckVectorProjectionReady, Outcome: OutcomePassed, EvidenceSHA256: vectorReceipt},
			{ID: CheckSourceReady, Outcome: OutcomePassed, EvidenceSHA256: digestFor(formatName + "-ready")},
			{ID: CheckSourceDeleted, Outcome: OutcomePassed, EvidenceSHA256: digestFor(formatName + "-deleted")},
		}
		runs = append(runs, Run{
			Format: format, MediaType: mediaTypeFor(format), SourceID: uuid.NewString(), SourceVersion: 1,
			SourceVersionReceiptSHA256: sourceReceipt, IngestionJobID: uuid.NewString(), IngestionJobReceiptSHA256: jobReceipt,
			TraceID: fmt.Sprintf("%032x", index+1), StartedAt: completed.Add(-20 * time.Minute), CompletedAt: completed, Ready: true,
			FullTextProjection: Projection{Version: "fulltext-v1", DocumentCount: 3, ReceiptSHA256: fulltextReceipt},
			VectorProjection:   Projection{Version: "vector-v1", DocumentCount: 3, ReceiptSHA256: vectorReceipt}, Checks: checks,
		})
	}
	return Input{
		Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging",
		ReleaseID: releaseID, ReleaseReceiptSHA256: releaseDigest, Ready: true,
		GeneratedAt: now.Add(-30 * time.Minute), Runs: runs,
	}
}

func digestFor(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

func writeReleaseFixture(t *testing.T, directory string, completed time.Time) (string, string, string) {
	t.Helper()
	releaseID := "release-format-20260810"
	digest := strings.Repeat("a", 64)
	receipt := map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging",
		"namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": releaseID,
		"started_at": completed.Add(-10 * time.Minute), "completed_at": completed, "outcome": "passed",
		"images":    map[string]string{"api": "registry.local/api@sha256:" + digest, "worker": "registry.local/worker@sha256:" + digest, "reconciler": "registry.local/reconciler@sha256:" + digest, "migrate": "registry.local/migrate@sha256:" + digest},
		"migration": map[string]string{"outcome": "complete"}, "rollouts": map[string]string{"outcome": "healthy"},
		"deployments": []map[string]string{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}},
		"rollback":    map[string]bool{"attempted": false, "succeeded": false},
	}
	path := writeJSON(t, directory, "release.json", receipt)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, releaseID, fmt.Sprintf("%x", sha256.Sum256(contents))
}

func writeJSON(t *testing.T, directory, name string, value any) string {
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
