package migrationcohortevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestEvaluateReadyRepresentativeCohort(t *testing.T) {
	input := readyInput()
	result, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.FormatCoverageComplete || !result.SizeCoverageComplete || !result.ReconciliationComplete {
		t.Fatalf("result=%+v", result)
	}
	if result.CheckCount != 9 || result.PassedCount != 9 || result.FailedCount != 0 || result.InconclusiveCount != 0 {
		t.Fatalf("result=%+v", result)
	}
	for index, check := range result.Checks {
		if check.ID != RequiredChecks()[index] {
			t.Fatalf("checks=%+v", result.Checks)
		}
	}
}

func TestEvaluateKeepsFailedCohortValidButUnready(t *testing.T) {
	input := readyInput()
	input.Ready = false
	input.FailedItemCount = 2
	input.ImportedItemCount -= 2
	setOutcome(input.Checks, CheckImportCompleted, OutcomeFailed)
	setOutcome(input.Checks, CheckFailuresReviewed, OutcomePassed)
	result, err := Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.FailedItemCount != 2 || result.FailedCount != 1 || !result.ReconciliationComplete {
		t.Fatalf("result=%+v", result)
	}
}

func TestEvaluateRejectsUnsafeOrContradictoryCohort(t *testing.T) {
	tests := map[string]func(*Input){
		"missing format":              func(input *Input) { input.Formats = input.Formats[:3] },
		"duplicate format":            func(input *Input) { input.Formats[3].Format = input.Formats[0].Format },
		"zero format":                 func(input *Input) { input.Formats[0].SourceCount = 0 },
		"missing size":                func(input *Input) { input.SizeBuckets = input.SizeBuckets[:2] },
		"duplicate size":              func(input *Input) { input.SizeBuckets[2].Bucket = input.SizeBuckets[0].Bucket },
		"zero size":                   func(input *Input) { input.SizeBuckets[0].SourceCount = 0 },
		"format total mismatch":       func(input *Input) { input.Formats[0].SourceCount++ },
		"size total mismatch":         func(input *Input) { input.SizeBuckets[0].SourceCount++ },
		"item arithmetic mismatch":    func(input *Input) { input.ExpectedItemCount++ },
		"unexplained loss green":      func(input *Input) { input.UnexplainedLossCount = 1 },
		"duplicate publication green": func(input *Input) { input.DuplicatePublicationCount = 1 },
		"failed item green":           func(input *Input) { input.FailedItemCount = 1; input.ImportedItemCount-- },
		"missing check":               func(input *Input) { input.Checks = input.Checks[:8] },
		"duplicate check":             func(input *Input) { input.Checks[8].ID = input.Checks[0].ID },
		"unknown outcome":             func(input *Input) { input.Checks[0].Outcome = "unknown" },
		"contradictory readiness":     func(input *Input) { input.Ready = false },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := readyInput()
			mutate(&input)
			if _, err := Evaluate(input); err == nil {
				t.Fatal("invalid cohort was accepted")
			}
		})
	}
}

func TestCollectBindsCompleteOpenedPlatformAndReleaseChain(t *testing.T) {
	root := repositoryRoot(t)
	inventoryPath := filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json")
	planPath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json")
	changePath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json")
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		t.Fatal(err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := writeJSON(t, "release.json", releaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	input := readyBoundInput(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	inputPath := writeJSON(t, "cohort.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil || !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.CohortID != input.CohortID {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linked, now); err == nil {
		t.Fatal("symlink input accepted")
	}
	var unknown map[string]any
	encoded, _ := json.Marshal(input)
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["filename"] = "private-book.pdf"
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("content-bearing unknown field accepted")
	}
	mutations := map[string]func(*Input){
		"local classification":    func(value *Input) { value.Classification = "local_development" },
		"release substitution":    func(value *Input) { value.ReleaseReceiptSHA256 = digest(99) },
		"pre-release cohort":      func(value *Input) { value.StartedAt = now.Add(-24 * time.Hour) },
		"stale report":            func(value *Input) { value.GeneratedAt = now.Add(-25 * time.Hour) },
		"unapproved consent":      func(value *Input) { value.ConsentApprovedAt = value.StartedAt.Add(time.Minute) },
		"expired monthly consent": func(value *Input) { value.ConsentApprovedAt = value.StartedAt.Add(-32 * 24 * time.Hour) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			invalid := input
			mutate(&invalid)
			if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, name+".json", invalid), now); err == nil {
				t.Fatal("unsafe cohort evidence accepted")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode=%v err=%v", info, err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("receipt overwrite accepted")
	}
}

func TestLoadReadyReceiptRevalidatesCanonicalContentAndExactDigest(t *testing.T) {
	root := repositoryRoot(t)
	inventoryPath := filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json")
	planPath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json")
	changePath := filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json")
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		t.Fatal(err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := writeJSON(t, "loader-release.json", releaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	input := readyBoundInput(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	inputPath := writeJSON(t, "loader-cohort-input.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "ready-cohort-receipt.json", receipt)
	loaded, exactDigest, err := LoadReadyReceipt(path)
	if err != nil || loaded.CohortID != receipt.CohortID || exactDigest != digestFile(t, path) {
		t.Fatalf("loaded=%+v digest=%q err=%v", loaded, exactDigest, err)
	}
	unready := receipt
	unready.Ready = false
	if _, _, err := LoadReadyReceipt(writeJSON(t, "unready-cohort-receipt.json", unready)); err == nil {
		t.Fatal("unready cohort receipt accepted")
	}
	tampered := receipt
	tampered.UnexplainedLossCount = 1
	if _, _, err := LoadReadyReceipt(writeJSON(t, "tampered-cohort-receipt.json", tampered)); err == nil {
		t.Fatal("tampered cohort receipt accepted")
	}
	linked := filepath.Join(t.TempDir(), "cohort-link.json")
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReadyReceipt(linked); err == nil {
		t.Fatal("symlink cohort receipt accepted")
	}
}

func readyInput() Input {
	checks := make([]Check, 0, len(RequiredChecks()))
	digestChars := "abcdef012"
	for index, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: strings.Repeat(string(digestChars[index]), 64)})
	}
	return Input{
		AccountCount: 3, LibraryCount: 4, SourceCount: 12, MemoryCount: 20, NoteCount: 8,
		ExpectedItemCount: 40, ImportedItemCount: 32, MergedItemCount: 5, SkippedItemCount: 3,
		Formats:     []FormatCoverage{{Format: FormatPDF, SourceCount: 4}, {Format: FormatEPUB, SourceCount: 3}, {Format: FormatMarkdown, SourceCount: 3}, {Format: FormatText, SourceCount: 2}},
		SizeBuckets: []SizeCoverage{{Bucket: SizeSmall, SourceCount: 5}, {Bucket: SizeMedium, SourceCount: 4}, {Bucket: SizeLarge, SourceCount: 3}},
		Ready:       true, Checks: checks,
	}
}

func setOutcome(checks []Check, id CheckID, outcome Outcome) {
	for index := range checks {
		if checks[index].ID == id {
			checks[index].Outcome = outcome
		}
	}
}

func readyBoundInput(now time.Time) Input {
	input := readyInput()
	input.Schema = InputSchemaV1
	input.Classification = "staging_external"
	input.Environment = "staging"
	input.CohortID = "internal-cohort-1"
	input.DatasetVersion = "representative-v1"
	input.ConsentVersion = "consent-v1"
	input.ImporterVersion = "ampb2-v2"
	input.InventoryID, input.InventoryReceiptSHA256 = "inventory", digest(1)
	input.PlanID, input.PlanReceiptSHA256 = "plan", digest(2)
	input.ChangeID, input.ChangeReceiptSHA256 = "change", digest(3)
	input.ReleaseID, input.ReleaseReceiptSHA256 = "staging-release", digest(4)
	input.CohortDecisionSHA256 = digest(5)
	input.CohortReportSHA256 = digest(6)
	input.ConsentApprovedAt = now.Add(-12 * time.Hour)
	input.StartedAt = now.Add(-6 * time.Hour)
	input.CompletedAt = now.Add(-2 * time.Hour)
	input.GeneratedAt = now.Add(-time.Hour)
	return input
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
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

func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest(30) }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
