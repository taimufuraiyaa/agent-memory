package gamedayevidence

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
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	input := validInput(now, inventory, plan, change, release)
	input.ReleaseReceiptSHA256 = releaseDigest
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.ReleaseReceiptSHA256 != releaseDigest || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.EnabledIntegrationCount != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestBuildNormalizesReadyReleaseBoundGameDays(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.DrillCount != 7 || receipt.CheckCount != 49 || receipt.PassedCount != 49 || receipt.ComponentSubjectCount != 8 || receipt.IntegrationSubjectCount != 3 || receipt.EnabledIntegrationCount != 2 || receipt.BundleCheckCount != 8 || receipt.BundlePassedCount != 8 {
		t.Fatalf("receipt=%+v", receipt)
	}
	for _, drill := range receipt.Drills {
		if drill.ElapsedSeconds != 360 || drill.DetectionSeconds != 30 || drill.AlertSeconds != 30 || drill.AcknowledgementSeconds != 30 || drill.ContainmentSeconds != 60 || drill.RecoverySeconds != 180 {
			t.Fatalf("drill=%+v", drill)
		}
	}
}

func TestBuildPreservesTargetBreachAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, input := validEvidence(now)
	input.Drills[0].TargetSeconds = 300
	input.BundleChecks[5].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now)
	if err != nil || receipt.Ready || receipt.TargetBreachCount != 1 || receipt.FailedCount != 0 || receipt.BundleFailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildRejectsIncompleteMismatchedOrContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Input){
		"missing drill":           func(v *Input) { v.Drills = v.Drills[:6] },
		"duplicate drill":         func(v *Input) { v.Drills[6].ID = v.Drills[0].ID },
		"component coverage":      func(v *Input) { v.Drills[2].ExercisedSubjectCount = 7 },
		"integration enabled":     func(v *Input) { v.Drills[3].EnabledSubjectCount = 1 },
		"causal timeline":         func(v *Input) { v.Drills[0].AlertedAt = v.Drills[0].DetectedAt.Add(-time.Second) },
		"missing check":           func(v *Input) { v.Drills[0].Checks = v.Drills[0].Checks[:6] },
		"target contradiction":    func(v *Input) { v.Drills[0].TargetSeconds = 300 },
		"readiness contradiction": func(v *Input) { v.Ready = false },
		"review artifact binding": func(v *Input) { v.BundleChecks[7].EvidenceSHA256 = digest("1") },
		"local classification":    func(v *Input) { v.Classification = "local_development" },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, input := validEvidence(now)
			mutate(&input)
			if _, err := build(inventory, plan, change, release, digest("d"), input, digest("e"), now); err == nil {
				t.Fatal("invalid game-day evidence accepted")
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
		t.Fatal("overwrite accepted")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "receipt-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, Receipt{}); err == nil {
		t.Fatal("symlink destination accepted")
	}
}

func TestDecodeRejectsUnknownFieldsAndSymlink(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema":"agent-memory-staging-game-day-input-v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var input Input
	if _, err := decodeStrictRegular(unknown, &input); err == nil {
		t.Fatal("unknown field accepted")
	}
	valid := writeJSON(t, "input.json", validInput(time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC), validInventoryForDecode(), platformplan.Plan{PlanID: "p"}, platformchange.Receipt{ChangeID: "c"}, platformrollback.ReleaseReceipt{ReleaseID: "r", CompletedAt: time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)}))
	link := filepath.Join(t.TempDir(), "input-link.json")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStrictRegular(link, &input); err == nil {
		t.Fatal("symlink input accepted")
	}
}

func TestDecodeRejectsValidateThenOpenPathReplacement(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, original := validEvidence(now)
	replacement := validInput(now, inventory, plan, change, release)
	replacement.BundleID = "replacement-bundle"
	path := writeJSON(t, "game-day.json", original)
	replacementPath := writeJSON(t, "replacement.json", replacement)

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

func validInventoryForDecode() platforminventory.Inventory {
	components := make([]platforminventory.Component, 8)
	return platforminventory.Inventory{InventoryID: "i", Components: components, ExternalIntegrations: []platforminventory.ExternalIntegration{{}, {}, {}}}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, Input) {
	components := []platforminventory.Component{}
	for _, kind := range []platforminventory.ComponentKind{platforminventory.ComponentKubernetes, platforminventory.ComponentIdentity, platforminventory.ComponentPostgres, platforminventory.ComponentObjectStorage, platforminventory.ComponentQueue, platforminventory.ComponentSecrets, platforminventory.ComponentObservability, platforminventory.ComponentBackup} {
		components = append(components, platforminventory.Component{Kind: kind})
	}
	integrations := []platforminventory.ExternalIntegration{{Kind: platforminventory.IntegrationPayment, Enabled: true}, {Kind: platforminventory.IntegrationEmail, Enabled: false}, {Kind: platforminventory.IntegrationModel, Enabled: true}}
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", GeneratedAt: now.Add(-12 * time.Hour), Components: components, ExternalIntegrations: integrations, ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-11 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "staging-release", StartedAt: now.Add(-10 * time.Hour), CompletedAt: now.Add(-9 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	input := validInput(now, inventory, plan, change, release)
	return inventory, plan, change, release, input
}

func validInput(now time.Time, inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt) Input {
	ids := RequiredDrills()
	drills := make([]InputDrill, 0, len(ids))
	base := release.CompletedAt.UTC().Add(30 * time.Minute)
	enabledIntegrations := 0
	for _, integration := range inventory.ExternalIntegrations {
		if integration.Enabled {
			enabledIntegrations++
		}
	}
	for index, id := range ids {
		started := base.Add(time.Duration(index) * 45 * time.Minute)
		subjects, enabled, exercised, disabled := 1, 0, 1, 0
		switch id {
		case DrillComponentFailure:
			subjects, exercised = len(inventory.Components), len(inventory.Components)
		case DrillIntegrationOutage:
			subjects, enabled, exercised, disabled = len(inventory.ExternalIntegrations), enabledIntegrations, enabledIntegrations, len(inventory.ExternalIntegrations)-enabledIntegrations
		case DrillIsolationAttempt, DrillIncompleteDeletion:
			subjects, exercised = 2, 2
		}
		checks := make([]Check, 0, 7)
		for n, checkID := range RequiredChecks(id) {
			checks = append(checks, Check{ID: checkID, Outcome: OutcomePassed, EvidenceSHA256: digest(string(rune('1' + n)))})
		}
		drills = append(drills, InputDrill{ID: id, DrillID: "drill-" + string(id), TargetPolicyVersion: "target-v1", TargetSeconds: 600, TargetDecisionSHA256: digest("1"), ReportSHA256: digest("2"), EvidenceManifestSHA256: digest("3"), StartedAt: started, DetectedAt: started.Add(30 * time.Second), AlertedAt: started.Add(60 * time.Second), AcknowledgedAt: started.Add(90 * time.Second), ContainedAt: started.Add(150 * time.Second), RecoveredAt: started.Add(330 * time.Second), CompletedAt: started.Add(360 * time.Second), SubjectCount: subjects, EnabledSubjectCount: enabled, ExercisedSubjectCount: exercised, DisabledStateVerifiedCount: disabled, Checks: checks})
	}
	bundleChecks := make([]Check, 0, 8)
	for _, id := range RequiredBundleChecks() {
		bundleChecks = append(bundleChecks, Check{ID: CheckID(id), Outcome: OutcomePassed, EvidenceSHA256: digest("a")})
	}
	bundleChecks[len(bundleChecks)-1].EvidenceSHA256 = digest("f")
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", BundleID: "game-day-bundle-1", ReviewVersion: "ops-security-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("d"), OperationsSecurityReviewSHA256: digest("f"), GeneratedAt: now.Add(-10 * time.Minute), Ready: true, Drills: drills, BundleChecks: bundleChecks}
}

func cloneInput(v Input) Input {
	v.Drills = append([]InputDrill(nil), v.Drills...)
	for i := range v.Drills {
		v.Drills[i].Checks = append([]Check(nil), v.Drills[i].Checks...)
	}
	v.BundleChecks = append([]Check(nil), v.BundleChecks...)
	return v
}
func digest(c string) string { return strings.Repeat(c, 64) }

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
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err = os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
func digestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func releaseMap() map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": "2026-08-10T05:00:00Z", "completed_at": "2026-08-10T06:00:00Z", "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
}
