package betaintegrityevidence

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaoperationsevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestBuildDerivesClosedSharedWindowIntegrityReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, slo, operations, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), slo, digest("slo"), operations, digest("operations"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.ChainCoverageComplete || !receipt.ArchiveReconciliationComplete || !receipt.IsolationClassificationComplete || !receipt.AuditIntegrityClassificationComplete || !receipt.FindingClosureComplete || receipt.IntegrityBreachCount != 0 || receipt.UnexplainedSignalCount != 0 || receipt.PassedCount != 9 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestLoadReadyRevalidatesIntegrityReceiptAndPrerequisites(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, slo, operations, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), slo, digest("slo"), operations, digest("operations"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "ready-integrity.json", receipt)
	loaded, fileDigest, err := LoadReady(path, slo, digest("slo"), operations, digest("operations"))
	if err != nil || !loaded.Ready || fileDigest != digestFile(t, path) {
		t.Fatalf("loaded=%+v digest=%s err=%v", loaded, fileDigest, err)
	}
	receipt.IntegrityBreachCount = 1
	if _, _, err := LoadReady(writeJSON(t, "forged-integrity.json", receipt), slo, digest("slo"), operations, digest("operations")); err == nil {
		t.Fatal("forged integrity receipt accepted")
	}
	if _, _, err := LoadReady(path, slo, digest("wrong"), operations, digest("operations")); err == nil {
		t.Fatal("wrong prerequisite digest accepted")
	}
}

func TestBuildPreservesHonestIntegrityAndExplanationFailures(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, slo, operations, input := validEvidence(now)
	input.ChainBreakCount = 1
	input.ArchiveVerifiedCount--
	input.ArchiveMissingCount = 1
	input.IsolationExplainedSignalCount--
	input.IsolationUnexplainedSignalCount = 1
	input.AuditIntegrityExplainedSignalCount--
	input.AuditIntegrityUnclassifiedSignalCount = 1
	input.ClosedAnomalyFindingCount--
	input.OpenAnomalyFindingCount = 1
	for _, id := range []CheckID{CheckAuditChain, CheckArchiveReconcile, CheckAuditIntegrityClassify, CheckAnomalyClosed, CheckNoUnexplainedIsolation} {
		setOutcome(input.Checks, id, OutcomeFailed)
	}
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("release"), slo, digest("slo"), operations, digest("operations"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.IntegrityBreachCount != 2 || receipt.UnexplainedSignalCount != 1 || receipt.OpenFindingCount != 1 || receipt.FailedCount != 5 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *betasloevidence.Receipt, *betaoperationsevidence.Receipt, *Input){
		"chain break passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.ChainBreakCount = 1
			input.Ready = false
		},
		"archive missing passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.ArchiveVerifiedCount--
			input.ArchiveMissingCount = 1
			input.Ready = false
		},
		"unclassified passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.IsolationExplainedSignalCount--
			input.IsolationUnclassifiedSignalCount = 1
			input.Ready = false
		},
		"unexplained passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.IsolationExplainedSignalCount--
			input.IsolationUnexplainedSignalCount = 1
			input.Ready = false
		},
		"open finding passed": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.ClosedAnomalyFindingCount--
			input.OpenAnomalyFindingCount = 1
			input.Ready = false
		},
		"empty audit export": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.AuditEventCount = 0
			input.ChainVerifiedEventCount = 0
			input.ArchiveExpectedCount = 0
			input.ArchiveVerifiedCount = 0
		},
		"archive mismatch": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.ArchiveExpectedCount++
		},
		"unsafe version": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.AnomalyEngineVersion = "person@example.test"
		},
		"wrong operations digest": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.BetaOperationsReceiptSHA256 = digest("wrong")
		},
		"unready operations": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, operations *betaoperationsevidence.Receipt, _ *Input) {
			operations.Ready = false
		},
		"cross window operations": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, operations *betaoperationsevidence.Receipt, _ *Input) {
			operations.WindowStart = operations.WindowStart.Add(time.Second)
		},
		"stale generation": func(_ *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, input *Input) {
			input.GeneratedAt = now.Add(-25 * time.Hour)
		},
		"change binding": func(change *platformchange.Receipt, _ *betasloevidence.Receipt, _ *betaoperationsevidence.Receipt, _ *Input) {
			change.PlanReceiptSHA256 = digest("wrong")
		},
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, slo, operations, input := validEvidence(now)
			mutate(&change, &slo, &operations, &input)
			if _, err := build(inventory, plan, change, release, digest("release"), slo, digest("slo"), operations, digest("operations"), input, digest("input"), now); err == nil {
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

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, betasloevidence.Receipt, betaoperationsevidence.Receipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("inventory")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("plan")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-72 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("change")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", StartedAt: now.Add(-60 * time.Hour), CompletedAt: now.Add(-58 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	slo := betasloevidence.Receipt{Input: betasloevidence.Input{Classification: "production_external", Environment: "production", ObservationID: "beta-slo", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), WindowStart: now.Add(-48 * time.Hour), WindowEnd: now.Add(-24 * time.Hour), Ready: true}, Schema: betasloevidence.ReceiptSchemaV1, InputSHA256: digest("slo-input"), CollectedAt: now.Add(-23 * time.Hour), ObservationDurationSeconds: 86_400, CoverageComplete: true, CheckCount: 6, PassedCount: 6}
	operations := betaoperationsevidence.Receipt{Input: betaoperationsevidence.Input{Classification: "production_external", Environment: "production", AssessmentID: "beta-operations", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), BetaSLOObservationID: slo.ObservationID, BetaSLOReceiptSHA256: digest("slo"), WindowStart: slo.WindowStart, WindowEnd: slo.WindowEnd, Ready: true}, Schema: betaoperationsevidence.ReceiptSchemaV1, InputSHA256: digest("operations-input"), CollectedAt: now.Add(-time.Hour), SampleCoverageComplete: true, CheckCount: 9, PassedCount: 9}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	input := Input{
		Schema: InputSchemaV1, Classification: "production_external", Environment: "production", ReviewID: "beta-integrity-2026-08",
		AnomalyEngineVersion: "rules-v1", AuditChainVerifierVersion: "chain-v1", ArchiveReconcilerVersion: "archive-v1", SignalClassificationVersion: "classification-v1", ResidualRiskPolicyVersion: "risk-v1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"),
		BetaSLOObservationID: slo.ObservationID, BetaSLOReceiptSHA256: digest("slo"), BetaOperationsAssessmentID: operations.AssessmentID, BetaOperationsReceiptSHA256: digest("operations"), WindowStart: slo.WindowStart, WindowEnd: slo.WindowEnd,
		AuditDatabaseChainReportSHA256: digest("chain-report"), AuditArchiveReconciliationSHA256: digest("archive-report"), IsolationSignalExportSHA256: digest("isolation-signals"), AuditIntegritySignalExportSHA256: digest("audit-signals"), AnomalyReportSHA256: digest("anomaly-report"), ResidualRiskDecisionSHA256: digest("risk-decision"), SecurityReviewSHA256: digest("security-review"),
		RiskPolicyApprovedAt: now.Add(-49 * time.Hour), SnapshotAt: now.Add(-23 * time.Hour), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour),
		AuditEventCount: 1000, ChainVerifiedEventCount: 1000, ChainBreakCount: 0, ArchiveExpectedCount: 1000, ArchiveVerifiedCount: 1000,
		IsolationSignalCount: 3, IsolationExplainedSignalCount: 3, AuditIntegritySignalCount: 2, AuditIntegrityExplainedSignalCount: 2,
		AnomalyFindingCount: 5, ClosedAnomalyFindingCount: 5, Ready: true, Checks: checks,
	}
	return inventory, plan, change, release, slo, operations, input
}

func setOutcome(checks []Check, id CheckID, outcome Outcome) {
	for index := range checks {
		if checks[index].ID == id {
			checks[index].Outcome = outcome
		}
	}
}

func digest(seed string) string {
	value := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%x", value)
}
