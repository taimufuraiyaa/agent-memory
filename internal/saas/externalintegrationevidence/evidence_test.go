package externalintegrationevidence

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

func TestCollectBindsInventoryAndNormalizesDisabledIntegrations(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, nil)
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	input := readyInput(now, inventory, nil)
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(inventoryPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.EnabledCount != 0 || receipt.DisabledCount != 3 || receipt.IntegrationCount != 3 || receipt.PassedIntegrationCount != 3 || receipt.CheckCount != 7 || receipt.PassedCount != 7 || receipt.InputSHA256 != digestFile(t, inputPath) {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestCollectAcceptsEnabledIntegrationWithPositiveCleanTraffic(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	enabled := map[IntegrationKind]bool{IntegrationPayment: true}
	inventoryPath := writeInventory(t, now, enabled)
	inventory, _ := platforminventory.Load(inventoryPath)
	input := readyInput(now, inventory, enabled)
	receipt, err := Collect(inventoryPath, writeJSON(t, "enabled.json", input), now)
	if err != nil || !receipt.Ready || receipt.EnabledCount != 1 || receipt.SampledRequestCount != 10 || receipt.ApprovedDataFieldCount != 4 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectPreservesEnabledUnsampledIntegrationAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	enabled := map[IntegrationKind]bool{IntegrationModel: true}
	inventoryPath := writeInventory(t, now, enabled)
	inventory, _ := platforminventory.Load(inventoryPath)
	input := readyInput(now, inventory, enabled)
	input.Integrations[2].SampledRequestCount = 0
	input.Integrations[2].Outcome = OutcomeInconclusive
	input.Checks[4].Outcome = OutcomeInconclusive
	input.Checks[5].Outcome = OutcomeInconclusive
	input.Ready = false
	receipt, err := Collect(inventoryPath, writeJSON(t, "unsampled.json", input), now)
	if err != nil || receipt.Ready || receipt.InconclusiveIntegrationCount != 1 || receipt.InconclusiveCount != 2 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectPreservesProhibitedTrafficAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	enabled := map[IntegrationKind]bool{IntegrationEmail: true}
	inventoryPath := writeInventory(t, now, enabled)
	inventory, _ := platforminventory.Load(inventoryPath)
	input := readyInput(now, inventory, enabled)
	input.Integrations[1].CustomerContentByteCount = 9
	input.Integrations[1].Outcome = OutcomeFailed
	input.Checks[5].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := Collect(inventoryPath, writeJSON(t, "failed.json", input), now)
	if err != nil || receipt.Ready || receipt.CustomerContentByteCount != 9 || receipt.FailedIntegrationCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectRejectsUnsafeMismatchedAndContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, nil)
	inventory, _ := platforminventory.Load(inventoryPath)
	base := readyInput(now, inventory, nil)
	for name, mutate := range map[string]func(*Input){
		"classification":          func(value *Input) { value.Classification = "local_development" },
		"inventory digest":        func(value *Input) { value.InventoryReceiptSHA256 = digest(99) },
		"enabled substitution":    func(value *Input) { value.Integrations[0].Enabled = true },
		"disabled traffic":        func(value *Input) { value.Integrations[0].SampledRequestCount = 1 },
		"bad digest":              func(value *Input) { value.DataPolicySHA256 = "bad" },
		"missing integration":     func(value *Input) { value.Integrations = value.Integrations[:2] },
		"duplicate integration":   func(value *Input) { value.Integrations[2].Kind = value.Integrations[0].Kind },
		"missing check":           func(value *Input) { value.Checks = value.Checks[:6] },
		"duplicate check":         func(value *Input) { value.Checks[6].ID = value.Checks[0].ID },
		"pre-inventory review":    func(value *Input) { value.ReviewedAt = inventory.GeneratedAt.Add(-time.Minute) },
		"stale":                   func(value *Input) { value.GeneratedAt = now.Add(-25 * time.Hour) },
		"readiness contradiction": func(value *Input) { value.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			input := cloneInput(base)
			mutate(&input)
			if _, err := Collect(inventoryPath, writeJSON(t, name+".json", input), now); err == nil {
				t.Fatal("unsafe external-integration evidence accepted")
			}
		})
	}
	inputPath := writeJSON(t, "safe.json", base)
	linkPath := filepath.Join(t.TempDir(), "input-link.json")
	if err := os.Symlink(inputPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(inventoryPath, linkPath, now); err == nil {
		t.Fatal("symlink input accepted")
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
		t.Fatal("receipt overwrite accepted")
	}
}

func TestLoadReadyRevalidatesReceiptAgainstInventoryAndReturnsExactDigest(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, nil)
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := Collect(inventoryPath, writeJSON(t, "input.json", readyInput(now, inventory, nil)), now)
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "receipt.json", receipt)
	loaded, receiptDigest, err := LoadReady(path, inventory)
	if err != nil || !loaded.Ready || receiptDigest != digestFile(t, path) {
		t.Fatalf("loaded=%+v digest=%q err=%v", loaded, receiptDigest, err)
	}
}

func TestLoadReadyRejectsUnreadyTamperedInventoryMismatchedAndSymlinkReceipts(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := writeInventory(t, now, nil)
	inventory, _ := platforminventory.Load(inventoryPath)
	receipt, err := Collect(inventoryPath, writeJSON(t, "input.json", readyInput(now, inventory, nil)), now)
	if err != nil {
		t.Fatal(err)
	}

	unready := receipt
	unready.Ready = false
	if _, _, err := LoadReady(writeJSON(t, "unready.json", unready), inventory); err == nil {
		t.Fatal("unready receipt accepted")
	}
	tampered := receipt
	tampered.PassedCount--
	if _, _, err := LoadReady(writeJSON(t, "tampered.json", tampered), inventory); err == nil {
		t.Fatal("tampered receipt accepted")
	}
	otherInventory := inventory
	otherInventory.InventoryID = "inventory-other"
	if _, _, err := LoadReady(writeJSON(t, "mismatch.json", receipt), otherInventory); err == nil {
		t.Fatal("inventory-mismatched receipt accepted")
	}
	path := writeJSON(t, "safe-receipt.json", receipt)
	link := filepath.Join(t.TempDir(), "receipt-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReady(link, inventory); err == nil {
		t.Fatal("symlink receipt accepted")
	}
}

func readyInput(now time.Time, inventory platforminventory.Inventory, enabled map[IntegrationKind]bool) Input {
	integrations := make([]IntegrationReview, 0, len(RequiredIntegrations()))
	for index, kind := range RequiredIntegrations() {
		review := IntegrationReview{Kind: kind, Enabled: enabled[kind], ConfigurationVersion: fmt.Sprintf("config-v%d", index+1), PurposeVersion: fmt.Sprintf("purpose-v%d", index+1),
			ConfigurationSHA256: digest(index + 10), PurposeDecisionSHA256: digest(index + 20), ContractOrDisabledStateSHA256: digest(index + 30), RetentionTrainingSettingsSHA256: digest(index + 40), TrafficExportSHA256: digest(index + 50), ExitPlanSHA256: digest(index + 60), Outcome: OutcomePassed}
		if review.Enabled {
			review.ApprovedDataFieldCount = 4
			review.SampledRequestCount = 10
		}
		integrations = append(integrations, review)
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for index, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(index + 70)})
	}
	return Input{Schema: InputSchemaV1, Classification: "self_managed_external", Environment: string(inventory.Environment), ReviewID: "external-integration-review", PolicyVersion: "policy-v1", TrafficReviewVersion: "traffic-v1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, DataPolicySHA256: digest(1), IntegrationManifestSHA256: digest(2), ReviewDecisionSHA256: digest(3), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Integrations: integrations, Checks: checks}
}

func cloneInput(value Input) Input {
	value.Integrations = append([]IntegrationReview(nil), value.Integrations...)
	value.Checks = append([]Check(nil), value.Checks...)
	return value
}

func writeInventory(t *testing.T, now time.Time, enabled map[IntegrationKind]bool) string {
	t.Helper()
	components := []string{}
	for _, kind := range []string{"kubernetes", "identity", "postgres", "object_storage", "queue", "secrets", "observability", "backup"} {
		components = append(components, fmt.Sprintf(`{"kind":%q,"owner_group":"platform_operations","version":"v1","replicas":1,"failure_domain_ids":["fd-a"],"public_ingress":false}`, kind))
	}
	integrations := []string{}
	for _, kind := range []IntegrationKind{IntegrationPayment, IntegrationEmail, IntegrationModel} {
		integrations = append(integrations, fmt.Sprintf(`{"kind":%q,"enabled":%t,"owner_group":"privacy_security"}`, kind, enabled[kind]))
	}
	contents := fmt.Sprintf(`{"schema":"agent-memory-self-managed-platform-inventory-v1","environment":"staging","inventory_id":"inventory-1","generated_at":%q,"administrative_domain_id":"admin-a","site_id":"site-a","failure_domains":[{"id":"fd-a"}],"components":[%s],"external_integrations":[%s]}`, now.Add(-3*time.Hour).Format(time.RFC3339), strings.Join(components, ","), strings.Join(integrations, ","))
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
