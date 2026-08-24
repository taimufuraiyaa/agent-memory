package evidenceindex

import (
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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

func TestCanonicalCatalogMatchesEveryExternalEvidenceMatrixControl(t *testing.T) {
	root := repositoryRoot(t)
	catalog, err := LoadCatalog(filepath.Join(root, "api", "evidence", "v1", "external-control-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := os.ReadFile(filepath.Join(root, "docs", "saas", "external-evidence-matrix.md"))
	if err != nil {
		t.Fatal(err)
	}
	matrixIDs := matrixControlIDs(string(matrix))
	catalogIDs := make([]string, 0, len(catalog.Controls))
	for _, control := range catalog.Controls {
		catalogIDs = append(catalogIDs, control.ID)
	}
	sort.Strings(catalogIDs)
	if len(matrixIDs) != 57 || len(catalogIDs) != 57 {
		t.Fatalf("matrix controls=%d catalog controls=%d, want 57 each", len(matrixIDs), len(catalogIDs))
	}
	if strings.Join(matrixIDs, "\n") != strings.Join(catalogIDs, "\n") {
		t.Fatal("canonical catalog does not match the external evidence matrix")
	}
	example, err := LoadIndex(filepath.Join(root, "docs", "saas", "external-evidence-index.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if example.Schema != IndexSchemaV1 || example.Gate != ExternalEvidenceGate || len(example.Entries) != 0 {
		t.Fatalf("external evidence example must remain a safe empty index: %+v", example)
	}
}

func TestCanonicalCatalogTrustAnchorRejectsSemanticSubstitution(t *testing.T) {
	root := repositoryRoot(t)
	catalog, err := LoadCatalog(filepath.Join(root, "api", "evidence", "v1", "external-control-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalCatalog(catalog); err != nil {
		t.Fatalf("repository catalog rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Catalog){
		"truncated": func(value *Catalog) {
			value.Controls = append([]Control(nil), value.Controls[:1]...)
		},
		"reordered": func(value *Catalog) {
			value.Controls = append([]Control(nil), value.Controls...)
			value.Controls[0], value.Controls[1] = value.Controls[1], value.Controls[0]
		},
		"approval control": func(value *Catalog) {
			value.Controls = append([]Control(nil), value.Controls...)
			value.Controls[0].ApprovalControl = "substituted_control"
		},
		"owner group": func(value *Catalog) {
			value.Controls = append([]Control(nil), value.Controls...)
			value.Controls[0].OwnerGroup = "substituted_owner"
		},
		"evidence requirement": func(value *Catalog) {
			value.Controls = append([]Control(nil), value.Controls...)
			value.Controls[0].EvidenceRequirement = "weaker substituted requirement"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := catalog
			mutate(&candidate)
			if err := validateCanonicalCatalog(candidate); err == nil {
				t.Fatal("substituted external-control catalog accepted")
			}
		})
	}
}

func TestVerifyRequiresEveryDossierAndMatchingCurrentApproval(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	firstDigest := writeDossier(t, root, "artifacts/first.json", "first reviewed dossier")
	secondDigest := writeDossier(t, root, "artifacts/second.pdf", "second reviewed dossier")
	catalog := Catalog{Schema: CatalogSchemaV1, Controls: []Control{
		{ID: "P0.1-A", ApprovalControl: "p0_1_a", OwnerGroup: "product_counsel", EvidenceRequirement: "signed launch decision"},
		{ID: "P1.2-A", ApprovalControl: "p1_2_a", OwnerGroup: "operations", EvidenceRequirement: "staging release receipt"},
	}}
	index := Index{Schema: IndexSchemaV1, Gate: ExternalEvidenceGate, GeneratedAt: now, Entries: []Entry{
		{ControlID: "P0.1-A", ApprovalControl: "p0_1_a", DossierPath: "artifacts/first.json", EvidenceRef: "report://product/launch", EvidenceSHA256: firstDigest, Classification: ExternalBusiness, Environment: External, CollectedAt: now.Add(-time.Hour)},
		{ControlID: "P1.2-A", ApprovalControl: "p1_2_a", DossierPath: "artifacts/second.pdf", EvidenceRef: "report://operations/staging-release", EvidenceSHA256: secondDigest, Classification: ExternalStaging, Environment: Staging, ReleaseID: "v1.2.3", CollectedAt: now.Add(-time.Hour)},
	}}
	bundle, approvals := signedApprovals(t, catalog, index, now)

	report, err := verify(catalog, index, root, bundle, approvals, now)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.Total != 2 || report.Verified != 2 || len(report.Missing) != 0 {
		t.Fatalf("ready report=%+v", report)
	}

	incomplete := index
	incomplete.Entries = incomplete.Entries[:1]
	report, err = verify(catalog, incomplete, root, bundle, approvals, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Verified != 1 || strings.Join(report.Missing, ",") != "P1.2-A" {
		t.Fatalf("incomplete report=%+v", report)
	}
}

func TestVerifyReportsExpiredAndRejectedCurrentDecisions(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	digest := writeDossier(t, root, "artifacts/review.json", "reviewed dossier")
	catalog := Catalog{Schema: CatalogSchemaV1, Controls: []Control{{ID: "P1.2-A", ApprovalControl: "p1_2_a", OwnerGroup: "operations", EvidenceRequirement: "staging release receipt"}}}
	index := Index{Schema: IndexSchemaV1, Gate: ExternalEvidenceGate, GeneratedAt: now, Entries: []Entry{{
		ControlID: "P1.2-A", ApprovalControl: "p1_2_a", DossierPath: "artifacts/review.json",
		EvidenceRef: "report://operations/release", EvidenceSHA256: digest,
		Classification: ExternalStaging, Environment: Staging, CollectedAt: now.Add(-time.Hour),
	}}}

	bundle, expiredApproval := signedApprovalsWithDecision(t, catalog, index, "approved", now.Add(-time.Minute), now)
	report, err := verify(catalog, index, root, bundle, expiredApproval, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || strings.Join(report.Expired, ",") != "P1.2-A" || report.Verified != 0 {
		t.Fatalf("expired report=%+v", report)
	}

	bundle, rejectedApproval := signedApprovalsWithDecision(t, catalog, index, "rejected", now.Add(time.Hour), now)
	report, err = verify(catalog, index, root, bundle, rejectedApproval, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || strings.Join(report.Rejected, ",") != "P1.2-A" || report.Verified != 0 {
		t.Fatalf("rejected report=%+v", report)
	}
}

func TestVerifyRejectsUnknownAndDuplicateIndexControls(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	digest := writeDossier(t, root, "artifacts/review.json", "reviewed dossier")
	catalog := Catalog{Schema: CatalogSchemaV1, Controls: []Control{{ID: "P1.2-A", ApprovalControl: "p1_2_a", OwnerGroup: "operations", EvidenceRequirement: "staging release receipt"}}}
	entry := Entry{ControlID: "P1.2-A", ApprovalControl: "p1_2_a", DossierPath: "artifacts/review.json", EvidenceRef: "report://operations/release", EvidenceSHA256: digest, Classification: ExternalStaging, Environment: Staging, CollectedAt: now.Add(-time.Hour)}
	valid := Index{Schema: IndexSchemaV1, Gate: ExternalEvidenceGate, GeneratedAt: now, Entries: []Entry{entry}}
	bundle, approvals := signedApprovals(t, catalog, valid, now)

	duplicate := valid
	duplicate.Entries = []Entry{entry, entry}
	if _, err := verify(catalog, duplicate, root, bundle, approvals, now); err == nil {
		t.Fatal("duplicate index control was accepted")
	}
	unknown := valid
	unknown.Entries = []Entry{entry}
	unknown.Entries[0].ControlID = "P1.3-A"
	if _, err := verify(catalog, unknown, root, bundle, approvals, now); err == nil {
		t.Fatal("unknown index control was accepted")
	}
}

func TestVerifyRejectsLocalEvidenceTraversalSymlinkAndDigestMismatch(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	digest := writeDossier(t, root, "artifacts/review.json", "reviewed dossier")
	catalog := Catalog{Schema: CatalogSchemaV1, Controls: []Control{{ID: "P1.2-A", ApprovalControl: "p1_2_a", OwnerGroup: "operations", EvidenceRequirement: "staging release receipt"}}}
	entry := Entry{ControlID: "P1.2-A", ApprovalControl: "p1_2_a", DossierPath: "artifacts/review.json", EvidenceRef: "report://operations/release", EvidenceSHA256: digest, Classification: ExternalStaging, Environment: Staging, CollectedAt: now.Add(-time.Hour)}
	index := Index{Schema: IndexSchemaV1, Gate: ExternalEvidenceGate, GeneratedAt: now, Entries: []Entry{entry}}
	bundle, approvals := signedApprovals(t, catalog, index, now)

	for name, mutate := range map[string]func(*Index){
		"local evidence": func(value *Index) { value.Entries[0].Classification = Classification("local_development") },
		"traversal":      func(value *Index) { value.Entries[0].DossierPath = "../review.json" },
		"digest mismatch": func(value *Index) {
			value.Entries[0].EvidenceSHA256 = strings.Repeat("0", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := index
			candidate.Entries = append([]Entry(nil), index.Entries...)
			mutate(&candidate)
			if _, err := verify(catalog, candidate, root, bundle, approvals, now); err == nil {
				t.Fatal("unsafe evidence index was accepted")
			}
		})
	}

	target := filepath.Join(root, "artifacts", "review.json")
	link := filepath.Join(root, "artifacts", "review-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	symlinkIndex := index
	symlinkIndex.Entries = append([]Entry(nil), index.Entries...)
	symlinkIndex.Entries[0].DossierPath = "artifacts/review-link.json"
	if _, err := verify(catalog, symlinkIndex, root, bundle, approvals, now); err == nil {
		t.Fatal("symlink dossier was accepted")
	}
}

func TestLoadIndexRejectsUnknownFieldsAndSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "index.json")
	contents := `{"schema":"agent-memory-external-evidence-index-v1","gate":"external_evidence","generated_at":"2026-08-08T12:00:00Z","entries":[],"unknown":true}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(path); err == nil {
		t.Fatal("unknown index field was accepted")
	}
	valid := filepath.Join(root, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"schema":"agent-memory-external-evidence-index-v1","gate":"external_evidence","generated_at":"2026-08-08T12:00:00Z","entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.json")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(link); err == nil {
		t.Fatal("symlink index was accepted")
	}
}

func TestDecodeStrictRegularRejectsPostOpenPathReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "index.json")
	if err := os.WriteFile(path, []byte(`{"schema":"original"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Schema string `json:"schema"`
	}
	err := decodeStrictRegularWithHook(path, maximumMetadataBytes, &decoded, func() {
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

func TestHashDossierRejectsPostOpenPathReplacement(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "artifacts", "review.txt")
	if err := os.WriteFile(path, []byte("original dossier"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := hashDossierWithHook(root, "artifacts/review.txt", func() {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement dossier"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("a dossier path replaced after open must fail closed")
	}
}

func TestHashDossierRejectsIntermediateDirectoryReplacedByOutsideSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "evidence")
	artifacts := filepath.Join(root, "artifacts")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "review.txt"), []byte("inside dossier"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "review.txt"), []byte("outside dossier"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := hashDossierWithHooks(root, "artifacts/review.txt", func() {
		if err := os.Rename(artifacts, filepath.Join(root, "original-artifacts")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, artifacts); err != nil {
			t.Fatal(err)
		}
	}, nil)
	if err == nil {
		t.Fatal("dossier redirected through an outside intermediate symlink was accepted")
	}
}

func TestHashDossierRejectsArtifactRootReplacementAfterCapture(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "evidence")
	replacement := filepath.Join(parent, "replacement")
	writeDossier(t, root, "artifacts/review.txt", "original dossier")
	writeDossier(t, replacement, "artifacts/review.txt", "replacement dossier")

	_, err := hashDossierWithHooks(root, "artifacts/review.txt", func() {
		if err := os.Rename(root, filepath.Join(parent, "original-root")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, root); err != nil {
			t.Fatal(err)
		}
	}, nil)
	if err == nil {
		t.Fatal("dossier verification succeeded after artifact-root replacement")
	}
}

func TestLoadApprovalsDirectoryRejectsMembershipChange(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "approved.json"), []byte(`{"schema":"agent-memory-release-approval-v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadApprovalsDirectoryWithHook(directory, func() {
		if err := os.WriteFile(filepath.Join(directory, "rejected.json"), []byte(`{"schema":"agent-memory-release-approval-v1"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("an approval added after the initial snapshot must fail closed")
	}
}

func TestCanonicalFileVerificationRejectsApprovalAddedDuringDossierVerification(t *testing.T) {
	fixture := canonicalFileVerificationFixture(t)
	_, err := verifyCanonicalFilesWithHook(
		fixture.catalogPath, fixture.indexPath, fixture.artifactRoot,
		fixture.trustPath, fixture.approvalsPath, fixture.now,
		func() {
			writeJSONFile(t, filepath.Join(fixture.approvalsPath, "newer-rejection.json"), fixture.approval)
		},
	)
	if err == nil {
		t.Fatal("approval added during dossier verification was accepted")
	}
}

func TestCanonicalFileVerificationReturnsStableReportAndSourceDigests(t *testing.T) {
	fixture := canonicalFileVerificationFixture(t)
	result, err := VerifyCanonicalFiles(
		fixture.catalogPath, fixture.indexPath, fixture.artifactRoot,
		fixture.trustPath, fixture.approvalsPath, fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.Ready || result.Report.Total != 57 || result.Report.Verified != 2 || len(result.Report.Missing) != 55 {
		t.Fatalf("unexpected report: %+v", result.Report)
	}
	for path, actual := range map[string]string{
		fixture.catalogPath: result.CatalogSHA256,
		fixture.indexPath:   result.IndexSHA256,
		fixture.trustPath:   result.TrustBundleSHA256,
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		expected := sha256.Sum256(contents)
		if actual != hex.EncodeToString(expected[:]) {
			t.Fatalf("source digest mismatch for %s", path)
		}
	}
	approvalSetHash := sha256.New()
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("approved-%d.json", index)
		approvalContents, err := os.ReadFile(filepath.Join(fixture.approvalsPath, name))
		if err != nil {
			t.Fatal(err)
		}
		approvalFileDigest := sha256.Sum256(approvalContents)
		fmt.Fprintf(approvalSetHash, "%s\x00%x\n", name, approvalFileDigest)
	}
	if result.ApprovalSetSHA256 != hex.EncodeToString(approvalSetHash.Sum(nil)) {
		t.Fatal("approval-set digest does not bind the stable source snapshot")
	}
}

func TestCanonicalFileVerificationRejectsEarlierDossierReplacedDuringLaterDossierPass(t *testing.T) {
	fixture := canonicalFileVerificationFixture(t)
	_, err := verifyCanonicalFilesWithHooks(
		fixture.catalogPath, fixture.indexPath, fixture.artifactRoot,
		fixture.trustPath, fixture.approvalsPath, fixture.now, nil,
		func(position int) {
			if position != 0 {
				return
			}
			if err := os.Rename(fixture.firstDossierPath, fixture.firstDossierPath+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.firstDossierPath, []byte("reviewed canonical dossier 0"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	)
	if err == nil {
		t.Fatal("an already hashed dossier replaced during a later dossier pass was accepted")
	}
}

func TestCanonicalFileVerificationRejectsArtifactRootReplacementDuringFinalization(t *testing.T) {
	fixture := canonicalFileVerificationFixture(t)
	replacement := t.TempDir()
	_, err := verifyCanonicalFilesWithFinalizationHook(
		fixture.catalogPath, fixture.indexPath, fixture.artifactRoot,
		fixture.trustPath, fixture.approvalsPath, fixture.now,
		func() {
			if err := os.Rename(fixture.artifactRoot, fixture.artifactRoot+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, fixture.artifactRoot); err != nil {
				t.Fatal(err)
			}
		},
	)
	if err == nil {
		t.Fatal("artifact root replaced during final source validation was accepted")
	}
}

func TestVerifyPreservesMissingApprovalWithoutOpeningDossier(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	catalog := Catalog{Schema: CatalogSchemaV1, Controls: []Control{{
		ID: "P1.2-A", ApprovalControl: "platform_design_review", OwnerGroup: "architecture_security_privacy_operations", EvidenceRequirement: "reviewed design",
	}}}
	index := Index{Schema: IndexSchemaV1, Gate: ExternalEvidenceGate, GeneratedAt: now, Entries: []Entry{{
		ControlID: "P1.2-A", ApprovalControl: "platform_design_review", DossierPath: "artifacts/missing.json",
		EvidenceRef: "report://platform/review", EvidenceSHA256: strings.Repeat("a", 64),
		Classification: ExternalReview, Environment: External, CollectedAt: now.Add(-time.Hour),
	}}}
	bundle, _ := signedApprovals(t, catalog, index, now)
	report, err := verify(catalog, index, t.TempDir(), bundle, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Verified != 0 || len(report.Missing) != 1 || report.Missing[0] != "P1.2-A" {
		t.Fatalf("unexpected missing-approval report: %+v", report)
	}
}

func TestCanonicalFileVerificationRejectsMetadataPathReplacementAfterLoad(t *testing.T) {
	fixture := canonicalFileVerificationFixture(t)
	_, err := verifyCanonicalFilesWithHook(
		fixture.catalogPath, fixture.indexPath, fixture.artifactRoot,
		fixture.trustPath, fixture.approvalsPath, fixture.now,
		func() {
			if err := os.Rename(fixture.indexPath, fixture.indexPath+".original"); err != nil {
				t.Fatal(err)
			}
			writeJSONFile(t, fixture.indexPath, fixture.index)
		},
	)
	if err == nil {
		t.Fatal("index path replaced after load was accepted")
	}
}

type canonicalFilesFixture struct {
	catalogPath, indexPath, artifactRoot, trustPath, approvalsPath string
	firstDossierPath                                               string
	now                                                            time.Time
	index                                                          Index
	approval                                                       readiness.SignedApproval
}

func canonicalFileVerificationFixture(t *testing.T) canonicalFilesFixture {
	t.Helper()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	repository := repositoryRoot(t)
	catalog, err := LoadCatalog(filepath.Join(repository, "api", "evidence", "v1", "external-control-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	index := Index{Schema: IndexSchemaV1, Gate: ExternalEvidenceGate, GeneratedAt: now, Entries: []Entry{}}
	for position, control := range catalog.Controls[:2] {
		relative := fmt.Sprintf("artifacts/review-%d.json", position)
		digest := writeDossier(t, root, relative, fmt.Sprintf("reviewed canonical dossier %d", position))
		index.Entries = append(index.Entries, Entry{
			ControlID: control.ID, ApprovalControl: control.ApprovalControl,
			DossierPath: relative, EvidenceRef: fmt.Sprintf("report://program/review-%d", position),
			EvidenceSHA256: digest, Classification: ExternalReview, Environment: External,
			CollectedAt: now.Add(-time.Hour),
		})
	}
	bundle, approvals := signedApprovals(t, catalog, index, now)
	if len(approvals) != 2 {
		t.Fatalf("approval count=%d", len(approvals))
	}
	approvalsPath := filepath.Join(root, "approvals")
	if err := os.Mkdir(approvalsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.json")
	indexPath := filepath.Join(root, "index.json")
	trustPath := filepath.Join(root, "trust.json")
	writeJSONFile(t, catalogPath, catalog)
	writeJSONFile(t, indexPath, index)
	writeJSONFile(t, trustPath, bundle)
	for position, approval := range approvals {
		writeJSONFile(t, filepath.Join(approvalsPath, fmt.Sprintf("approved-%d.json", position)), approval)
	}
	return canonicalFilesFixture{
		catalogPath: catalogPath, indexPath: indexPath, artifactRoot: root,
		trustPath: trustPath, approvalsPath: approvalsPath, now: now,
		firstDossierPath: filepath.Join(root, "artifacts", "review-0.json"),
		index:            index, approval: approvals[0],
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", ".."))
}

func matrixControlIDs(contents string) []string {
	result := []string{}
	for _, line := range strings.Split(contents, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Control ") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 3 {
			result = append(result, strings.TrimSpace(parts[1]))
		}
	}
	sort.Strings(result)
	return result
}

func writeDossier(t *testing.T, root, relative, contents string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(digest[:])
}

func signedApprovals(t *testing.T, catalog Catalog, index Index, now time.Time) (readiness.TrustBundle, []readiness.SignedApproval) {
	return signedApprovalsWithDecision(t, catalog, index, "approved", now.Add(24*time.Hour), now)
}

func signedApprovalsWithDecision(t *testing.T, catalog Catalog, index Index, decision string, expiresAt, now time.Time) (readiness.TrustBundle, []readiness.SignedApproval) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	controls := make([]string, 0, len(catalog.Controls))
	for _, control := range catalog.Controls {
		controls = append(controls, control.ApprovalControl)
	}
	bundle := readiness.TrustBundle{Schema: readiness.ApprovalTrustSchema, Keys: []readiness.TrustedApprover{{
		KeyID: "evidence-2026", Owner: "evidence-review", PublicKey: base64.StdEncoding.EncodeToString(public),
		Gates: []string{ExternalEvidenceGate}, Controls: controls,
	}}}
	approvals := make([]readiness.SignedApproval, 0, len(index.Entries))
	for _, entry := range index.Entries {
		approval := readiness.SignedApproval{
			Schema: readiness.ApprovalArtifactSchema, Gate: ExternalEvidenceGate, Control: entry.ApprovalControl,
			Decision: decision, Owner: "evidence-review", KeyID: "evidence-2026", EvidenceRef: entry.EvidenceRef,
			EvidenceSHA256: entry.EvidenceSHA256, IssuedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano),
		}
		payload, err := readiness.CanonicalApprovalPayload(approval)
		if err != nil {
			t.Fatal(err)
		}
		approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		approvals = append(approvals, approval)
	}
	return bundle, approvals
}
