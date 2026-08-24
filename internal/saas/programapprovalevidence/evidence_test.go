package programapprovalevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/externalintegrationevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchscopeevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

func TestStrictInputReaderRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema":"agent-memory-checkpoint-zero-program-input-v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var input Input
	if _, err := decodeStrictRegular(unknown, &input); err == nil {
		t.Fatal("unknown field accepted")
	}
	safe := filepath.Join(t.TempDir(), "safe.json")
	if err := os.WriteFile(safe, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "input-link.json")
	if err := os.Symlink(safe, link); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStrictRegular(link, &input); err == nil {
		t.Fatal("symlink input accepted")
	}
}

func TestReceiptSchemaCoversExactContentFreePublishedShape(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, scope, integration, input := readyEvidence(now)
	receipt, err := build(inventory, scope, digest(90), integration, digest(91), input, digest(92), now)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range [][]byte{[]byte(`"name"`), []byte(`"email"`), []byte(`"path"`), []byte(`"content"`), []byte(`"endpoint"`), []byte(`"credential"`)} {
		if bytes.Contains(contents, prohibited) {
			t.Fatalf("receipt contains prohibited field %s", prohibited)
		}
	}
	var object map[string]any
	if err := json.Unmarshal(contents, &object); err != nil {
		t.Fatal(err)
	}
	schemaContents, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "evidence", "v1", "checkpoint-zero-program-receipt.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(schemaContents, &schema); err != nil {
		t.Fatal(err)
	}
	if len(object) != len(schema.Required) || len(schema.Properties) != len(schema.Required) {
		t.Fatalf("receipt=%d required=%d properties=%d", len(object), len(schema.Required), len(schema.Properties))
	}
	for _, key := range schema.Required {
		if _, ok := object[key]; !ok {
			t.Fatalf("receipt missing schema-required property %q", key)
		}
	}
	assertSchemaCoversObject(t, input, "checkpoint-zero-program-input.schema.json")
}

func assertSchemaCoversObject(t *testing.T, value any, schemaName string) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(contents, &object); err != nil {
		t.Fatal(err)
	}
	schemaContents, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "evidence", "v1", schemaName))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(schemaContents, &schema); err != nil {
		t.Fatal(err)
	}
	if len(object) != len(schema.Required) || len(schema.Properties) != len(schema.Required) {
		t.Fatalf("%s object=%d required=%d properties=%d", schemaName, len(object), len(schema.Required), len(schema.Properties))
	}
	for _, key := range schema.Required {
		if _, ok := object[key]; !ok {
			t.Fatalf("%s missing property %q", schemaName, key)
		}
	}
}

func TestCollectRevalidatesAndBindsFullPrerequisiteChain(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "inventory-1", GeneratedAt: now.Add(-6 * time.Hour), AdministrativeDomainID: "admin-a", SiteID: "site-a", FailureDomains: []platforminventory.FailureDomain{{ID: "fd-a"}}}
	for _, kind := range []platforminventory.ComponentKind{platforminventory.ComponentKubernetes, platforminventory.ComponentIdentity, platforminventory.ComponentPostgres, platforminventory.ComponentObjectStorage, platforminventory.ComponentQueue, platforminventory.ComponentSecrets, platforminventory.ComponentObservability, platforminventory.ComponentBackup} {
		inventory.Components = append(inventory.Components, platforminventory.Component{Kind: kind, OwnerGroup: "platform_operations", Version: "v1", Replicas: 1, FailureDomainIDs: []string{"fd-a"}})
	}
	for _, kind := range []platforminventory.IntegrationKind{platforminventory.IntegrationPayment, platforminventory.IntegrationEmail, platforminventory.IntegrationModel} {
		inventory.ExternalIntegrations = append(inventory.ExternalIntegrations, platforminventory.ExternalIntegration{Kind: kind, OwnerGroup: "privacy_security"})
	}
	inventoryPath := writeJSON(t, "inventory.json", inventory)
	loadedInventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}

	scopeInput := launchscopeevidence.Input{Schema: launchscopeevidence.InputSchemaV1, Classification: "external_business", Environment: "external", ScopeDecisionID: "scope-1", ScopeDecisionVersion: "scope-v1", JurisdictionPolicyVersion: "jurisdiction-v1", LegalReviewVersion: "legal-v1", RiskRegisterVersion: "risk-v1", DecisionRegisterSHA256: digest(1), LaunchScopeDecisionSHA256: digest(2), JurisdictionMemoSHA256: digest(3), PolicyManifestSHA256: digest(4), LegalReviewSHA256: digest(5), RiskRegisterSHA256: digest(6), ScopeApprovedAt: now.Add(-6 * time.Hour), LegalReviewCompletedAt: now.Add(-6 * time.Hour), GeneratedAt: now.Add(-5 * time.Hour), LaunchCountryCount: 1, MinimumAgeYears: 18, SupportLanguageCount: 1, NoticeJurisdictionCount: 1, Ready: true}
	for index, id := range launchscopeevidence.RequiredLegalPositions() {
		scopeInput.LegalPositions = append(scopeInput.LegalPositions, launchscopeevidence.LegalPosition{ID: id, PolicyCopySHA256: digest(100 + index), ReviewEvidenceSHA256: digest(110 + index), Outcome: launchscopeevidence.OutcomePassed})
	}
	for index, id := range launchscopeevidence.RequiredChecks() {
		scopeInput.Checks = append(scopeInput.Checks, launchscopeevidence.Check{ID: id, Outcome: launchscopeevidence.OutcomePassed, EvidenceSHA256: digest(120 + index)})
	}
	scopeReceipt, err := launchscopeevidence.Collect(writeJSON(t, "scope-input.json", scopeInput), now.Add(-4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	scopePath := filepath.Join(t.TempDir(), "scope-receipt.json")
	if err := launchscopeevidence.Publish(scopePath, scopeReceipt); err != nil {
		t.Fatal(err)
	}

	integrationInput := externalintegrationevidence.Input{Schema: externalintegrationevidence.InputSchemaV1, Classification: "self_managed_external", Environment: "staging", ReviewID: "integration-1", PolicyVersion: "policy-v1", TrafficReviewVersion: "traffic-v1", InventoryID: loadedInventory.InventoryID, InventoryReceiptSHA256: loadedInventory.ReceiptSHA256, DataPolicySHA256: digest(130), IntegrationManifestSHA256: digest(131), ReviewDecisionSHA256: digest(132), ReviewedAt: now.Add(-4 * time.Hour), GeneratedAt: now.Add(-3 * time.Hour), Ready: true}
	for index, kind := range externalintegrationevidence.RequiredIntegrations() {
		integrationInput.Integrations = append(integrationInput.Integrations, externalintegrationevidence.IntegrationReview{Kind: kind, ConfigurationVersion: "config-v1", PurposeVersion: "purpose-v1", ConfigurationSHA256: digest(140 + index), PurposeDecisionSHA256: digest(150 + index), ContractOrDisabledStateSHA256: digest(160 + index), RetentionTrainingSettingsSHA256: digest(170 + index), TrafficExportSHA256: digest(180 + index), ExitPlanSHA256: digest(190 + index), Outcome: externalintegrationevidence.OutcomePassed})
	}
	for index, id := range externalintegrationevidence.RequiredChecks() {
		integrationInput.Checks = append(integrationInput.Checks, externalintegrationevidence.Check{ID: id, Outcome: externalintegrationevidence.OutcomePassed, EvidenceSHA256: digest(200 + index)})
	}
	integrationReceipt, err := externalintegrationevidence.Collect(inventoryPath, writeJSON(t, "integration-input.json", integrationInput), now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	integrationPath := filepath.Join(t.TempDir(), "integration-receipt.json")
	if err := externalintegrationevidence.Publish(integrationPath, integrationReceipt); err != nil {
		t.Fatal(err)
	}

	_, _, _, input := readyEvidence(now)
	input.InventoryReceiptSHA256 = loadedInventory.ReceiptSHA256
	input.LaunchScopeReceiptSHA256 = fileDigest(t, scopePath)
	input.IntegrationReceiptSHA256 = fileDigest(t, integrationPath)
	receipt, err := Collect(inventoryPath, scopePath, integrationPath, writeJSON(t, "cp0-input.json", input), now)
	if err != nil || !receipt.Ready || receipt.LaunchScopeReceiptSHA256 != fileDigest(t, scopePath) || receipt.IntegrationReceiptSHA256 != fileDigest(t, integrationPath) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildNormalizesReadyCheckpointZeroReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, scope, integration, input := readyEvidence(now)
	receipt, err := build(inventory, scope, digest(90), integration, digest(91), input, digest(92), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.BlockerCategoryCount != 4 || receipt.StaffingDomainCount != 3 || receipt.CoveredStaffingDomainCount != 3 || receipt.CheckCount != 10 || receipt.PassedCount != 10 || receipt.OpenBlockerCount != 0 || receipt.DeferredBlockerCount != 4 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestBuildPreservesCompleteAdverseReviewAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, scope, integration, input := readyEvidence(now)
	input.Blockers[1].DeferredCount = 0
	input.Blockers[1].OpenCount = 1
	input.Blockers[1].Outcome = OutcomeFailed
	setCheck(&input, CheckBlockersReconciled, OutcomeFailed)
	input.Ready = false
	receipt, err := build(inventory, scope, digest(90), integration, digest(91), input, digest(92), now)
	if err != nil || receipt.Ready || receipt.OpenBlockerCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildRejectsIncompleteContradictoryAndUnsafeEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Input){
		"classification":          func(v *Input) { v.Classification = "local_development" },
		"inventory binding":       func(v *Input) { v.InventoryReceiptSHA256 = digest(99) },
		"scope binding":           func(v *Input) { v.LaunchScopeReceiptSHA256 = digest(99) },
		"digest":                  func(v *Input) { v.TopologyReviewSHA256 = "bad" },
		"stale":                   func(v *Input) { v.GeneratedAt = now.Add(-25 * time.Hour) },
		"pre-prerequisite review": func(v *Input) { v.ReviewedAt = now.Add(-3 * time.Hour) },
		"missing blocker":         func(v *Input) { v.Blockers = v.Blockers[:3] },
		"duplicate blocker":       func(v *Input) { v.Blockers[3].ID = v.Blockers[0].ID },
		"blocker arithmetic":      func(v *Input) { v.Blockers[0].TotalCount++ },
		"missing staffing":        func(v *Input) { v.Staffing = v.Staffing[:2] },
		"staffing contradiction":  func(v *Input) { v.Staffing[0].BackupCoveredMinutes = 0 },
		"missing check":           func(v *Input) { v.Checks = v.Checks[:9] },
		"economics contradiction": func(v *Input) { v.ForecastMonthlyCostMicroUSD = v.ApprovedInfrastructureMonthlyCapMicroUSD + 1 },
		"readiness contradiction": func(v *Input) { v.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, scope, integration, input := readyEvidence(now)
			mutate(&input)
			if _, err := build(inventory, scope, digest(90), integration, digest(91), input, digest(92), now); err == nil {
				t.Fatal("unsafe checkpoint-zero evidence accepted")
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
		t.Fatalf("mode=%v err=%v", info, err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("receipt overwrite accepted")
	}
}

func readyEvidence(now time.Time) (platforminventory.Inventory, launchscopeevidence.Receipt, externalintegrationevidence.Receipt, Input) {
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: platforminventory.Staging, InventoryID: "inventory-1", GeneratedAt: now.Add(-4 * time.Hour), ReceiptSHA256: digest(80)}
	scope := launchscopeevidence.Receipt{Ready: true, ScopeDecisionID: "scope-1", CollectedAt: now.Add(-3 * time.Hour)}
	integration := externalintegrationevidence.Receipt{Ready: true, ReviewID: "integration-1", InventoryID: inventory.InventoryID, CollectedAt: now.Add(-2 * time.Hour)}
	blockers := make([]BlockerCategory, 0, 4)
	for index, id := range RequiredBlockerCategories() {
		blockers = append(blockers, BlockerCategory{ID: id, TotalCount: 1, DeferredCount: 1, Outcome: OutcomePassed, EvidenceSHA256: digest(index + 10)})
	}
	staffing := make([]StaffingDomain, 0, 3)
	for index, id := range RequiredStaffingDomains() {
		staffing = append(staffing, StaffingDomain{ID: id, RequiredCoverageMinutes: 60, PrimaryCoveredMinutes: 60, BackupCoveredMinutes: 60, PrimarySlotCount: 1, BackupSlotCount: 1, Outcome: OutcomePassed, EvidenceSHA256: digest(index + 20)})
	}
	checks := make([]Check, 0, 10)
	for index, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(index + 30)})
	}
	input := Input{Schema: InputSchemaV1, Classification: "external_business", Environment: "staging", ReviewID: "cp0-review-1",
		DecisionRegisterVersion: "decision-v1", TopologyVersion: "topology-v1", RecoveryPlanVersion: "recovery-v1", ForecastVersion: "forecast-v1", BetaCapVersion: "beta-v1", StaffingPlanVersion: "staffing-v1",
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, LaunchScopeDecisionID: scope.ScopeDecisionID, LaunchScopeReceiptSHA256: digest(90), IntegrationReviewID: integration.ReviewID, IntegrationReceiptSHA256: digest(91),
		DecisionRegisterSHA256: digest(1), TopologyReviewSHA256: digest(2), FacilityReviewSHA256: digest(3), RecoveryExitReviewSHA256: digest(4), IntegrationBoundarySHA256: digest(5), JurisdictionDecisionSHA256: digest(6), BlockerRegisterSHA256: digest(7), CostForecastSHA256: digest(8), InfrastructureCostCapDecisionSHA256: digest(9), BetaCapDecisionSHA256: digest(40), StaffingPlanSHA256: digest(41), CP0AReviewSHA256: digest(42), CP0BReviewSHA256: digest(43),
		ReviewedAt: now.Add(-time.Hour), GeneratedAt: now.Add(-30 * time.Minute), ForecastMonthlyCostMicroUSD: 900_000_000, ApprovedInfrastructureMonthlyCapMicroUSD: 1_000_000_000, BetaAccountCap: 100, EstimatedWorstCaseBetaMonthlyCostMicroUSD: 500_000_000, ApprovedWorstCaseBetaMonthlyCapMicroUSD: 600_000_000,
		Ready: true, Blockers: blockers, Staffing: staffing, Checks: checks}
	return inventory, scope, integration, input
}

func setCheck(input *Input, id CheckID, result Outcome) {
	for index := range input.Checks {
		if input.Checks[index].ID == id {
			input.Checks[index].Outcome = result
		}
	}
}

func digest(value int) string { return fmt.Sprintf("%064x", value) }

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

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}
