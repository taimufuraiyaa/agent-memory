package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	catalog := loadCanonicalCatalog(t)
	index := evidenceindex.Index{Schema: evidenceindex.IndexSchemaV1, Gate: evidenceindex.ExternalEvidenceGate, GeneratedAt: now, Entries: []evidenceindex.Entry{}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	approvalControls := make([]string, 0, len(catalog.Controls))
	approvals := make([]readiness.SignedApproval, 0, len(catalog.Controls))
	firstDossierContents := "private reviewed dossier contents"
	firstDossierPath := ""
	for position, control := range catalog.Controls {
		dossierContents := firstDossierContents
		if position > 0 {
			dossierContents = "private reviewed dossier " + control.ID
		}
		dossierName := control.ApprovalControl + ".json"
		dossierPath := filepath.Join(artifacts, "artifacts", dossierName)
		if err := os.WriteFile(dossierPath, []byte(dossierContents), 0o600); err != nil {
			t.Fatal(err)
		}
		if position == 0 {
			firstDossierPath = dossierPath
		}
		digestBytes := sha256.Sum256([]byte(dossierContents))
		digest := hex.EncodeToString(digestBytes[:])
		entry := evidenceindex.Entry{
			ControlID: control.ID, ApprovalControl: control.ApprovalControl,
			DossierPath: "artifacts/" + dossierName,
			EvidenceRef: "report://program/" + control.ApprovalControl, EvidenceSHA256: digest,
			Classification: evidenceindex.ExternalReview, Environment: evidenceindex.External, CollectedAt: now.Add(-time.Hour),
		}
		index.Entries = append(index.Entries, entry)
		approval := readiness.SignedApproval{
			Schema: readiness.ApprovalArtifactSchema, Gate: evidenceindex.ExternalEvidenceGate, Control: control.ApprovalControl,
			Decision: "approved", Owner: "program-review", KeyID: "program-2026",
			EvidenceRef: entry.EvidenceRef, EvidenceSHA256: digest,
			IssuedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
		}
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		approvals = append(approvals, approval)
		approvalControls = append(approvalControls, control.ApprovalControl)
	}
	trust := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{
		KeyID: "program-2026", Owner: "program-review", PublicKey: base64.StdEncoding.EncodeToString(public),
		Gates: []string{evidenceindex.ExternalEvidenceGate}, Controls: approvalControls,
	}}}
	catalogPath := writeJSON(t, root, "catalog.json", catalog)
	indexPath := writeJSON(t, root, "index.json", index)
	trustPath := writeJSON(t, root, "trust.json", trust)
	approvalsPath := filepath.Join(root, "approvals")
	if err := os.Mkdir(approvalsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for position, approval := range approvals {
		writeJSON(t, approvalsPath, fmt.Sprintf("%02d.json", position), approval)
	}
	args := []string{"--catalog", catalogPath, "--index", indexPath, "--artifacts-root", artifacts, "--trust", trustPath, "--approvals-dir", approvalsPath, "--at", now.Format(time.RFC3339)}
	var stdout, stderr bytes.Buffer
	if exit := run(args, time.Now, &stdout, &stderr); exit != 0 {
		t.Fatalf("ready exit=%d stderr=%s", exit, stderr.String())
	}
	if strings.Contains(stdout.String(), firstDossierContents) || strings.Contains(stdout.String(), firstDossierPath) || strings.Contains(stdout.String(), approvals[0].Signature) || strings.Contains(stdout.String(), trust.Keys[0].PublicKey) || strings.Contains(stdout.String(), approvals[0].Owner) {
		t.Fatal("content-free report leaked evidence, path, signature, key, or owner")
	}
	var report evidenceindex.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || !report.Ready || report.Verified != 57 {
		t.Fatalf("ready report=%+v err=%v", report, err)
	}

	index.Entries = append([]evidenceindex.Entry(nil), index.Entries[1:]...)
	incompletePath := writeJSON(t, root, "incomplete.json", index)
	args[3] = incompletePath
	stdout.Reset()
	stderr.Reset()
	if exit := run(args, time.Now, &stdout, &stderr); exit != 3 {
		t.Fatalf("incomplete exit=%d stderr=%s", exit, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Ready || strings.Join(report.Missing, ",") != "P0.1-A" {
		t.Fatalf("incomplete report=%+v err=%v", report, err)
	}

	substituted := catalog
	substituted.Controls = append([]evidenceindex.Control(nil), catalog.Controls...)
	substituted.Controls[0].EvidenceRequirement = "weaker substituted requirement"
	substitutedPath := writeJSON(t, root, "substituted-catalog.json", substituted)
	args[1] = substitutedPath
	stdout.Reset()
	stderr.Reset()
	if exit := run(args, time.Now, &stdout, &stderr); exit != 1 {
		t.Fatalf("substituted catalog exit=%d stderr=%s", exit, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatal("substituted catalog emitted a readiness report")
	}
	args[1] = catalogPath

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

func loadCanonicalCatalog(t *testing.T) evidenceindex.Catalog {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	catalog, err := evidenceindex.LoadCatalog(filepath.Join(root, "api", "evidence", "v1", "external-control-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	return catalog
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
