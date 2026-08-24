package privacyreviewevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectNormalizesReadyReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	path := writeInput(t, "ready.json", readyInput(now))
	receipt, err := Collect(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.SurfaceCount != 4 || receipt.SurfacePassedCount != 4 || receipt.ContractCount != 5 || receipt.ContractPassedCount != 5 || receipt.CheckCount != 8 || receipt.PassedCount != 8 || receipt.InputSHA256 != digestFile(t, path) {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestCollectPreservesCompleteAdverseReviewAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	input := readyInput(now)
	input.Surfaces[1].Outcome = OutcomeFailed
	input.Checks[0].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := Collect(writeInput(t, "unready.json", input), now)
	if err != nil || receipt.Ready || receipt.SurfaceFailedCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectRejectsUnsafeIncompleteAndContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	base := readyInput(now)
	for name, mutate := range map[string]func(*Input){
		"classification":          func(v *Input) { v.Classification = "local_development" },
		"digest":                  func(v *Input) { v.OpenAPISHA256 = "bad" },
		"stale":                   func(v *Input) { v.GeneratedAt = now.Add(-25 * time.Hour) },
		"future review":           func(v *Input) { v.ReviewCompletedAt = v.GeneratedAt.Add(time.Minute) },
		"missing surface":         func(v *Input) { v.Surfaces = v.Surfaces[:3] },
		"duplicate surface":       func(v *Input) { v.Surfaces[3].ID = v.Surfaces[0].ID },
		"missing contract":        func(v *Input) { v.Contracts = v.Contracts[:4] },
		"duplicate contract":      func(v *Input) { v.Contracts[4].ID = v.Contracts[0].ID },
		"missing check":           func(v *Input) { v.Checks = v.Checks[:7] },
		"duplicate check":         func(v *Input) { v.Checks[7].ID = v.Checks[0].ID },
		"surface contradiction":   func(v *Input) { v.Surfaces[0].Outcome = OutcomeFailed },
		"readiness contradiction": func(v *Input) { v.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			input := cloneInput(base)
			mutate(&input)
			if _, err := Collect(writeInput(t, name+".json", input), now); err == nil {
				t.Fatal("unsafe review accepted")
			}
		})
	}
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema":"agent-memory-privacy-review-input-v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(unknown, now); err == nil {
		t.Fatal("unknown field accepted")
	}
	path := writeInput(t, "safe.json", base)
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(link, now); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestDecodeRejectsValidateThenOpenPathReplacement(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	original := readyInput(now)
	replacement := cloneInput(original)
	replacement.ReviewID = "replacement-review"
	path := writeInput(t, "review.json", original)
	replacementPath := writeInput(t, "replacement.json", replacement)

	var decoded Input
	_, err := decodeStrictRegularWithHook(path, &decoded, func() {
		if renameErr := os.Rename(replacementPath, path); renameErr != nil {
			t.Fatalf("replace validated input: %v", renameErr)
		}
	})
	if err == nil {
		t.Fatalf("validate-then-open replacement was accepted: %+v", decoded)
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("overwrite accepted")
	}
}

func readyInput(now time.Time) Input {
	surfaces := make([]Surface, 0, len(RequiredSurfaces()))
	for i, id := range RequiredSurfaces() {
		surfaces = append(surfaces, Surface{ID: id, RenderedSHA256: digest(i + 10), CopySHA256: digest(i + 20), AccessibilityReviewSHA256: digest(i + 30), Outcome: OutcomePassed})
	}
	contracts := make([]Contract, 0, len(RequiredContracts()))
	for i, id := range RequiredContracts() {
		contracts = append(contracts, Contract{ID: id, SchemaSHA256: digest(i + 40), CompatibilityReviewSHA256: digest(i + 50), Outcome: OutcomePassed})
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for i, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(i + 60)})
	}
	return Input{Schema: InputSchemaV1, Classification: "external_business", Environment: "external", ReviewID: "cp7-a-review-2026", DashboardBuildVersion: "dashboard-v1", OpenAPIVersion: "saas-v1", ReceiptManifestVersion: "rights-v1", DashboardBuildManifestSHA256: digest(1), OpenAPISHA256: digest(2), ReceiptSchemaManifestSHA256: digest(3), PrivacySignedReviewSHA256: digest(4), CounselSignedReviewSHA256: digest(5), ReviewStartedAt: now.Add(-4 * time.Hour), ReviewCompletedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Surfaces: surfaces, Contracts: contracts, Checks: checks}
}

func cloneInput(v Input) Input {
	v.Surfaces = append([]Surface(nil), v.Surfaces...)
	v.Contracts = append([]Contract(nil), v.Contracts...)
	v.Checks = append([]Check(nil), v.Checks...)
	return v
}
func writeInput(t *testing.T, name string, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
func digestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func digest(v int) string { return fmt.Sprintf("%064x", v) }
