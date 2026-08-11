package billingreconciliation

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

func TestCollectValidatesCompleteProductionFileChain(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventoryPath, planPath, changePath, inventory, plan, change := productionChain(t)
	releasePath := writeJSON(t, "release.json", productionReleaseMap())
	release, releaseDigest, err := platformrollback.LoadPassedReleaseForEnvironment(releasePath, "production")
	if err != nil {
		t.Fatal(err)
	}
	inputPath := writeJSON(t, "billing.json", validInput(now, inventory, plan, change, release, releaseDigest))
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InvoiceVarianceMicroUSD != 0 || receipt.SettlementVarianceMicroUSD != 0 || receipt.UsageVarianceQuantity != 0 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.PassedCount != 8 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linked, now); err == nil {
		t.Fatal("symlink accepted")
	}
	unknown := map[string]any{}
	encoded, _ := json.Marshal(validInput(now, inventory, plan, change, release, releaseDigest))
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["invoice_ids"] = []string{"secret"}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, writeJSON(t, "unknown.json", unknown), now); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestBuildDerivesAndPreservesHonestVariance(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.LedgerInvoicedMicroUSD -= 20
	input.MaximumInvoiceVarianceMicroUSD = 10
	input.Checks[1].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.InvoiceVarianceMicroUSD != 20 || receipt.VarianceBreachCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestLoadReadyRevalidatesBillingReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "ready.json", receipt)
	loaded, fileDigest, err := LoadReady(path)
	if err != nil || !loaded.Ready || fileDigest != digestFile(t, path) {
		t.Fatalf("loaded=%+v digest=%s err=%v", loaded, fileDigest, err)
	}
	receipt.InvoiceVarianceMicroUSD++
	if _, _, err := LoadReady(writeJSON(t, "forged.json", receipt)); err == nil {
		t.Fatal("forged derivation accepted")
	}
}

func TestBuildRejectsContradictoryIncompleteStaleUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platforminventory.Inventory, *platformchange.Receipt, *Input){
		"payment disabled": func(inv *platforminventory.Inventory, _ *platformchange.Receipt, _ *Input) {
			inv.ExternalIntegrations[0].Enabled = false
		},
		"known breach passed": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.LedgerInvoicedMicroUSD -= 20
			in.MaximumInvoiceVarianceMicroUSD = 10
		},
		"partial invoice coverage": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.MatchedInvoiceCount--
			in.Ready = false
		},
		"missing check": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.Checks = in.Checks[:7]
			in.Ready = false
		},
		"duplicate check": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.Checks[1].ID = in.Checks[0].ID
		},
		"unknown outcome": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.Checks[0].Outcome = "unknown"
			in.Ready = false
		},
		"unsafe version": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.ProcessorExportVersion = "person@example.test"
		},
		"period before release": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.PeriodStart = now.Add(-20 * time.Hour)
		},
		"overlong period": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.PeriodEnd = in.PeriodStart.Add(32 * 24 * time.Hour)
			in.ReconciledAt = in.PeriodEnd
			in.GeneratedAt = in.PeriodEnd
		},
		"stale reconciliation": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.ReconciledAt = now.Add(-25 * time.Hour)
		},
		"future generation": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.GeneratedAt = now.Add(time.Second)
		},
		"negative total": func(_ *platforminventory.Inventory, _ *platformchange.Receipt, in *Input) {
			in.ProviderInvoicedMicroUSD = -1
			in.Ready = false
		},
		"change binding": func(_ *platforminventory.Inventory, change *platformchange.Receipt, _ *Input) {
			change.PlanReceiptSHA256 = digest("9")
		},
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, input := validEvidence(now)
			mutate(&inventory, &change, &input)
			if _, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now); err == nil {
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
		t.Fatal("replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	_ = os.WriteFile(target, []byte("{}"), 0o600)
	linked := filepath.Join(t.TempDir(), "linked.json")
	_ = os.Symlink(target, linked)
	if err := Publish(linked, Receipt{}); err == nil {
		t.Fatal("symlink accepted")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Production, InventoryID: "production-inventory", ReceiptSHA256: digest("a"), ExternalIntegrations: []platforminventory.ExternalIntegration{{Kind: platforminventory.IntegrationPayment, Enabled: true}, {Kind: platforminventory.IntegrationEmail}, {Kind: platforminventory.IntegrationModel}}}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Production, PlanID: "production-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Production, ChangeID: "production-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-12 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "production", Namespace: "agent-memory-production", ReleaseID: "production-release", StartedAt: now.Add(-11 * time.Hour), CompletedAt: now.Add(-10 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	return inventory, plan, change, release, validInput(now, inventory, plan, change, release, digest("d"))
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) Input {
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Input{Schema: InputSchemaV1, Classification: "production_external", Environment: "production", ReconciliationID: "billing-recon-1", ProcessorExportVersion: "processor-v1", LedgerExportVersion: "ledger-v1", RecomputationVersion: "recompute-v1", TargetVersion: "targets-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, ProcessorInvoiceExportSHA256: digest("2"), ProcessorSettlementExportSHA256: digest("3"), InvoiceLedgerExportSHA256: digest("4"), UsageLedgerExportSHA256: digest("5"), UsageRecomputationSHA256: digest("6"), WebhookOrderingReportSHA256: digest("7"), TargetDecisionSHA256: digest("8"), TargetApprovedAt: now.Add(-9 * time.Hour), PeriodStart: now.Add(-8 * time.Hour), PeriodEnd: now.Add(-4 * time.Hour), ReconciledAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-30 * time.Minute), Currency: "USD", TenantSampleCount: 2, ProcessorInvoiceCount: 2, MatchedInvoiceCount: 2, ProcessorSettlementCount: 2, MatchedSettlementCount: 2, UsageSampleCount: 4, MatchedUsageSampleCount: 4, ProviderInvoicedMicroUSD: 12_000_000, LedgerInvoicedMicroUSD: 12_000_000, ProviderSettledMicroUSD: 11_500_000, LedgerSettledMicroUSD: 11_500_000, AuthoritativeUsageQuantity: 5_000, RecomputedUsageQuantity: 5_000, MaximumInvoiceVarianceMicroUSD: 10, MaximumSettlementVarianceMicroUSD: 10, MaximumUsageVarianceQuantity: 1, Ready: true, Checks: checks}
}

func productionChain(t *testing.T) (string, string, string, platforminventory.Inventory, platformplan.Plan, platformchange.Receipt) {
	t.Helper()
	root := repositoryRoot(t)
	var invMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-platform-inventory.production.example.json"), &invMap)
	invMap["inventory_id"] = "production-inventory"
	integrations := invMap["external_integrations"].([]any)
	integrations[0].(map[string]any)["enabled"] = true
	inventoryPath := writeJSON(t, "inventory.json", invMap)
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var planMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-infrastructure-plan.production.example.json"), &planMap)
	planMap["inventory_id"] = inventory.InventoryID
	planMap["inventory_receipt_sha256"] = inventory.ReceiptSHA256
	planMap["plan_id"] = "production-plan"
	planPath := writeJSON(t, "plan.json", planMap)
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		t.Fatal(err)
	}
	var changeMap map[string]any
	readJSON(t, filepath.Join(root, "docs/saas/self-managed-infrastructure-change.production.example.json"), &changeMap)
	changeMap["inventory_id"] = inventory.InventoryID
	changeMap["inventory_receipt_sha256"] = inventory.ReceiptSHA256
	changeMap["plan_id"] = plan.PlanID
	changeMap["plan_receipt_sha256"] = plan.ReceiptSHA256
	changeMap["change_id"] = "production-change"
	changePath := writeJSON(t, "change.json", changeMap)
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	return inventoryPath, planPath, changePath, inventory, plan, change
}
func productionReleaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "production", "namespace": "agent-memory-production", "kubernetes_context": "production-context", "release_id": "production-release", "started_at": "2026-08-10T04:30:00Z", "completed_at": "2026-08-10T05:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
func readJSON(t *testing.T, path string, dest any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, dest); err != nil {
		t.Fatal(err)
	}
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
func digest(c string) string { return strings.Repeat(c, 64) }
