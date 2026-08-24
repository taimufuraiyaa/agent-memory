package publicbetagateevidence

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaintegrityevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betaoperationsevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/betasloevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billingreconciliation"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

func TestBuildDerivesReadySharedWindowAbuseAndCostGate(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, billing, slo, operations, integrity, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("release"), billing, digest("billing"), slo, digest("slo"), operations, digest("operations"), integrity, digest("integrity"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || !receipt.AbuseClassificationComplete || !receipt.CostWithinCeiling || receipt.ActualCostPerActiveTenantMicroUSD != 334 || receipt.PassedCount != 9 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestAbuseAndCostFailures(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, billing, slo, operations, integrity, input := validEvidence(now)
	input.ClosedAbuseFindingCount--
	input.OpenLaunchBlockingAbuseFindingCount = 1
	input.ActualWindowCostMicroUSD = input.MaximumWindowCostMicroUSD + 1
	setOutcome(input.Checks, CheckNoBlockingAbuse, OutcomeFailed)
	setOutcome(input.Checks, CheckCostCeiling, OutcomeFailed)
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("release"), billing, digest("billing"), slo, digest("slo"), operations, digest("operations"), integrity, digest("integrity"), input, digest("input"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.CostWithinCeiling || receipt.FailedCount != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryEmptyOverflowAndCrossWindowEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*billingreconciliation.Receipt, *Input){
		"blocking abuse passed": func(_ *billingreconciliation.Receipt, input *Input) {
			input.ClosedAbuseFindingCount--
			input.OpenLaunchBlockingAbuseFindingCount = 1
			input.Ready = false
		},
		"unclassified passed": func(_ *billingreconciliation.Receipt, input *Input) {
			input.ClosedAbuseFindingCount--
			input.UnclassifiedAbuseFindingCount = 1
			input.Ready = false
		},
		"cost overrun passed": func(_ *billingreconciliation.Receipt, input *Input) {
			input.ActualWindowCostMicroUSD = input.MaximumWindowCostMicroUSD + 1
			input.Ready = false
		},
		"empty signup coverage": func(_ *billingreconciliation.Receipt, input *Input) { input.SignupAttemptCount = 0 },
		"empty tenant coverage": func(_ *billingreconciliation.Receipt, input *Input) { input.ActiveTenantCount = 0 },
		"finding mismatch":      func(_ *billingreconciliation.Receipt, input *Input) { input.AbuseFindingCount++ },
		"money overflow": func(_ *billingreconciliation.Receipt, input *Input) {
			input.ActualWindowCostMicroUSD = maximumMoneyMicroUSD + 1
		},
		"billing cross window": func(billing *billingreconciliation.Receipt, _ *Input) {
			billing.PeriodStart = billing.PeriodStart.Add(time.Second)
		},
		"unsafe version": func(_ *billingreconciliation.Receipt, input *Input) { input.AbusePolicyVersion = "person@example.test" },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, billing, slo, operations, integrity, input := validEvidence(now)
			mutate(&billing, &input)
			if _, err := build(inventory, plan, change, release, digest("release"), billing, digest("billing"), slo, digest("slo"), operations, digest("operations"), integrity, digest("integrity"), input, digest("input"), now); err == nil {
				t.Fatal("expected rejection")
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
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("receipt replaced")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, billingreconciliation.Receipt, betasloevidence.Receipt, betaoperationsevidence.Receipt, betaintegrityevidence.Receipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("inventory")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("plan")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-72 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("change")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", CompletedAt: now.Add(-58 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	start, end := now.Add(-48*time.Hour), now.Add(-24*time.Hour)
	billing := billingreconciliation.Receipt{Input: billingreconciliation.Input{InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), ReconciliationID: "billing-review", PeriodStart: start, PeriodEnd: end, Ready: true}, Schema: billingreconciliation.ReceiptSchemaV1}
	slo := betasloevidence.Receipt{Input: betasloevidence.Input{InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), ObservationID: "beta-slo", WindowStart: start, WindowEnd: end, Ready: true}, Schema: betasloevidence.ReceiptSchemaV1}
	operations := betaoperationsevidence.Receipt{Input: betaoperationsevidence.Input{InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), AssessmentID: "beta-operations", WindowStart: start, WindowEnd: end, Ready: true}, Schema: betaoperationsevidence.ReceiptSchemaV1}
	integrity := betaintegrityevidence.Receipt{Input: betaintegrityevidence.Input{InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), ReviewID: "beta-integrity", WindowStart: start, WindowEnd: end, Ready: true}, Schema: betaintegrityevidence.ReceiptSchemaV1}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(string(id))})
	}
	input := Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", GateReviewID: "public-beta-gate", AbusePolicyVersion: "abuse-v1", CostPolicyVersion: "cost-v1", ReviewPolicyVersion: "review-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("release"), BillingReconciliationID: billing.ReconciliationID, BillingReceiptSHA256: digest("billing"), BetaSLOObservationID: slo.ObservationID, BetaSLOReceiptSHA256: digest("slo"), BetaOperationsAssessmentID: operations.AssessmentID, BetaOperationsReceiptSHA256: digest("operations"), BetaIntegrityReviewID: integrity.ReviewID, BetaIntegrityReceiptSHA256: digest("integrity"), WindowStart: start, WindowEnd: end, AbuseExportSHA256: digest("abuse-export"), CostExportSHA256: digest("cost-export"), TargetDecisionSHA256: digest("targets"), DomainOwnerReviewSHA256: digest("review"), TargetApprovedAt: start.Add(-time.Hour), SnapshotAt: end.Add(time.Hour), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), SignupAttemptCount: 100, AbuseFindingCount: 3, ClosedAbuseFindingCount: 3, ActiveTenantCount: 3, ActualWindowCostMicroUSD: 1000, MaximumWindowCostMicroUSD: 2000, MaximumCostPerActiveTenantMicroUSD: 500, Ready: true, Checks: checks}
	return inventory, plan, change, release, billing, slo, operations, integrity, input
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
