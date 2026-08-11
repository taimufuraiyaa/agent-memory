package betaoperationsevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestCollectValidatesCompleteProductionChainAndReadyBetaSLOReceipt(t *testing.T) {
	now := time.Date(2026, 8, 13, 18, 0, 0, 0, time.UTC)
	inventoryPath, planPath, changePath, inventory, plan, change := productionChain(t)
	releasePath := writeJSON(t, "release.json", productionReleaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedReleaseForEnvironment(releasePath, "production")
	if err != nil {
		t.Fatal(err)
	}
	betaInput := validBetaSLOInput(now, inventory, plan, change, release, releaseDigest)
	betaInputPath := writeJSON(t, "beta-slo-input.json", betaInput)
	betaReceipt, err := betasloevidence.Collect(inventoryPath, planPath, changePath, releasePath, betaInputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	betaReceiptPath := filepath.Join(t.TempDir(), "beta-slo-receipt.json")
	if err := betasloevidence.Publish(betaReceiptPath, betaReceipt); err != nil {
		t.Fatal(err)
	}
	betaDigest := digestFile(t, betaReceiptPath)
	_, _, _, _, _, input := validEvidence(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	input.BetaSLOObservationID, input.BetaSLOReceiptSHA256 = betaReceipt.ObservationID, betaDigest
	input.WindowStart, input.WindowEnd = betaReceipt.WindowStart, betaReceipt.WindowEnd
	inputPath := writeJSON(t, "beta-operations.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, betaReceiptPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.BetaSLOReceiptSHA256 != betaDigest {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, betaReceiptPath, linked, now); err == nil {
		t.Fatal("symlink accepted")
	}
	var unknown map[string]any
	encoded, _ := json.Marshal(input)
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["case_id"] = "unsafe"
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, betaReceiptPath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestBuildDerivesCompleteSameWindowDomainOperations(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, betaSLO, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), betaSLO, digest("beta-slo"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.SampleCoverageComplete || receipt.TargetBreachCount != 0 || receipt.SampleShortfallCount != 0 || receipt.DueCaseCount != 14 || receipt.PassedCount != 9 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	for _, result := range receipt.DomainResults {
		if !result.Reconciled || !result.SampleCoverageComplete || !result.TargetMet {
			t.Fatalf("unexpected domain result: %+v", result)
		}
	}
}

func TestBuildPreservesHonestTargetAndSampleFailures(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, betaSLO, input := validEvidence(now)
	input.Domains[0].WithinTargetCount--
	input.Domains[0].LateCaseCount++
	input.Domains[0].MaximumObservedDurationSeconds = input.Domains[0].MaximumTargetSeconds + 1
	input.Domains[3].MatchedSampleCount--
	input.Checks[3].Outcome = OutcomeFailed
	input.Checks[4].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("release"), betaSLO, digest("beta-slo"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.TargetBreachCount != 1 || receipt.SampleShortfallCount != 1 || receipt.FailedCount != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteUnsafeStaleAndCrossWindowEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *betasloevidence.Receipt, *Input){
		"late case passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.Domains[0].WithinTargetCount--
			input.Domains[0].LateCaseCount++
			input.Ready = false
		},
		"sample shortfall passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.Domains[3].MatchedSampleCount--
			input.Ready = false
		},
		"aggregate mismatch": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.Domains[1].DueCaseCount++
		},
		"empty domain with sample": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.Domains[2].RequiredSampleCount = 1
		},
		"duplicate domain": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.Domains[1].ID = input.Domains[0].ID
		},
		"missing check": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.Checks = input.Checks[:8]
			input.Ready = false
		},
		"unsafe version": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.SupportExportVersion = "person@example.test"
		},
		"cross window": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.WindowStart = input.WindowStart.Add(time.Second)
		},
		"wrong beta digest": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.BetaSLOReceiptSHA256 = digest("wrong")
		},
		"unready beta SLO": func(_ *platformchange.Receipt, beta *betasloevidence.Receipt, _ *Input) { beta.Ready = false },
		"stale generation": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.GeneratedAt = now.Add(-25 * time.Hour)
		},
		"late review": func(_ *platformchange.Receipt, beta *betasloevidence.Receipt, input *Input) {
			beta.WindowStart = beta.WindowStart.Add(-48 * time.Hour)
			beta.WindowEnd = beta.WindowEnd.Add(-48 * time.Hour)
			input.WindowStart, input.WindowEnd = beta.WindowStart, beta.WindowEnd
			input.TargetApprovedAt = input.WindowStart.Add(-time.Hour)
			input.AggregatedAt = input.WindowEnd.Add(time.Hour)
			input.ReviewedAt = input.AggregatedAt.Add(25 * time.Hour)
		},
		"future generation": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, input *Input) {
			input.GeneratedAt = now.Add(time.Second)
		},
		"change binding": func(change *platformchange.Receipt, _ *betasloevidence.Receipt, _ *Input) {
			change.PlanReceiptSHA256 = digest("wrong")
		},
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, betaSLO, input := validEvidence(now)
			mutate(&change, &betaSLO, &input)
			if _, err := build(inventory, plan, change, release, digest("release"), betaSLO, digest("beta-slo"), input, digest("input"), now); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnceAndRejectsSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("receipt replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	_ = os.WriteFile(target, []byte("{}"), 0o600)
	linked := filepath.Join(t.TempDir(), "linked.json")
	_ = os.Symlink(target, linked)
	if err := Publish(linked, Receipt{}); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestLoadReadyReloadsExactReceiptAndRejectsUnreadyOrTamperedReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, betaSLO, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), betaSLO, digest("beta-slo"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "ready.json", receipt)
	loaded, receiptDigest, err := LoadReady(path, betaSLO, digest("beta-slo"))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Ready || receiptDigest != digestFile(t, path) || loaded.DueCaseCount != 14 {
		t.Fatalf("unexpected loaded receipt: %+v digest=%s", loaded, receiptDigest)
	}
	for name, mutate := range map[string]func(*Receipt){
		"unready":            func(value *Receipt) { value.Ready = false },
		"totals changed":     func(value *Receipt) { value.DueCaseCount++ },
		"result changed":     func(value *Receipt) { value.DomainResults[0].TargetMet = false },
		"coverage changed":   func(value *Receipt) { value.SampleCoverageComplete = false },
		"window changed":     func(value *Receipt) { value.WindowStart = value.WindowStart.Add(time.Second) },
		"prerequisite empty": func(value *Receipt) { value.BetaSLOReceiptSHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := receipt
			candidate.Domains = append([]DomainAggregate(nil), receipt.Domains...)
			candidate.DomainResults = append([]DomainResult(nil), receipt.DomainResults...)
			candidate.Checks = append([]Check(nil), receipt.Checks...)
			mutate(&candidate)
			if _, _, err := LoadReady(writeJSON(t, name+".json", candidate), betaSLO, digest("beta-slo")); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, betasloevidence.Receipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("inventory")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("plan")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-72 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("change")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", StartedAt: now.Add(-60 * time.Hour), CompletedAt: now.Add(-58 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	betaSLO := betasloevidence.Receipt{Input: betasloevidence.Input{Classification: "production_external", Environment: "production", ObservationID: "beta-slo", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), WindowStart: now.Add(-48 * time.Hour), WindowEnd: now.Add(-24 * time.Hour), Ready: true}, Schema: betasloevidence.ReceiptSchemaV1, InputSHA256: digest("slo-input"), CollectedAt: now.Add(-23 * time.Hour), ObservationDurationSeconds: 86_400, CoverageComplete: true, CheckCount: 6, PassedCount: 6}
	domains := []DomainAggregate{
		{ID: DomainDeletion, DueCaseCount: 5, WithinTargetCount: 5, RequiredSampleCount: 2, SampledCaseCount: 2, MatchedSampleCount: 2, MaximumTargetSeconds: 3_600, MaximumObservedDurationSeconds: 1_800, EvidenceSHA256: digest("deletion")},
		{ID: DomainRightsNotice, DueCaseCount: 3, WithinTargetCount: 3, RequiredSampleCount: 2, SampledCaseCount: 2, MatchedSampleCount: 2, MaximumTargetSeconds: 172_800, MaximumObservedDurationSeconds: 7_200, EvidenceSHA256: digest("notice")},
		{ID: DomainAnomalyAlert, DueCaseCount: 0, MaximumTargetSeconds: 3_600, EvidenceSHA256: digest("anomaly")},
		{ID: DomainSupportCase, DueCaseCount: 6, WithinTargetCount: 6, RequiredSampleCount: 2, SampledCaseCount: 2, MatchedSampleCount: 2, MaximumTargetSeconds: 86_400, MaximumObservedDurationSeconds: 3_600, EvidenceSHA256: digest("support")},
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	input := Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", AssessmentID: "beta-operations-2026-08", AggregatePolicyVersion: "aggregates-v1", SamplePolicyVersion: "samples-v1", TargetVersion: "targets-v1", SupportExportVersion: "support-export-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), BetaSLOObservationID: betaSLO.ObservationID, BetaSLOReceiptSHA256: digest("beta-slo"), WindowStart: betaSLO.WindowStart, WindowEnd: betaSLO.WindowEnd, DeletionReceiptExportSHA256: digest("deletion-export"), NoticeCaseExportSHA256: digest("notice-export"), AnomalyCaseExportSHA256: digest("anomaly-export"), SupportCaseExportSHA256: digest("support-export"), SampleManifestSHA256: digest("samples"), TargetDecisionSHA256: digest("target-decision"), PrivacySecuritySupportReviewSHA256: digest("review"), TargetApprovedAt: now.Add(-49 * time.Hour), AggregatedAt: now.Add(-23 * time.Hour), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Domains: domains, Checks: checks}
	return inventory, plan, change, release, betaSLO, input
}

func digest(seed string) string {
	value := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%x", value)
}

func validBetaSLOInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) betasloevidence.Input {
	checks := make([]betasloevidence.Check, 0, len(betasloevidence.RequiredChecks()))
	for _, id := range betasloevidence.RequiredChecks() {
		checks = append(checks, betasloevidence.Check{ID: id, Outcome: betasloevidence.OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	values := map[betasloevidence.MetricID]int64{
		betasloevidence.MetricAPIAvailability: 999_500, betasloevidence.MetricSearchP95: 700_000,
		betasloevidence.MetricMemoryWriteP95: 250_000, betasloevidence.MetricStatusMetadataP95: 250_000,
		betasloevidence.MetricUploadAcceptanceP95: 1_500_000, betasloevidence.MetricNativeIndexingWithin60: 960_000,
	}
	metrics := make([]betasloevidence.MetricObservation, 0, len(betasloevidence.RequiredMetrics()))
	for _, id := range betasloevidence.RequiredMetrics() {
		metrics = append(metrics, betasloevidence.MetricObservation{ID: id, ObservedValue: values[id], ExpectedSampleCount: 288, ObservedSampleCount: 288, EvidenceSHA256: digest(string(id))})
	}
	return betasloevidence.Input{
		Schema: betasloevidence.InputSchemaV1, Classification: "production_external", Environment: "production",
		ObservationID: "beta-slo-2026-08", MetricSourceVersion: "prometheus-v1", QueryManifestVersion: "queries-v1", SLODefinitionVersion: "slo-v1", WindowDecisionVersion: "window-v1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256,
		ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		MetricExportSHA256: digest("metric-export"), QueryManifestSHA256: digest("query-manifest"), WindowDecisionSHA256: digest("window-decision"), SLODefinitionDecisionSHA256: digest("slo-decision"), ProductOperationsReviewSHA256: digest("review"),
		WindowApprovedAt: now.Add(-57 * time.Hour), WindowStart: now.Add(-48 * time.Hour), WindowEnd: now.Add(-24 * time.Hour), EvaluatedAt: now.Add(-23 * time.Hour), GeneratedAt: now.Add(-time.Hour), MinimumWindowSeconds: 86_400,
		Ready: true, Metrics: metrics, Checks: checks,
	}
}

func productionChain(t *testing.T) (string, string, string, platforminventory.Inventory, platformplan.Plan, platformchange.Receipt) {
	t.Helper()
	root := repositoryRoot(t)
	var inventoryMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-platform-inventory.production.example.json"), &inventoryMap)
	inventoryMap["inventory_id"] = "production-inventory"
	inventoryPath := writeJSON(t, "inventory.json", inventoryMap)
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var planMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-infrastructure-plan.production.example.json"), &planMap)
	planMap["inventory_id"], planMap["inventory_receipt_sha256"], planMap["plan_id"] = inventory.InventoryID, inventory.ReceiptSHA256, "production-plan"
	planPath := writeJSON(t, "plan.json", planMap)
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		t.Fatal(err)
	}
	var changeMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-infrastructure-change.production.example.json"), &changeMap)
	changeMap["inventory_id"], changeMap["inventory_receipt_sha256"] = inventory.InventoryID, inventory.ReceiptSHA256
	changeMap["plan_id"], changeMap["plan_receipt_sha256"], changeMap["change_id"] = plan.PlanID, plan.ReceiptSHA256, "production-change"
	changePath := writeJSON(t, "change.json", changeMap)
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	return inventoryPath, planPath, changePath, inventory, plan, change
}

func productionReleaseMap() map[string]any {
	image := func(name string) string {
		return "registry.example/agent-memory-" + name + "@sha256:" + digest("image")
	}
	return map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "production", "namespace": "agent-memory-production", "kubernetes_context": "production-context", "release_id": "production-release", "started_at": "2026-08-10T04:30:00Z", "completed_at": "2026-08-10T05:00:00Z", "outcome": "passed",
		"images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"},
		"deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false},
	}
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
	path := filepath.Join(t.TempDir(), name)
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSON(t *testing.T, path string, destination any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		t.Fatal(err)
	}
}

func digestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := sha256.Sum256(contents)
	return fmt.Sprintf("%x", value)
}
