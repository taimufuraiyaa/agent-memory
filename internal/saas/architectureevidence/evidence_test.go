package architectureevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

func TestCollectValidatesOpenedInventoryAndInput(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventoryPath := filepath.Join(repositoryRoot(t), "docs", "saas", "self-managed-platform-inventory.example.json")
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	_, input := validEvidence(now, platforminventory.Staging)
	input.Environment, input.InventoryID, input.InventoryReceiptSHA256 = string(inventory.Environment), inventory.InventoryID, inventory.ReceiptSHA256
	input.Checks[0].EvidenceSHA256 = inventory.ReceiptSHA256
	states := map[string]bool{}
	for _, integration := range inventory.ExternalIntegrations {
		states[string(integration.Kind)] = integration.Enabled
	}
	for index := range input.Integrations {
		input.Integrations[index].Enabled = states[input.Integrations[index].Kind]
		input.Integrations[index].Disposition = DispositionDisabled
		if input.Integrations[index].Enabled {
			input.Integrations[index].Disposition = DispositionApprovedContract
		}
	}
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(inventoryPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.InventoryReceiptSHA256 != inventory.ReceiptSHA256 {
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
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, Receipt{}); err == nil {
		t.Fatal("symlink destination accepted")
	}
}

func TestDecodeRejectsUnknownFieldsAndSymlink(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema":"agent-memory-self-managed-architecture-review-input-v1","unknown":true}`), 0o600); err != nil {
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

func TestBuildNormalizesReadyArchitectureReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, input := validEvidence(now, platforminventory.Staging)
	receipt, err := build(inventory, input, digest("f"), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.ComponentCount != 8 || receipt.ComponentDomainReviewCount != 48 || receipt.DataFlowCount != 8 || receipt.IntegrationCount != 3 || receipt.CheckCount != 10 || receipt.PassedCount != 10 || receipt.IndependentFailureDomainCount != 1 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestBuildPreservesCompleteAdverseReviewAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, input := validEvidence(now, platforminventory.Staging)
	input.Components[0].Domains[2].Outcome = OutcomeFailed
	input.Components[0].Outcome = OutcomeFailed
	input.Checks[4].Outcome = OutcomeFailed
	input.Ready = false
	receipt, err := build(inventory, input, digest("f"), now)
	if err != nil || receipt.Ready || receipt.FailedComponentCount != 1 || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildRequiresTwoIndependentProductionDomains(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	inventory, input := validEvidence(now, platforminventory.Production)
	input.IndependentFailureDomainCount = 1
	if _, err := build(inventory, input, digest("f"), now); err == nil {
		t.Fatal("single production domain accepted")
	}
}

func TestBuildRejectsIncompleteSubstitutedOrContradictoryReview(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Input){
		"missing component":       func(v *Input) { v.Components = v.Components[:7] },
		"duplicate component":     func(v *Input) { v.Components[7].Kind = v.Components[0].Kind },
		"missing domain":          func(v *Input) { v.Components[0].Domains = v.Components[0].Domains[:5] },
		"component contradiction": func(v *Input) { v.Components[0].Outcome = OutcomeFailed },
		"failure-domain mismatch": func(v *Input) { v.ReviewedFailureDomainCount++ },
		"missing data flow":       func(v *Input) { v.DataFlows = v.DataFlows[:7] },
		"integration state":       func(v *Input) { v.Integrations[0].Enabled = !v.Integrations[0].Enabled },
		"enabled disabled-decision": func(v *Input) {
			v.Integrations[0].Disposition = DispositionDisabled
		},
		"check contradiction":     func(v *Input) { v.Checks[7].Outcome = OutcomeFailed },
		"review substitution":     func(v *Input) { v.Checks[9].EvidenceSHA256 = digest("1") },
		"readiness contradiction": func(v *Input) { v.Ready = false },
		"local classification":    func(v *Input) { v.Classification = "local_development" },
	} {
		t.Run(name, func(t *testing.T) {
			inventory, input := validEvidence(now, platforminventory.Staging)
			mutate(&input)
			if _, err := build(inventory, input, digest("f"), now); err == nil {
				t.Fatal("invalid architecture evidence accepted")
			}
		})
	}
}

func validEvidence(now time.Time, environment platforminventory.Environment) (platforminventory.Inventory, Input) {
	domainIDs := []string{"domain-a"}
	if environment == platforminventory.Production {
		domainIDs = []string{"domain-a", "domain-b"}
	}
	failureDomains := make([]platforminventory.FailureDomain, 0, len(domainIDs))
	for _, id := range domainIDs {
		failureDomains = append(failureDomains, platforminventory.FailureDomain{ID: id})
	}
	componentKinds := []platforminventory.ComponentKind{platforminventory.ComponentKubernetes, platforminventory.ComponentIdentity, platforminventory.ComponentPostgres, platforminventory.ComponentObjectStorage, platforminventory.ComponentQueue, platforminventory.ComponentSecrets, platforminventory.ComponentObservability, platforminventory.ComponentBackup}
	components := make([]platforminventory.Component, 0, len(componentKinds))
	for _, kind := range componentKinds {
		components = append(components, platforminventory.Component{Kind: kind, FailureDomainIDs: append([]string(nil), domainIDs...)})
	}
	integrations := []platforminventory.ExternalIntegration{{Kind: platforminventory.IntegrationPayment, Enabled: true}, {Kind: platforminventory.IntegrationEmail, Enabled: false}, {Kind: platforminventory.IntegrationModel, Enabled: true}}
	inventory := platforminventory.Inventory{Schema: platforminventory.SchemaV1, Environment: environment, InventoryID: "platform-inventory", GeneratedAt: now.Add(-2 * time.Hour), FailureDomains: failureDomains, Components: components, ExternalIntegrations: integrations, ReceiptSHA256: digest("a")}
	componentReviews := make([]ComponentReview, 0, len(components))
	for _, component := range components {
		domains := make([]DomainReview, 0, len(RequiredComponentDomains()))
		for _, id := range RequiredComponentDomains() {
			domains = append(domains, DomainReview{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("1")})
		}
		componentReviews = append(componentReviews, ComponentReview{Kind: string(component.Kind), DeclaredFailureDomainCount: len(component.FailureDomainIDs), ServiceADRSHA256: digest("2"), Outcome: OutcomePassed, Domains: domains})
	}
	flows := make([]DataFlowReview, 0, len(RequiredDataFlows()))
	for _, id := range RequiredDataFlows() {
		flows = append(flows, DataFlowReview{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("3")})
	}
	integrationReviews := make([]IntegrationReview, 0, len(integrations))
	for _, integration := range integrations {
		disposition := DispositionDisabled
		if integration.Enabled {
			disposition = DispositionApprovedContract
		}
		integrationReviews = append(integrationReviews, IntegrationReview{Kind: string(integration.Kind), Enabled: integration.Enabled, Disposition: disposition, Outcome: OutcomePassed, EvidenceSHA256: digest("4")})
	}
	checks := make([]Check, 0, len(RequiredChecks()))
	for _, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest("5")})
	}
	checks[0].EvidenceSHA256 = digest("a")
	checks[1].EvidenceSHA256 = digest("b")
	checks[2].EvidenceSHA256 = digest("c")
	checks[3].EvidenceSHA256 = digest("d")
	checks[4].EvidenceSHA256 = digest("c")
	checks[5].EvidenceSHA256 = digest("e")
	checks[6].EvidenceSHA256 = digest("c")
	checks[7].EvidenceSHA256 = digest("6")
	checks[8].EvidenceSHA256 = digest("7")
	checks[9].EvidenceSHA256 = digest("8")
	input := Input{Schema: InputSchemaV1, Classification: "self_managed_external", Environment: string(environment), ReviewID: "architecture-review-1", ReviewVersion: "architecture-v1", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, TopologyADRManifestSHA256: digest("c"), ServiceADRManifestSHA256: digest("b"), FacilityInventorySHA256: digest("d"), FailureDomainReviewSHA256: digest("e"), DataFlowManifestSHA256: digest("6"), IntegrationContractManifestSHA256: digest("7"), AccountableReviewSHA256: digest("8"), ReviewedAt: now.Add(-time.Hour), GeneratedAt: now.Add(-30 * time.Minute), Ready: true, FacilityCount: len(domainIDs), ReviewedFailureDomainCount: len(domainIDs), IndependentFailureDomainCount: len(domainIDs), Components: componentReviews, DataFlows: flows, Integrations: integrationReviews, Checks: checks}
	return inventory, input
}

func digest(value string) string { return strings.Repeat(value, 64) }

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
