package alphaevidence

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
	"github.com/taimufuraiyaa/agent-memory/internal/saas/stagingjourney"
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
	now := time.Date(2026, 10, 10, 20, 0, 0, 0, time.UTC)
	releasePath := writeJSON(t, "release.json", releaseMap(now.Add(-60*24*time.Hour)))
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	journey := readyJourneyReceipt(release, releaseDigest, now.Add(-59*24*time.Hour))
	journeyPath := writeJSON(t, "journey.json", journey)
	journeyDigest := digestFile(t, journeyPath)
	_, _, _, _, _, input := validEvidence(now)
	input.InventoryID, input.InventoryReceiptSHA256 = inventory.InventoryID, inventory.ReceiptSHA256
	input.PlanID, input.PlanReceiptSHA256 = plan.PlanID, plan.ReceiptSHA256
	input.ChangeID, input.ChangeReceiptSHA256 = change.ChangeID, change.ReceiptSHA256
	input.ReleaseID, input.ReleaseReceiptSHA256 = release.ReleaseID, releaseDigest
	input.JourneyReceiptSHA256 = journeyDigest
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(inventoryPath, planPath, changePath, releasePath, journeyPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.JourneyReceiptSHA256 != journeyDigest {
		t.Fatalf("receipt=%+v", receipt)
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
	if err := os.WriteFile(unknown, []byte(`{"schema":"agent-memory-staging-internal-alpha-input-v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var input Input
	if _, err := decodeStrictRegular(unknown, &input); err == nil {
		t.Fatal("unknown field accepted")
	}
	valid := writeJSON(t, "input.json", Input{Schema: InputSchemaV1})
	link := filepath.Join(t.TempDir(), "input-link.json")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStrictRegular(link, &input); err == nil {
		t.Fatal("symlink input accepted")
	}
}

func TestBuildNormalizesReadyInternalAlphaCohort(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, journey, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("d"), journey, digest("e"), input, digest("f"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.AccountCount != 3 || receipt.SourceCount != 8 || receipt.FormatCount != 4 || receipt.StageCount != 11 || receipt.CheckCount != 9 || receipt.SupportCaseCount != 2 || receipt.AlphaDays != 35 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestReceiptJSONUsesReceiptSchemaWithoutNestedInput(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, journey, input := validEvidence(now)
	receipt, err := build(inventory, plan, change, release, digest("d"), journey, digest("e"), input, digest("f"), now)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	if document["schema"] != ReceiptSchemaV1 || document["cohort_id"] != input.CohortID || document["input"] != nil || document["input_sha256"] != digest("f") {
		t.Fatalf("document=%v", document)
	}
}

func TestBuildPreservesSupportTargetBreachAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, journey, input := validEvidence(now)
	input.Support.MaxAcknowledgementSeconds = 901
	input.Checks[5].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, plan, change, release, digest("d"), journey, digest("e"), input, digest("f"), now)
	if err != nil || receipt.Ready || receipt.TargetBreachCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildRejectsCohortOrJourneyBeforeAppliedChange(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, plan, change, release, journey, input := validEvidence(now)
	change.GeneratedAt = input.StartedAt.Add(time.Hour)
	if _, err := build(inventory, plan, change, release, digest("d"), journey, digest("e"), input, digest("f"), now); err == nil {
		t.Fatal("pre-change alpha evidence accepted")
	}
}

func TestBuildRejectsIncompleteSubstitutedOrContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Input){
		"missing format":        func(v *Input) { v.Formats = v.Formats[:3] },
		"duplicate stage":       func(v *Input) { v.Stages[10].ID = v.Stages[0].ID },
		"over source cap":       func(v *Input) { v.ApprovedSourceCountCap = 7 },
		"format total mismatch": func(v *Input) { v.Formats[0].SourceCount++ },
		"deletion total mismatch": func(v *Input) {
			v.Formats[0].DeletedCount--
			v.Checks[3].Outcome = OutcomeFailed
			v.Ready = false
		},
		"renewal too early":     func(v *Input) { v.Stages[8].CompletedAt = v.Stages[2].CompletedAt.Add(27 * 24 * time.Hour) },
		"journey substitution":  func(v *Input) { v.JourneyReceiptSHA256 = digest("1") },
		"review substitution":   func(v *Input) { v.Checks[8].EvidenceSHA256 = digest("1") },
		"support substitution":  func(v *Input) { v.Support.EvidenceSHA256 = digest("1") },
		"support contradiction": func(v *Input) { v.Support.OpenCaseCount = 1 },
		"check contradiction":   func(v *Input) { v.Checks[4].Outcome = OutcomeFailed },
		"readiness contradiction": func(v *Input) {
			v.Ready = false
		},
		"local classification": func(v *Input) { v.Classification = "local_development" },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, plan, change, release, journey, input := validEvidence(now)
			mutate(&input)
			if _, err := build(inventory, plan, change, release, digest("d"), journey, digest("e"), input, digest("f"), now); err == nil {
				t.Fatal("invalid internal-alpha evidence accepted")
			}
		})
	}
}

func validEvidence(now time.Time) (platforminventory.Inventory, platformplan.Plan, platformchange.Receipt, platformrollback.ReleaseReceipt, stagingjourney.Receipt, Input) {
	components := make([]platforminventory.Component, 8)
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "staging-inventory", Components: components, ReceiptSHA256: digest("a")}
	plan := platformplan.Plan{Schema: platformplan.SchemaV1, Environment: platforminventory.Staging, PlanID: "staging-plan", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ReceiptSHA256: digest("b")}
	change := platformchange.Receipt{Schema: platformchange.SchemaV1, Environment: platforminventory.Staging, ChangeID: "staging-change", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, GeneratedAt: now.Add(-50 * 24 * time.Hour), Apply: platformchange.Apply{Outcome: platformchange.ApplySucceeded}, Rollback: platformchange.Rollback{Outcome: platformchange.RollbackNotRequired}, ResourceInventory: platformchange.ResourceInventory{Outcome: platformchange.ResourceInventoryCollected}, Drift: platformchange.Drift{Outcome: platformchange.DriftClean}, ReceiptSHA256: digest("c")}
	release := platformrollback.ReleaseReceipt{Schema: "agent-memory-kubernetes-release-receipt-v1", Environment: "staging", Namespace: "agent-memory-staging", ReleaseID: "staging-release", StartedAt: now.Add(-45 * 24 * time.Hour), CompletedAt: now.Add(-44 * 24 * time.Hour), Outcome: "passed", Migration: platformrollback.ReleaseStage{Outcome: "complete"}, Rollouts: platformrollback.ReleaseStage{Outcome: "healthy"}}
	journey := readyJourneyReceipt(release, digest("d"), now.Add(-43*24*time.Hour))
	started := now.Add(-36 * 24 * time.Hour)
	stageTimes := []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour, 5 * time.Hour, 6 * time.Hour, 7 * time.Hour, 8 * time.Hour, 32 * 24 * time.Hour, 34 * 24 * time.Hour, 35 * 24 * time.Hour}
	stages := make([]Stage, 0, len(RequiredStages()))
	for index, id := range RequiredStages() {
		stages = append(stages, Stage{ID: id, Outcome: OutcomePassed, CompletedAt: started.Add(stageTimes[index]), EvidenceSHA256: digest("1")})
	}
	formats := []Format{{ID: FormatPDF, SourceCount: 2, SourceBytes: 200, IndexedCount: 2, NonSensitiveCount: 2, DeletedCount: 2, EvidenceSHA256: digest("2")}, {ID: FormatEPUB, SourceCount: 2, SourceBytes: 200, IndexedCount: 2, NonSensitiveCount: 2, DeletedCount: 2, EvidenceSHA256: digest("2")}, {ID: FormatMarkdown, SourceCount: 2, SourceBytes: 200, IndexedCount: 2, NonSensitiveCount: 2, DeletedCount: 2, EvidenceSHA256: digest("2")}, {ID: FormatText, SourceCount: 2, SourceBytes: 200, IndexedCount: 2, NonSensitiveCount: 2, DeletedCount: 2, EvidenceSHA256: digest("2")}}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("3")})
	}
	checks[0].EvidenceSHA256 = digest("4")
	checks[1].EvidenceSHA256 = digest("5")
	checks[5].EvidenceSHA256 = digest("6")
	checks[6].EvidenceSHA256 = digest("7")
	checks[7].EvidenceSHA256 = digest("8")
	checks[8].EvidenceSHA256 = digest("9")
	input := Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", CohortID: "internal-alpha-1", ReviewVersion: "alpha-review-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, PlanID: plan.PlanID, PlanReceiptSHA256: plan.ReceiptSHA256, ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: digest("d"), JourneyReceiptSHA256: digest("e"), CohortDecisionSHA256: digest("4"), SourcePolicySHA256: digest("5"), SupportPolicySHA256: digest("6"), DeletionManifestSHA256: digest("7"), TraceAuditManifestSHA256: digest("8"), ProductQAOperationsReviewSHA256: digest("9"), StartedAt: started, CompletedAt: started.Add(35 * 24 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, AccountCount: 3, ApprovedSourceCountCap: 10, ApprovedSourceBytesCap: 1000, SourceCount: 8, SourceBytes: 800, Formats: formats, Stages: stages, Support: Support{CaseCount: 2, ResolvedCaseCount: 2, OpenCaseCount: 0, OverdueCaseCount: 0, SampledCaseCount: 2, MatchedSampleCount: 2, AcknowledgementTargetSeconds: 900, ResolutionTargetSeconds: 86400, MaxAcknowledgementSeconds: 600, MaxResolutionSeconds: 7200, EvidenceSHA256: digest("6")}, Deletion: Deletion{AccountRequestedCount: 3, AccountDeletedCount: 3, AccountPendingCount: 0, SourceRequestedCount: 8, SourceDeletedCount: 8, SourcePendingCount: 0, EvidenceSHA256: digest("7")}, Checks: checks}
	return inventory, plan, change, release, journey, input
}

func digest(value string) string { return strings.Repeat(value, 64) }

func readyJourneyReceipt(release platformrollback.ReleaseReceipt, releaseDigest string, collected time.Time) stagingjourney.Receipt {
	checks := func(prefix string) []stagingjourney.Check {
		ids := []stagingjourney.CheckID{stagingjourney.CheckAuthenticated, stagingjourney.CheckMemoryWriteAudited, stagingjourney.CheckMemorySearchAudit, stagingjourney.CheckExportReadyAudited, stagingjourney.CheckClientCleanup}
		values := make([]stagingjourney.Check, 0, len(ids))
		for index, id := range ids {
			values = append(values, stagingjourney.Check{ID: id, Outcome: stagingjourney.OutcomePassed, RequestID: fmt.Sprintf("%s0000000-0000-4000-8000-%012d", prefix, index+1)})
		}
		return values
	}
	return stagingjourney.Receipt{Schema: stagingjourney.ReceiptSchemaV1, Ready: true, Environment: "staging", ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest, CollectedAt: collected, Journeys: []stagingjourney.ReceiptJourney{{ClientKind: stagingjourney.HumanWeb, InputSHA256: digest("1"), TraceID: strings.Repeat("1", 32), StartedAt: collected.Add(-20 * time.Minute), CompletedAt: collected.Add(-15 * time.Minute), Checks: checks("1")}, {ClientKind: stagingjourney.ScopedAgent, InputSHA256: digest("2"), TraceID: strings.Repeat("2", 32), StartedAt: collected.Add(-10 * time.Minute), CompletedAt: collected.Add(-5 * time.Minute), Checks: checks("2")}}}
}

func releaseMap(completed time.Time) map[string]any {
	image := func(name string) string { return "registry.example/agent-memory-" + name + "@sha256:" + digest("a") }
	return map[string]any{"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging", "namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": "staging-release", "started_at": completed.Add(-time.Hour), "completed_at": completed, "outcome": "passed", "images": map[string]any{"api": image("api"), "worker": image("worker"), "reconciler": image("reconciler"), "migrate": image("migrate")}, "migration": map[string]any{"outcome": "complete"}, "rollouts": map[string]any{"outcome": "healthy"}, "deployments": []map[string]any{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}}, "rollback": map[string]any{"attempted": false, "succeeded": false}}
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

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
