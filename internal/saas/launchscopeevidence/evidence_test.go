package launchscopeevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectNormalizesReadyLaunchScopeAndLegalReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	path := writeInput(t, "ready.json", readyInput(now))
	receipt, err := Collect(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.LegalPositionCount != 6 || receipt.LegalPassedCount != 6 || receipt.CheckCount != 8 || receipt.PassedCount != 8 || receipt.InputSHA256 != digestFile(t, path) {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestCollectPreservesCompleteAdverseReviewAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	input := readyInput(now)
	input.LegalPositions[2].Outcome = OutcomeFailed
	input.Checks[5].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := Collect(writeInput(t, "unready.json", input), now)
	if err != nil || receipt.Ready || receipt.LegalFailedCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectRejectsUnsafeIncompleteAndContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	base := readyInput(now)
	for name, mutate := range map[string]func(*Input){
		"classification": func(value *Input) { value.Classification = "local_development" },
		"digest":         func(value *Input) { value.JurisdictionMemoSHA256 = "bad" },
		"zero countries": func(value *Input) { value.LaunchCountryCount = 0 },
		"age":            func(value *Input) { value.MinimumAgeYears = 121 },
		"stale":          func(value *Input) { value.GeneratedAt = now.Add(-25 * time.Hour) },
		"future review":  func(value *Input) { value.LegalReviewCompletedAt = value.GeneratedAt.Add(time.Minute) },
		"missing legal":  func(value *Input) { value.LegalPositions = value.LegalPositions[:5] },
		"duplicate legal": func(value *Input) {
			value.LegalPositions[5].ID = value.LegalPositions[0].ID
		},
		"missing check": func(value *Input) { value.Checks = value.Checks[:7] },
		"duplicate check": func(value *Input) {
			value.Checks[7].ID = value.Checks[0].ID
		},
		"risk contradiction": func(value *Input) {
			value.BlockingRiskCount = 1
		},
		"readiness contradiction": func(value *Input) { value.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			input := cloneInput(base)
			mutate(&input)
			if _, err := Collect(writeInput(t, name+".json", input), now); err == nil {
				t.Fatal("unsafe launch-scope evidence accepted")
			}
		})
	}

	unknown := []byte(`{"schema":"agent-memory-launch-scope-input-v1","unknown":true}`)
	unknownPath := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknownPath, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(unknownPath, now); err == nil {
		t.Fatal("unknown field accepted")
	}

	inputPath := writeInput(t, "safe.json", base)
	linkPath := filepath.Join(t.TempDir(), "input-link.json")
	if err := os.Symlink(inputPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(linkPath, now); err == nil {
		t.Fatal("symlink input accepted")
	}
}

func TestCollectRequiresRiskCheckToMatchAggregates(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	input := readyInput(now)
	input.BlockingRiskCount = 2
	input.Checks[6].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := Collect(writeInput(t, "risks.json", input), now)
	if err != nil || receipt.Ready || receipt.BlockingRiskCount != 2 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
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
		t.Fatal("receipt overwrite accepted")
	}
}

func TestLoadReadyRevalidatesReceiptAndReturnsExactDigest(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	receipt, err := Collect(writeInput(t, "input.json", readyInput(now)), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeInput(t, "receipt.json", receipt)
	loaded, receiptDigest, err := LoadReady(path)
	if err != nil || !loaded.Ready || receiptDigest != digestFile(t, path) {
		t.Fatalf("loaded=%+v digest=%q err=%v", loaded, receiptDigest, err)
	}
}

func TestLoadReadyRejectsUnreadyTamperedAndSymlinkReceipts(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	receipt, err := Collect(writeInput(t, "input.json", readyInput(now)), now)
	if err != nil {
		t.Fatal(err)
	}

	unready := receipt
	unready.Ready = false
	if _, _, err := LoadReady(writeInput(t, "unready.json", unready)); err == nil {
		t.Fatal("unready receipt accepted")
	}
	tampered := receipt
	tampered.PassedCount--
	if _, _, err := LoadReady(writeInput(t, "tampered.json", tampered)); err == nil {
		t.Fatal("tampered receipt accepted")
	}
	path := writeInput(t, "safe-receipt.json", receipt)
	link := filepath.Join(t.TempDir(), "receipt-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReady(link); err == nil {
		t.Fatal("symlink receipt accepted")
	}
}

func readyInput(now time.Time) Input {
	positions := make([]LegalPosition, 0, len(RequiredLegalPositions()))
	for index, id := range RequiredLegalPositions() {
		positions = append(positions, LegalPosition{ID: id, PolicyCopySHA256: digest(index + 20), ReviewEvidenceSHA256: digest(index + 30), Outcome: OutcomePassed})
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for index, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(index + 40)})
	}
	return Input{Schema: InputSchemaV1, Classification: "external_business", Environment: "external",
		ScopeDecisionID: "launch-scope-2026", ScopeDecisionVersion: "scope-v1", JurisdictionPolicyVersion: "jurisdiction-v1", LegalReviewVersion: "legal-v1", RiskRegisterVersion: "risk-v1",
		DecisionRegisterSHA256: digest(1), LaunchScopeDecisionSHA256: digest(2), JurisdictionMemoSHA256: digest(3), PolicyManifestSHA256: digest(4), LegalReviewSHA256: digest(5), RiskRegisterSHA256: digest(6),
		ScopeApprovedAt: now.Add(-4 * time.Hour), LegalReviewCompletedAt: now.Add(-3 * time.Hour), GeneratedAt: now.Add(-time.Hour),
		LaunchCountryCount: 2, MinimumAgeYears: 18, SupportLanguageCount: 1, NoticeJurisdictionCount: 2,
		BlockingRiskCount: 0, UnownedRiskCount: 0, DeferredRiskCount: 1, Ready: true, LegalPositions: positions, Checks: checks}
}

func cloneInput(value Input) Input {
	value.LegalPositions = append([]LegalPosition(nil), value.LegalPositions...)
	value.Checks = append([]Check(nil), value.Checks...)
	return value
}

func writeInput(t *testing.T, name string, input any) string {
	t.Helper()
	contents, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func digestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func digest(value int) string { return fmt.Sprintf("%064x", value) }
