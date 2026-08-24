package recoveryexitevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

func TestCollectNormalizesExactInventoryAndFortyFourOperations(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, map[string]bool{"payment": true})
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	input := readyInput(now, inventory)
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(inventoryPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.SubjectCount != 11 || receipt.EnabledIntegrationCount != 1 || receipt.OperationCount != 44 || receipt.PassedOperationCount != 44 || receipt.CheckCount != 8 || receipt.PassedCount != 8 || receipt.InputSHA256 != fileDigest(t, inputPath) {
		t.Fatalf("receipt=%+v", receipt)
	}
	if receipt.Subjects[0].Class != ClassCoreComponent || receipt.Subjects[0].Kind != "backup" || receipt.Subjects[8].Class != ClassExternalIntegration {
		t.Fatalf("subjects not canonically sorted: %+v", receipt.Subjects)
	}
}

func TestCollectPreservesFailedExerciseAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, nil)
	inventory, _ := platforminventory.Load(inventoryPath)
	input := readyInput(now, inventory)
	input.Subjects[0].Failover.PassedCount = 0
	input.Subjects[0].Failover.FailedCount = 1
	input.Subjects[0].Failover.Outcome = OutcomeFailed
	input.Subjects[0].Outcome = OutcomeFailed
	input.Checks[3].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := Collect(inventoryPath, writeJSON(t, "failed.json", input), now)
	if err != nil || receipt.Ready || receipt.FailedSubjectCount != 1 || receipt.FailedOperationCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectRejectsIncompleteUnsafeAndContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, nil)
	inventory, _ := platforminventory.Load(inventoryPath)
	base := readyInput(now, inventory)
	for name, mutate := range map[string]func(*Input){
		"classification":          func(v *Input) { v.Classification = "local" },
		"inventory digest":        func(v *Input) { v.InventoryReceiptSHA256 = digest(99) },
		"enabled substitution":    func(v *Input) { v.Subjects[8].Enabled = true },
		"missing subject":         func(v *Input) { v.Subjects = v.Subjects[:10] },
		"duplicate subject":       func(v *Input) { v.Subjects[10].Kind = v.Subjects[9].Kind },
		"bad procedure digest":    func(v *Input) { v.Subjects[0].Restore.ProcedureSHA256 = "bad" },
		"attempt arithmetic":      func(v *Input) { v.Subjects[0].Export.PassedCount = 0 },
		"duration contradiction":  func(v *Input) { v.Subjects[0].Replacement.MaximumObservedSeconds = 301 },
		"operation contradiction": func(v *Input) { v.Subjects[0].Restore.Outcome = OutcomeFailed },
		"subject contradiction":   func(v *Input) { v.Subjects[0].Outcome = OutcomeFailed },
		"missing check":           func(v *Input) { v.Checks = v.Checks[:7] },
		"check contradiction":     func(v *Input) { v.Checks[2].Outcome = OutcomeFailed },
		"pre-inventory review":    func(v *Input) { v.ReviewedAt = inventory.GeneratedAt.Add(-time.Minute) },
		"stale":                   func(v *Input) { v.GeneratedAt = now.Add(-25 * time.Hour) },
		"readiness":               func(v *Input) { v.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			value := cloneInput(base)
			mutate(&value)
			if _, err := Collect(inventoryPath, writeJSON(t, name+".json", value), now); err == nil {
				t.Fatal("unsafe evidence accepted")
			}
		})
	}
	inputPath := writeJSON(t, "safe.json", base)
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(inputPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, link, now); err == nil {
		t.Fatal("symlink accepted")
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
		t.Fatal("overwrite accepted")
	}
}

func readyInput(now time.Time, inventory platforminventory.Inventory) Input {
	subjects := make([]SubjectReview, 0, 11)
	for i, component := range inventory.Components {
		subjects = append(subjects, subject(ClassCoreComponent, string(component.Kind), true, i))
	}
	for i, integration := range inventory.ExternalIntegrations {
		subjects = append(subjects, subject(ClassExternalIntegration, string(integration.Kind), integration.Enabled, i+8))
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for i, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(200 + i)})
	}
	return Input{Schema: InputSchemaV1, Classification: "self_managed_external", Environment: string(inventory.Environment), ReviewID: "recovery-review", ProcedureManifestVersion: "procedures-v1", ExerciseManifestVersion: "exercises-v1", TargetPolicyVersion: "targets-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, ProcedureManifestSHA256: digest(1), ExerciseManifestSHA256: digest(2), TargetPolicySHA256: digest(3), OperationsReviewSHA256: digest(4), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Subjects: subjects, Checks: checks}
}

func subject(class SubjectClass, kind string, enabled bool, seed int) SubjectReview {
	op := func(offset int) OperationReview {
		return OperationReview{ProcedureSHA256: digest(10 + seed*8 + offset), ExerciseSHA256: digest(11 + seed*8 + offset), AttemptCount: 1, PassedCount: 1, MaximumTargetSeconds: 300, MaximumObservedSeconds: 120, Outcome: OutcomePassed}
	}
	return SubjectReview{Class: class, Kind: kind, Enabled: enabled, ProcedureVersion: fmt.Sprintf("procedure-v%d", seed+1), Replacement: op(0), Failover: op(2), Export: op(4), Restore: op(6), Outcome: OutcomePassed}
}

func cloneInput(v Input) Input {
	v.Subjects = append([]SubjectReview(nil), v.Subjects...)
	v.Checks = append([]Check(nil), v.Checks...)
	return v
}
func digest(seed int) string { return fmt.Sprintf("%064x", seed+1) }
func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func writeInventory(t *testing.T, now time.Time, enabled map[string]bool) string {
	t.Helper()
	components := []string{}
	for _, kind := range []string{"kubernetes", "identity", "postgres", "object_storage", "queue", "secrets", "observability", "backup"} {
		components = append(components, fmt.Sprintf(`{"kind":%q,"owner_group":"platform_operations","version":"v1","replicas":1,"failure_domain_ids":["fd-a"],"public_ingress":false}`, kind))
	}
	integrations := []string{}
	for _, kind := range []string{"payment", "email", "model"} {
		integrations = append(integrations, fmt.Sprintf(`{"kind":%q,"enabled":%t,"owner_group":"platform_operations"}`, kind, enabled[kind]))
	}
	body := fmt.Sprintf(`{"schema":"agent-memory-self-managed-platform-inventory-v1","environment":"staging","inventory_id":"inventory-1","generated_at":%q,"administrative_domain_id":"admin-a","site_id":"site-a","failure_domains":[{"id":"fd-a"}],"components":[%s],"external_integrations":[%s]}`, now.Add(-3*time.Hour).Format(time.RFC3339), strings.Join(components, ","), strings.Join(integrations, ","))
	p := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
