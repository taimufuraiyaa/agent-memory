package alertevidence

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

func TestRequiredAlertsMatchDeployedPrometheusRuleContract(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "deploy", "saas", "observability", "prometheus-rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	alerts := RequiredAlerts()
	previousStart := -1
	for _, alert := range alerts {
		start := strings.Index(text, "- alert: "+alert.PrometheusName)
		if start < 0 {
			t.Fatalf("deployed rule missing fixed alert: %+v", alert)
		}
		block := text[start:]
		if next := strings.Index(block[len("- alert: "):], "- alert: "); next >= 0 {
			block = block[:len("- alert: ")+next]
		}
		if !strings.Contains(block, "severity: "+string(alert.Severity)) || !strings.Contains(block, "owner:") {
			t.Fatalf("deployed rule missing fixed alert mapping: %+v", alert)
		}
		if start <= previousStart {
			t.Fatal("deployed alert rules do not preserve the reviewed order")
		}
		previousStart = start
	}
}

func TestCollectValidatesCompleteOpenedFileChain(t *testing.T) {
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
	inputPath := writeJSON(t, "alerts.json", validInput(now, inventory, plan, change, release, releaseDigest))
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.AlertCount != 7 || receipt.PassedCount != 7 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.MaximumAcknowledgementSeconds != 180 || receipt.MaximumAcknowledgementTargetSeconds != 300 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	linkedInput := filepath.Join(t.TempDir(), "linked-alerts.json")
	if err := os.Symlink(inputPath, linkedInput); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, linkedInput, now); err == nil {
		t.Fatal("symlink input accepted")
	}

	unknown := map[string]any{}
	encoded, err := json.Marshal(validInput(now, inventory, plan, change, release, releaseDigest))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["route_url"] = "https://should-not-be-accepted.invalid"
	unknownPath := writeJSON(t, "unknown-alert-field.json", unknown)
	if _, err := Collect(inventoryPath, planPath, changePath, releasePath, unknownPath, now); err == nil {
		t.Fatal("unknown content-bearing input field accepted")
	}
}

func TestBuildCanonicalizesAlertsAndDerivesDurations(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Alerts[0], input.Alerts[6] = input.Alerts[6], input.Alerts[0]
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	first := receipt.Alerts[0]
	if !receipt.Ready || first.ID != RequiredAlerts()[0].ID || first.DeliverySeconds != 30 || first.EscalationSeconds != 120 || first.AcknowledgementSeconds != 180 || first.ResolutionSeconds != 300 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestBuildPreservesHonestFailureAndTargetBreach(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Alerts[0].MaximumDeliverySeconds = 10
	input.Alerts[0].Outcome = OutcomeFailed
	input.Alerts[1].Outcome = OutcomeInconclusive
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Ready || receipt.FailedCount != 1 || receipt.InconclusiveCount != 1 || receipt.TargetBreachCount != 1 {
		t.Fatalf("unexpected unready receipt: %+v", receipt)
	}
}

func TestBuildRejectsContradictoryIncompleteStaleUnsafeAndMisboundEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*platformchange.Receipt, *Input){
		"contradictory readiness": func(_ *platformchange.Receipt, in *Input) { in.Alerts[0].Outcome = OutcomeFailed },
		"missing alert":           func(_ *platformchange.Receipt, in *Input) { in.Alerts = in.Alerts[:6]; in.Ready = false },
		"duplicate alert":         func(_ *platformchange.Receipt, in *Input) { in.Alerts[1].ID = in.Alerts[0].ID },
		"wrong severity":          func(_ *platformchange.Receipt, in *Input) { in.Alerts[0].Severity = SeverityTicket },
		"unknown outcome":         func(_ *platformchange.Receipt, in *Input) { in.Alerts[0].Outcome = "unknown"; in.Ready = false },
		"unsafe owner slot":       func(_ *platformchange.Receipt, in *Input) { in.Alerts[0].OwnerSlotVersion = "person@example.test" },
		"pre-release trigger":     func(_ *platformchange.Receipt, in *Input) { in.Alerts[0].TriggeredAt = now.Add(-20 * time.Hour) },
		"delivery before trigger": func(_ *platformchange.Receipt, in *Input) {
			in.Alerts[0].DeliveredAt = in.Alerts[0].TriggeredAt.Add(-time.Second)
		},
		"ack before escalation": func(_ *platformchange.Receipt, in *Input) {
			in.Alerts[0].AcknowledgedAt = in.Alerts[0].EscalatedAt.Add(-time.Second)
		},
		"resolved after bundle": func(_ *platformchange.Receipt, in *Input) { in.Alerts[0].ResolvedAt = in.CompletedAt.Add(time.Second) },
		"known breach marked inconclusive": func(_ *platformchange.Receipt, in *Input) {
			in.Alerts[0].MaximumDeliverySeconds = 10
			in.Alerts[0].Outcome = OutcomeInconclusive
			in.Ready = false
		},
		"overlong bundle": func(_ *platformchange.Receipt, in *Input) {
			in.CompletedAt = in.StartedAt.Add(25 * time.Hour)
			in.GeneratedAt = in.CompletedAt
		},
		"stale bundle":   func(_ *platformchange.Receipt, in *Input) { in.GeneratedAt = now.Add(-25 * time.Hour) },
		"change binding": func(change *platformchange.Receipt, _ *Input) { change.PlanReceiptSHA256 = digest("9") },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, input := validEvidence(now)
			mutate(&change, &input)
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
		t.Fatal("existing destination replaced")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked.json")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	if err := Publish(linked, Receipt{}); err == nil {
		t.Fatal("symlink destination replaced")
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-12 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "staging-release", StartedAt: now.Add(-11 * time.Hour), CompletedAt: now.Add(-10 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	return inventory, plan, change, release, validInput(now, inventory, plan, change, release, digest("d"))
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string) Input {
	started := now.Add(-6 * time.Hour)
	alerts := make([]InputAlert, 0, len(RequiredAlerts()))
	for index, definition := range RequiredAlerts() {
		triggered := started.Add(time.Duration(index) * 30 * time.Minute)
		alerts = append(alerts, InputAlert{ID: definition.ID, Severity: definition.Severity, OwnerSlotVersion: "owner-slot-v1", TriggeredAt: triggered, DeliveredAt: triggered.Add(30 * time.Second), EscalatedAt: triggered.Add(2 * time.Minute), AcknowledgedAt: triggered.Add(3 * time.Minute), ResolvedAt: triggered.Add(5 * time.Minute), MaximumDeliverySeconds: 60, MaximumAcknowledgementSeconds: 300, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", BundleID: "alert-bundle-1", RuleSetVersion: "rules-v1", RouteVersion: "routes-v1", RosterVersion: "roster-v1", TargetVersion: "targets-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, RuleExportSHA256: digest("2"), RouteExportSHA256: digest("3"), OwnerRosterSHA256: digest("4"), SyntheticReportSHA256: digest("5"), TargetDecisionSHA256: digest("6"), StartedAt: started, CompletedAt: started.Add(4 * time.Hour), GeneratedAt: now.Add(-30 * time.Minute), Ready: true, Alerts: alerts}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
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
func digest(character string) string { return strings.Repeat(character, 64) }
func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
