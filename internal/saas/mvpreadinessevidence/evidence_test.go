package mvpreadinessevidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

func TestBuildReadyFoundationDerivesEightPassedGates(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	catalog, index := canonicalFoundation(t)
	report := evidenceindex.Report{
		Schema: evidenceindex.ReportSchemaV1, Total: 57, Verified: 49,
		Missing: append([]string(nil), finalMVPControlIDs...), Rejected: []string{}, Expired: []string{},
	}
	input := validInput(now, true)

	receipt, err := build(catalog, index, report, input, strings.Repeat("1", 64), sourceDigests{
		catalog: strings.Repeat("2", 64), index: strings.Repeat("3", 64),
		trust: strings.Repeat("4", 64), approvals: strings.Repeat("5", 64),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.FoundationalControlCount != 49 || receipt.VerifiedFoundationalCount != 49 || len(receipt.Gates) != 8 {
		t.Fatalf("unexpected ready receipt: %+v", receipt)
	}
	for position, gate := range receipt.Gates {
		if gate.ID != GateID(finalMVPControlIDs[position]) || gate.Outcome != OutcomePassed || len(gate.EvidenceSHA256) != 64 {
			t.Fatalf("unexpected gate %d: %+v", position, gate)
		}
	}
}

func TestCanonicalControlIDsMatchRepositoryCatalog(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
	catalog, err := evidenceindex.LoadCatalog(filepath.Join(root, "api", "evidence", "v1", "external-control-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Controls) != len(canonicalControlIDs) {
		t.Fatalf("catalog controls=%d canonical controls=%d", len(catalog.Controls), len(canonicalControlIDs))
	}
	for position, control := range catalog.Controls {
		if control.ID != canonicalControlIDs[position] {
			t.Fatalf("catalog position %d=%s canonical=%s", position, control.ID, canonicalControlIDs[position])
		}
	}
}

func TestBuildPreservesMissingFoundationAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	catalog, index := canonicalFoundation(t)
	report := evidenceindex.Report{
		Schema: evidenceindex.ReportSchemaV1, Total: 57, Verified: 48,
		Missing: append([]string{"CP3-A"}, finalMVPControlIDs...), Rejected: []string{}, Expired: []string{},
	}

	receipt, err := build(catalog, index, report, validInput(now, false), strings.Repeat("1", 64), sourceDigests{
		catalog: strings.Repeat("2", 64), index: strings.Repeat("3", 64),
		trust: strings.Repeat("4", 64), approvals: strings.Repeat("5", 64),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.VerifiedFoundationalCount != 48 || receipt.MissingFoundationalCount != 1 || len(receipt.UnavailableFoundationalControls) != 1 || receipt.UnavailableFoundationalControls[0] != "CP3-A" {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
	if gateOutcome(t, receipt, GateMVPA) != OutcomeInconclusive || gateOutcome(t, receipt, GateMVPB) != OutcomeInconclusive {
		t.Fatalf("missing journey evidence must affect MVP-A and MVP-B: %+v", receipt.Gates)
	}
}

func TestBuildRejectsContradictoryReadinessAndCatalogSubstitution(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	catalog, index := canonicalFoundation(t)
	report := evidenceindex.Report{Schema: evidenceindex.ReportSchemaV1, Total: 57, Verified: 49, Missing: append([]string(nil), finalMVPControlIDs...), Rejected: []string{}, Expired: []string{}}
	digests := sourceDigests{catalog: strings.Repeat("2", 64), index: strings.Repeat("3", 64), trust: strings.Repeat("4", 64), approvals: strings.Repeat("5", 64)}

	if _, err := build(catalog, index, report, validInput(now, false), strings.Repeat("1", 64), digests, now); err == nil {
		t.Fatal("expected contradictory readiness rejection")
	}
	catalog.Controls[0].ID = "P99.9-A"
	if _, err := build(catalog, index, report, validInput(now, true), strings.Repeat("1", 64), digests, now); err == nil {
		t.Fatal("expected canonical catalog substitution rejection")
	}
}

func TestBuildRejectsPreexistingFinalMVPIndexEntry(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	catalog, index := canonicalFoundation(t)
	index.Entries = append(index.Entries, evidenceindex.Entry{ControlID: "MVP-A", ApprovalControl: "mvp_a", EvidenceSHA256: strings.Repeat("f", 64)})
	report := evidenceindex.Report{Schema: evidenceindex.ReportSchemaV1, Total: 57, Verified: 49, Missing: append([]string(nil), finalMVPControlIDs...), Rejected: []string{}, Expired: []string{}}
	digests := sourceDigests{catalog: strings.Repeat("2", 64), index: strings.Repeat("3", 64), trust: strings.Repeat("4", 64), approvals: strings.Repeat("5", 64)}
	if _, err := build(catalog, index, report, validInput(now, true), strings.Repeat("1", 64), digests, now); err == nil {
		t.Fatal("expected preexisting final MVP index entry rejection")
	}
}

func TestReadRegularRejectsPostOpenPathReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(path, []byte(`{"schema":"original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readRegularWithHook(path, maximumInputBytes, func() {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"schema":"replacement"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("a metadata path replaced after open must fail closed")
	}
}

func TestCollectVerifiesRealSignedFoundationBeforeDerivingReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	root := t.TempDir()
	artifacts := filepath.Join(root, "artifacts")
	approvalsDirectory := filepath.Join(root, "approvals")
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(approvalsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog, _ := canonicalFoundation(t)
	index := evidenceindex.Index{Schema: evidenceindex.IndexSchemaV1, Gate: evidenceindex.ExternalEvidenceGate, GeneratedAt: now.Add(-2 * time.Minute), Entries: []evidenceindex.Entry{}}
	controls := make([]string, 0, len(catalog.Controls))
	for _, control := range catalog.Controls {
		controls = append(controls, control.ApprovalControl)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{
		KeyID: "program-2026", Owner: "program-review", PublicKey: base64.StdEncoding.EncodeToString(public),
		Gates: []string{evidenceindex.ExternalEvidenceGate}, Controls: controls,
	}}}
	for position, control := range catalog.Controls[:49] {
		contents := []byte(fmt.Sprintf("authoritative dossier %d", position))
		dossierName := fmt.Sprintf("%02d.json", position)
		if err := os.WriteFile(filepath.Join(artifacts, dossierName), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(contents))
		entry := evidenceindex.Entry{
			ControlID: control.ID, ApprovalControl: control.ApprovalControl,
			DossierPath: "artifacts/" + dossierName, EvidenceRef: fmt.Sprintf("report://program/%02d", position), EvidenceSHA256: digest,
			Classification: evidenceindex.ExternalReview, Environment: evidenceindex.External, CollectedAt: now.Add(-time.Hour),
		}
		index.Entries = append(index.Entries, entry)
		approval := readiness.SignedApproval{
			Schema: readiness.ApprovalArtifactSchema, Gate: evidenceindex.ExternalEvidenceGate, Control: control.ApprovalControl,
			Decision: "approved", Owner: "program-review", KeyID: "program-2026", EvidenceRef: entry.EvidenceRef, EvidenceSHA256: digest,
			IssuedAt: now.Add(-30 * time.Minute).Format(time.RFC3339Nano), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano),
		}
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		writeJSON(t, filepath.Join(approvalsDirectory, fmt.Sprintf("%02d.json", position)), approval)
	}
	catalogPath := filepath.Join(root, "catalog.json")
	indexPath := filepath.Join(root, "index.json")
	trustPath := filepath.Join(root, "trust.json")
	inputPath := filepath.Join(root, "input.json")
	writeJSON(t, catalogPath, catalog)
	writeJSON(t, indexPath, index)
	writeJSON(t, trustPath, trust)
	writeJSON(t, inputPath, validInput(now, true))

	receipt, err := Collect(catalogPath, indexPath, root, trustPath, approvalsDirectory, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.VerifiedFoundationalCount != 49 || len(receipt.Gates) != 8 {
		t.Fatalf("unexpected collected receipt: %+v", receipt)
	}
}

func TestCollectRejectsSemanticallySubstitutedCanonicalCatalog(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	root := t.TempDir()
	catalog, _ := canonicalFoundation(t)
	catalog.Controls = append([]evidenceindex.Control(nil), catalog.Controls...)
	catalog.Controls[0].OwnerGroup = "substituted_owner"
	catalogPath := filepath.Join(root, "catalog.json")
	indexPath := filepath.Join(root, "index.json")
	trustPath := filepath.Join(root, "trust.json")
	inputPath := filepath.Join(root, "input.json")
	approvalsDirectory := filepath.Join(root, "approvals")
	if err := os.Mkdir(approvalsDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, catalogPath, catalog)
	writeJSON(t, indexPath, evidenceindex.Index{
		Schema: evidenceindex.IndexSchemaV1, Gate: evidenceindex.ExternalEvidenceGate,
		GeneratedAt: now.Add(-time.Minute), Entries: []evidenceindex.Entry{},
	})
	writeJSON(t, trustPath, readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{}})
	writeJSON(t, inputPath, validInput(now, false))

	if _, err := Collect(catalogPath, indexPath, root, trustPath, approvalsDirectory, inputPath, now); err == nil || !strings.Contains(err.Error(), "catalog is not canonical") {
		t.Fatalf("substituted final-MVP catalog error=%v", err)
	}
}

func canonicalFoundation(t *testing.T) (evidenceindex.Catalog, evidenceindex.Index) {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
	catalog, err := evidenceindex.LoadCatalog(filepath.Join(repository, "api", "evidence", "v1", "external-control-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]evidenceindex.Entry, 0, 49)
	for position, control := range catalog.Controls {
		if strings.HasPrefix(control.ID, "MVP-") {
			continue
		}
		entries = append(entries, evidenceindex.Entry{ControlID: control.ID, ApprovalControl: control.ApprovalControl, EvidenceSHA256: fmt.Sprintf("%064x", position+1)})
	}
	return catalog, evidenceindex.Index{Schema: evidenceindex.IndexSchemaV1, Gate: evidenceindex.ExternalEvidenceGate, Entries: entries}
}

func validInput(now time.Time, expected bool) Input {
	return Input{
		Schema: InputSchemaV1, Classification: "external_review", Environment: "external",
		ReadinessID: "mvp-readiness-2026-08", ProgramVersion: "p0-p12-v1",
		ReviewDecisionSHA256: strings.Repeat("a", 64), GeneratedAt: now.Add(-time.Minute), ExpectedReady: expected,
	}
}

func gateOutcome(t *testing.T, receipt Receipt, id GateID) Outcome {
	t.Helper()
	for _, gate := range receipt.Gates {
		if gate.ID == id {
			return gate.Outcome
		}
	}
	t.Fatalf("gate %s missing", id)
	return ""
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}
