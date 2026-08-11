package securityclosureevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestEvaluateDerivesCompleteCoverageAndCriticalHighClosure(t *testing.T) {
	sources := passingSources()
	findings := []Finding{
		{FingerprintSHA256: digest(10), Severity: SeverityCritical, Exploitability: ExploitabilityExploitable, State: FindingClosed, RetestOutcome: RetestPassed, EvidenceSHA256: digest(11)},
		{FingerprintSHA256: digest(12), Severity: SeverityHigh, Exploitability: ExploitabilityNotExploitable, State: FindingOpen, RetestOutcome: RetestNotRequired, EvidenceSHA256: digest(13)},
		{FingerprintSHA256: digest(14), Severity: SeverityMedium, Exploitability: ExploitabilityExploitable, State: FindingOpen, RetestOutcome: RetestNotRequired, EvidenceSHA256: digest(15)},
	}
	result, err := evaluate(sources, findings)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CoverageComplete || result.FindingCount != 3 || result.BlockingFindingCount != 1 || result.OpenBlockingFindingCount != 0 || result.RetestIncompleteCount != 0 || !result.Ready {
		t.Fatalf("unexpected evaluation: %+v", result)
	}
}

func TestEvaluatePreservesUnresolvedAndInconclusiveCriticalHighAsUnready(t *testing.T) {
	findings := []Finding{
		{FingerprintSHA256: digest(10), Severity: SeverityCritical, Exploitability: ExploitabilityExploitable, State: FindingOpen, RetestOutcome: RetestInconclusive, EvidenceSHA256: digest(11)},
		{FingerprintSHA256: digest(12), Severity: SeverityHigh, Exploitability: ExploitabilityInconclusive, State: FindingClosed, RetestOutcome: RetestFailed, EvidenceSHA256: digest(13)},
	}
	result, err := evaluate(passingSources(), findings)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || result.BlockingFindingCount != 2 || result.OpenBlockingFindingCount != 1 || result.RetestIncompleteCount != 2 || result.InconclusiveClassificationCount != 1 {
		t.Fatalf("unresolved findings not derived: %+v", result)
	}
}

func TestEvaluateTreatsAnyInconclusiveExploitabilityAsUnready(t *testing.T) {
	findings := []Finding{{FingerprintSHA256: digest(10), Severity: SeverityMedium, Exploitability: ExploitabilityInconclusive, State: FindingOpen, RetestOutcome: RetestNotRequired, EvidenceSHA256: digest(11)}}
	result, err := evaluate(passingSources(), findings)
	if err != nil || result.Ready || result.InconclusiveClassificationCount != 1 {
		t.Fatalf("inconclusive classification not blocked: %+v err=%v", result, err)
	}
}

func TestEvaluateRejectsReplayMissingSourcesAndImpossibleLifecycle(t *testing.T) {
	base := []Finding{{FingerprintSHA256: digest(10), Severity: SeverityCritical, Exploitability: ExploitabilityExploitable, State: FindingClosed, RetestOutcome: RetestPassed, EvidenceSHA256: digest(11)}}
	for name, mutate := range map[string]func([]AssessmentSource, []Finding) ([]AssessmentSource, []Finding){
		"missing source": func(s []AssessmentSource, f []Finding) ([]AssessmentSource, []Finding) { return s[:3], f },
		"partial marked passed": func(s []AssessmentSource, f []Finding) ([]AssessmentSource, []Finding) {
			s[0].ObservedTargetCount--
			return s, f
		},
		"duplicate fingerprint": func(s []AssessmentSource, f []Finding) ([]AssessmentSource, []Finding) { return s, append(f, f[0]) },
		"open passed retest": func(s []AssessmentSource, f []Finding) ([]AssessmentSource, []Finding) {
			f[0].State = FindingOpen
			return s, f
		},
		"blocking not required": func(s []AssessmentSource, f []Finding) ([]AssessmentSource, []Finding) {
			f[0].RetestOutcome = RetestNotRequired
			return s, f
		},
	} {
		t.Run(name, func(t *testing.T) {
			sources := append([]AssessmentSource(nil), passingSources()...)
			findings := append([]Finding(nil), base...)
			sources, findings = mutate(sources, findings)
			if _, err := evaluate(sources, findings); err == nil {
				t.Fatal("invalid security closure evidence accepted")
			}
		})
	}
}

func TestBuildBindsStagingChainAndDerivesReadyAndUnreadyReceipts(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest(20), input, digest(21), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.CoverageComplete || receipt.BlockingFindingCount != 1 || receipt.OpenBlockingFindingCount != 0 || receipt.RetestIncompleteCount != 0 {
		t.Fatalf("unexpected ready receipt: %+v", receipt)
	}

	input.Findings[0].State = FindingOpen
	input.Findings[0].RetestOutcome = RetestInconclusive
	input.Ready = false
	for index := range input.Checks {
		if input.Checks[index].ID == CheckRemediationClosure || input.Checks[index].ID == CheckIndependentRetests || input.Checks[index].ID == CheckSecurityReview {
			input.Checks[index].Outcome = OutcomeFailed
		}
	}
	unready, err := build(inventory, plan, change, release, digest(20), input, digest(21), now)
	if err != nil || unready.Ready || unready.OpenBlockingFindingCount != 1 || unready.RetestIncompleteCount != 1 {
		t.Fatalf("valid unresolved receipt not preserved: %+v err=%v", unready, err)
	}

	input.ReleaseReceiptSHA256 = digest(22)
	if _, err := build(inventory, plan, change, release, digest(20), input, digest(21), now); err == nil {
		t.Fatal("release substitution accepted")
	}
}

func TestBuildRejectsPassedSecurityReviewWhenTechnicalClosureIsUnready(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Findings[0].State = FindingOpen
	input.Findings[0].RetestOutcome = RetestInconclusive
	input.Ready = false
	for index := range input.Checks {
		if input.Checks[index].ID == CheckRemediationClosure || input.Checks[index].ID == CheckIndependentRetests {
			input.Checks[index].Outcome = OutcomeFailed
		}
	}
	if _, err := build(inventory, plan, change, release, digest(20), input, digest(21), now); err == nil {
		t.Fatal("passed Security review accepted with unresolved technical closure")
	}
}

func TestCollectValidatesCompleteOpenedFileChainAndStrictInput(t *testing.T) {
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
	_, _, _, _, input := validEvidence(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	inputPath := writeJSON(t, "closure.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil || !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) {
		t.Fatalf("collect receipt=%+v err=%v", receipt, err)
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
	unknown["finding_text"] = "secret"
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("content-bearing unknown field accepted")
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

func passingSources() []AssessmentSource {
	result := make([]AssessmentSource, 0, len(requiredSources))
	for index, id := range requiredSources {
		result = append(result, AssessmentSource{ID: id, Outcome: OutcomePassed, ExpectedTargetCount: 10, ObservedTargetCount: 10, EvidenceSHA256: digest(index + 1)})
	}
	return result
}

func digest(value int) string { return fmt.Sprintf("%064x", value) }

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", ReceiptSHA256: digest(1)}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest(2)}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-12 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest(3)}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "staging-release", StartedAt: now.Add(-11 * time.Hour), CompletedAt: now.Add(-10 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(4)})
	}
	input := Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", ReviewID: "security-closure", RegisterVersion: "register-v1", ScopeVersion: "scope-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest(20), SourceManifestSHA256: digest(5), FindingRegisterSHA256: digest(6), ClassificationPolicySHA256: digest(7), RetestReportSHA256: digest(8), SecurityReviewSHA256: digest(9), SnapshotAt: now.Add(-6 * time.Hour), ReviewedAt: now.Add(-5 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Sources: passingSources(), Findings: []Finding{{FingerprintSHA256: digest(10), Severity: SeverityCritical, Exploitability: ExploitabilityExploitable, State: FindingClosed, RetestOutcome: RetestPassed, EvidenceSHA256: digest(11)}}, Checks: checks}
	return inventory, plan, change, release, input
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
func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest(30) }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
