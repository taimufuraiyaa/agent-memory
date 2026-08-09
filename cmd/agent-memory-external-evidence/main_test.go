package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidenceindex"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

func TestRunReturnsReadyIncompleteAndInvalidStatusesWithoutLeakingEvidence(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	artifacts := filepath.Join(root, "bundle")
	if err := os.MkdirAll(filepath.Join(artifacts, "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	dossierContents := "private reviewed dossier contents"
	dossierPath := filepath.Join(artifacts, "artifacts", "release.json")
	if err := os.WriteFile(dossierPath, []byte(dossierContents), 0o600); err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256([]byte(dossierContents))
	digest := hex.EncodeToString(digestBytes[:])
	catalog := evidenceindex.Catalog{Schema: evidenceindex.CatalogSchemaV1, Controls: []evidenceindex.Control{{
		ID: "P1.2-A", ApprovalControl: "p1_2_a", OwnerGroup: "operations", EvidenceRequirement: "staging release receipt",
	}}}
	index := evidenceindex.Index{Schema: evidenceindex.IndexSchemaV1, Gate: evidenceindex.ExternalEvidenceGate, GeneratedAt: now, Entries: []evidenceindex.Entry{{
		ControlID: "P1.2-A", ApprovalControl: "p1_2_a", DossierPath: "artifacts/release.json",
		EvidenceRef: "report://operations/staging", EvidenceSHA256: digest,
		Classification: evidenceindex.ExternalStaging, Environment: evidenceindex.Staging, CollectedAt: now.Add(-time.Hour),
	}}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	approval := readiness.SignedApproval{
		Schema: readiness.ApprovalArtifactSchema, Gate: evidenceindex.ExternalEvidenceGate, Control: "p1_2_a",
		Decision: "approved", Owner: "operations-review", KeyID: "operations-2026",
		EvidenceRef: index.Entries[0].EvidenceRef, EvidenceSHA256: digest,
		IssuedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
	}
	payload, err := readiness.CanonicalApprovalPayload(approval)
	if err != nil {
		t.Fatal(err)
	}
	approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
	trust := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{
		KeyID: "operations-2026", Owner: "operations-review", PublicKey: base64.StdEncoding.EncodeToString(public),
		Gates: []string{evidenceindex.ExternalEvidenceGate}, Controls: []string{"p1_2_a"},
	}}}
	catalogPath := writeJSON(t, root, "catalog.json", catalog)
	indexPath := writeJSON(t, root, "index.json", index)
	trustPath := writeJSON(t, root, "trust.json", trust)
	approvalsPath := filepath.Join(root, "approvals")
	if err := os.Mkdir(approvalsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, approvalsPath, "approval.json", approval)
	args := []string{"--catalog", catalogPath, "--index", indexPath, "--artifacts-root", artifacts, "--trust", trustPath, "--approvals-dir", approvalsPath, "--at", now.Format(time.RFC3339)}
	var stdout, stderr bytes.Buffer
	if exit := run(args, time.Now, &stdout, &stderr); exit != 0 {
		t.Fatalf("ready exit=%d stderr=%s", exit, stderr.String())
	}
	if strings.Contains(stdout.String(), dossierContents) || strings.Contains(stdout.String(), dossierPath) || strings.Contains(stdout.String(), approval.Signature) || strings.Contains(stdout.String(), trust.Keys[0].PublicKey) || strings.Contains(stdout.String(), approval.Owner) {
		t.Fatal("content-free report leaked evidence, path, signature, key, or owner")
	}
	var report evidenceindex.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || !report.Ready || report.Verified != 1 {
		t.Fatalf("ready report=%+v err=%v", report, err)
	}

	index.Entries = []evidenceindex.Entry{}
	incompletePath := writeJSON(t, root, "incomplete.json", index)
	args[3] = incompletePath
	stdout.Reset()
	stderr.Reset()
	if exit := run(args, time.Now, &stdout, &stderr); exit != 3 {
		t.Fatalf("incomplete exit=%d stderr=%s", exit, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Ready || strings.Join(report.Missing, ",") != "P1.2-A" {
		t.Fatalf("incomplete report=%+v err=%v", report, err)
	}

	invalidPath := filepath.Join(root, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{"schema":"wrong"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args[3] = invalidPath
	stdout.Reset()
	stderr.Reset()
	if exit := run(args, time.Now, &stdout, &stderr); exit != 1 {
		t.Fatalf("invalid exit=%d", exit)
	}
	if stdout.Len() != 0 {
		t.Fatal("invalid input emitted a readiness report")
	}
}

func writeJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	path := filepath.Join(directory, name)
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
