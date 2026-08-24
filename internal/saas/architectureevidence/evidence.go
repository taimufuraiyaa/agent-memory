// Package architectureevidence normalizes content-free P0.2-A architecture
// review evidence without exposing private topology or facility details.
package architectureevidence

import (
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"

	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	InputSchemaV1     = "agent-memory-self-managed-architecture-review-input-v1"
	ReceiptSchemaV1   = "agent-memory-self-managed-architecture-review-receipt-v1"
	maximumAge        = 24 * time.Hour
	maximumInputBytes = 256 << 10
)

type ComponentDomainID string
type DataFlowID string
type Disposition string
type CheckID string
type Outcome string

const (
	DomainOwnership        ComponentDomainID = "ownership"
	DomainCustody          ComponentDomainID = "custody"
	DomainCapacity         ComponentDomainID = "capacity"
	DomainFailureIsolation ComponentDomainID = "failure_isolation"
	DomainCost             ComponentDomainID = "cost"
	DomainIncidentResponse ComponentDomainID = "incident_response"

	FlowEdgeIdentity        DataFlowID = "edge_identity"
	FlowSourceIngestion     DataFlowID = "source_ingestion"
	FlowAuthoritativeStore  DataFlowID = "authoritative_storage"
	FlowAsyncProcessing     DataFlowID = "asynchronous_processing"
	FlowRetrievalModel      DataFlowID = "retrieval_model_routing"
	FlowAuditExport         DataFlowID = "audit_export"
	FlowDeletionBackup      DataFlowID = "deletion_backup"
	FlowExternalIntegration DataFlowID = "external_integrations"

	DispositionApprovedContract Disposition = "approved_contract"
	DispositionDisabled         Disposition = "disabled_decision"

	CheckInventoryBinding     CheckID = "inventory_binding_verified"
	CheckComponentCoverage    CheckID = "component_review_coverage_complete"
	CheckOwnership            CheckID = "ownership_review_passed"
	CheckCustody              CheckID = "custody_review_passed"
	CheckCapacityCost         CheckID = "capacity_cost_review_passed"
	CheckFailureIsolation     CheckID = "failure_isolation_review_passed"
	CheckIncidentResponse     CheckID = "incident_response_review_passed"
	CheckDataFlows            CheckID = "data_flow_review_passed"
	CheckIntegrationContracts CheckID = "integration_contract_review_passed"
	CheckAccountableReview    CheckID = "architecture_security_privacy_operations_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern            = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	requiredComponentDomains = []ComponentDomainID{DomainOwnership, DomainCustody, DomainCapacity, DomainFailureIsolation, DomainCost, DomainIncidentResponse}
	requiredDataFlows        = []DataFlowID{FlowEdgeIdentity, FlowSourceIngestion, FlowAuthoritativeStore, FlowAsyncProcessing, FlowRetrievalModel, FlowAuditExport, FlowDeletionBackup, FlowExternalIntegration}
	requiredChecks           = []CheckID{CheckInventoryBinding, CheckComponentCoverage, CheckOwnership, CheckCustody, CheckCapacityCost, CheckFailureIsolation, CheckIncidentResponse, CheckDataFlows, CheckIntegrationContracts, CheckAccountableReview}
)

type DomainReview struct {
	ID             ComponentDomainID `json:"id"`
	Outcome        Outcome           `json:"outcome"`
	EvidenceSHA256 string            `json:"evidence_sha256"`
}

type ComponentReview struct {
	Kind                       string         `json:"kind"`
	DeclaredFailureDomainCount int            `json:"declared_failure_domain_count"`
	ServiceADRSHA256           string         `json:"service_adr_sha256"`
	Outcome                    Outcome        `json:"outcome"`
	Domains                    []DomainReview `json:"domains"`
}

type DataFlowReview struct {
	ID             DataFlowID `json:"id"`
	Outcome        Outcome    `json:"outcome"`
	EvidenceSHA256 string     `json:"evidence_sha256"`
}

type IntegrationReview struct {
	Kind           string      `json:"kind"`
	Enabled        bool        `json:"enabled"`
	Disposition    Disposition `json:"disposition"`
	Outcome        Outcome     `json:"outcome"`
	EvidenceSHA256 string      `json:"evidence_sha256"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                            string              `json:"schema"`
	Classification                    string              `json:"classification"`
	Environment                       string              `json:"environment"`
	ReviewID                          string              `json:"review_id"`
	ReviewVersion                     string              `json:"review_version"`
	InventoryID                       string              `json:"inventory_id"`
	InventoryReceiptSHA256            string              `json:"inventory_receipt_sha256"`
	TopologyADRManifestSHA256         string              `json:"topology_adr_manifest_sha256"`
	ServiceADRManifestSHA256          string              `json:"service_adr_manifest_sha256"`
	FacilityInventorySHA256           string              `json:"facility_inventory_sha256"`
	FailureDomainReviewSHA256         string              `json:"failure_domain_review_sha256"`
	DataFlowManifestSHA256            string              `json:"data_flow_manifest_sha256"`
	IntegrationContractManifestSHA256 string              `json:"integration_contract_manifest_sha256"`
	AccountableReviewSHA256           string              `json:"accountable_review_sha256"`
	ReviewedAt                        time.Time           `json:"reviewed_at"`
	GeneratedAt                       time.Time           `json:"generated_at"`
	Ready                             bool                `json:"ready"`
	FacilityCount                     int                 `json:"facility_count"`
	ReviewedFailureDomainCount        int                 `json:"reviewed_failure_domain_count"`
	IndependentFailureDomainCount     int                 `json:"independent_failure_domain_count"`
	Components                        []ComponentReview   `json:"components"`
	DataFlows                         []DataFlowReview    `json:"data_flows"`
	Integrations                      []IntegrationReview `json:"integrations"`
	Checks                            []Check             `json:"checks"`
}

type Receipt struct {
	Input
	Schema                       string    `json:"schema"`
	InputSHA256                  string    `json:"input_sha256"`
	CollectedAt                  time.Time `json:"collected_at"`
	ComponentCount               int       `json:"component_count"`
	ComponentDomainReviewCount   int       `json:"component_domain_review_count"`
	PassedComponentCount         int       `json:"passed_component_count"`
	FailedComponentCount         int       `json:"failed_component_count"`
	InconclusiveComponentCount   int       `json:"inconclusive_component_count"`
	DataFlowCount                int       `json:"data_flow_count"`
	PassedDataFlowCount          int       `json:"passed_data_flow_count"`
	FailedDataFlowCount          int       `json:"failed_data_flow_count"`
	InconclusiveDataFlowCount    int       `json:"inconclusive_data_flow_count"`
	IntegrationCount             int       `json:"integration_count"`
	PassedIntegrationCount       int       `json:"passed_integration_count"`
	FailedIntegrationCount       int       `json:"failed_integration_count"`
	InconclusiveIntegrationCount int       `json:"inconclusive_integration_count"`
	CheckCount                   int       `json:"check_count"`
	PassedCount                  int       `json:"passed_count"`
	FailedCount                  int       `json:"failed_count"`
	InconclusiveCount            int       `json:"inconclusive_count"`
}

func RequiredComponentDomains() []ComponentDomainID {
	return append([]ComponentDomainID(nil), requiredComponentDomains...)
}
func RequiredDataFlows() []DataFlowID { return append([]DataFlowID(nil), requiredDataFlows...) }
func RequiredChecks() []CheckID       { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load architecture inventory: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, input, inputDigest, now)
}

type outcomeCounts struct{ passed, failed, inconclusive int }

func build(inventory platforminventory.Inventory, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || (inventory.Environment != platforminventory.Staging && inventory.Environment != platforminventory.Production) || input.Schema != InputSchemaV1 || input.Classification != "self_managed_external" || input.Environment != string(inventory.Environment) || !allOpaque(input.ReviewID, input.ReviewVersion, input.InventoryID) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !allDigests(input.InventoryReceiptSHA256, input.TopologyADRManifestSHA256, input.ServiceADRManifestSHA256, input.FacilityInventorySHA256, input.FailureDomainReviewSHA256, input.DataFlowManifestSHA256, input.IntegrationContractManifestSHA256, input.AccountableReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("architecture review identity or inventory binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("architecture review collection time is invalid")
	}
	now = now.UTC()
	reviewed, generated := input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if reviewed.IsZero() || generated.IsZero() || reviewed.Before(inventory.GeneratedAt.UTC()) || generated.Before(reviewed) || generated.After(now) || generated.Before(now.Add(-maximumAge)) {
		return Receipt{}, errors.New("architecture review timeline is invalid")
	}
	minimumIndependent := 1
	if inventory.Environment == platforminventory.Production {
		minimumIndependent = 2
	}
	if input.FacilityCount < 1 || input.FacilityCount > 32 || input.ReviewedFailureDomainCount != len(inventory.FailureDomains) || input.IndependentFailureDomainCount < minimumIndependent || input.IndependentFailureDomainCount > input.ReviewedFailureDomainCount {
		return Receipt{}, errors.New("architecture facility or failure-domain aggregate is invalid")
	}
	components, componentCounts, domainOutcomes, err := validateComponents(input.Components, inventory.Components)
	if err != nil {
		return Receipt{}, err
	}
	flows, flowCounts, flowOutcome, err := validateDataFlows(input.DataFlows)
	if err != nil {
		return Receipt{}, err
	}
	integrations, integrationCounts, integrationOutcome, err := validateIntegrations(input.Integrations, inventory.ExternalIntegrations)
	if err != nil {
		return Receipt{}, err
	}
	checks, checkCounts, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	derived := map[CheckID]Outcome{
		CheckInventoryBinding:     OutcomePassed,
		CheckComponentCoverage:    OutcomePassed,
		CheckOwnership:            domainOutcomes[0],
		CheckCustody:              domainOutcomes[1],
		CheckCapacityCost:         aggregateOutcomes(domainOutcomes[2], domainOutcomes[4]),
		CheckFailureIsolation:     domainOutcomes[3],
		CheckIncidentResponse:     domainOutcomes[5],
		CheckDataFlows:            flowOutcome,
		CheckIntegrationContracts: integrationOutcome,
	}
	for id, expected := range derived {
		if outcomeFor(checks, id) != expected {
			return Receipt{}, errors.New("architecture check contradicts review evidence")
		}
	}
	bindings := map[CheckID]string{
		CheckInventoryBinding:     input.InventoryReceiptSHA256,
		CheckComponentCoverage:    input.ServiceADRManifestSHA256,
		CheckOwnership:            input.TopologyADRManifestSHA256,
		CheckCustody:              input.FacilityInventorySHA256,
		CheckCapacityCost:         input.TopologyADRManifestSHA256,
		CheckFailureIsolation:     input.FailureDomainReviewSHA256,
		CheckIncidentResponse:     input.TopologyADRManifestSHA256,
		CheckDataFlows:            input.DataFlowManifestSHA256,
		CheckIntegrationContracts: input.IntegrationContractManifestSHA256,
		CheckAccountableReview:    input.AccountableReviewSHA256,
	}
	for id, digest := range bindings {
		if evidenceFor(checks, id) != digest {
			return Receipt{}, errors.New("architecture check artifact binding is invalid")
		}
	}
	ready := componentCounts.passed == len(components) && flowCounts.passed == len(flows) && integrationCounts.passed == len(integrations) && checkCounts.passed == len(checks) && checkCounts.failed == 0 && checkCounts.inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("architecture readiness contradicts evidence")
	}
	result := Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, ComponentCount: len(components), ComponentDomainReviewCount: len(components) * len(requiredComponentDomains), PassedComponentCount: componentCounts.passed, FailedComponentCount: componentCounts.failed, InconclusiveComponentCount: componentCounts.inconclusive, DataFlowCount: len(flows), PassedDataFlowCount: flowCounts.passed, FailedDataFlowCount: flowCounts.failed, InconclusiveDataFlowCount: flowCounts.inconclusive, IntegrationCount: len(integrations), PassedIntegrationCount: integrationCounts.passed, FailedIntegrationCount: integrationCounts.failed, InconclusiveIntegrationCount: integrationCounts.inconclusive, CheckCount: len(checks), PassedCount: checkCounts.passed, FailedCount: checkCounts.failed, InconclusiveCount: checkCounts.inconclusive}
	result.ReviewedAt, result.GeneratedAt = reviewed, generated
	result.Components, result.DataFlows, result.Integrations, result.Checks = components, flows, integrations, checks
	return result, nil
}

func validateComponents(values []ComponentReview, inventory []platforminventory.Component) ([]ComponentReview, outcomeCounts, [6]Outcome, error) {
	if len(values) != len(inventory) || len(values) != 8 {
		return nil, outcomeCounts{}, [6]Outcome{}, errors.New("architecture component coverage is incomplete")
	}
	expected := make(map[string]int, len(inventory))
	for _, component := range inventory {
		expected[string(component.Kind)] = len(component.FailureDomainIDs)
	}
	seen := map[string]bool{}
	ordered := append([]ComponentReview(nil), values...)
	totals := outcomeCounts{}
	domainTotals := [6]outcomeCounts{}
	for index := range ordered {
		value := &ordered[index]
		domainCount, exists := expected[value.Kind]
		if !exists || seen[value.Kind] || value.DeclaredFailureDomainCount != domainCount || !digestPattern.MatchString(value.ServiceADRSHA256) {
			return nil, outcomeCounts{}, [6]Outcome{}, errors.New("architecture component binding is invalid")
		}
		seen[value.Kind] = true
		domains, counts, err := validateDomainReviews(value.Domains)
		if err != nil {
			return nil, outcomeCounts{}, [6]Outcome{}, err
		}
		value.Domains = domains
		derived := outcomeFromCounts(counts)
		if value.Outcome != derived {
			return nil, outcomeCounts{}, [6]Outcome{}, errors.New("architecture component outcome contradicts domains")
		}
		addOutcome(&totals, derived)
		for domainIndex, domain := range domains {
			addOutcome(&domainTotals[domainIndex], domain.Outcome)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Kind < ordered[j].Kind })
	var outcomes [6]Outcome
	for index := range outcomes {
		outcomes[index] = outcomeFromCounts(domainTotals[index])
	}
	return ordered, totals, outcomes, nil
}

func validateDomainReviews(values []DomainReview) ([]DomainReview, outcomeCounts, error) {
	if len(values) != len(requiredComponentDomains) {
		return nil, outcomeCounts{}, errors.New("architecture component domain coverage is incomplete")
	}
	by := map[ComponentDomainID]DomainReview{}
	totals := outcomeCounts{}
	for _, value := range values {
		if _, exists := by[value.ID]; exists || !validOutcome(value.Outcome) || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, outcomeCounts{}, errors.New("architecture component domain review is invalid")
		}
		by[value.ID] = value
	}
	ordered := make([]DomainReview, 0, len(values))
	for _, id := range requiredComponentDomains {
		value, exists := by[id]
		if !exists {
			return nil, outcomeCounts{}, errors.New("architecture component domain is missing")
		}
		ordered = append(ordered, value)
		addOutcome(&totals, value.Outcome)
	}
	return ordered, totals, nil
}

func validateDataFlows(values []DataFlowReview) ([]DataFlowReview, outcomeCounts, Outcome, error) {
	if len(values) != len(requiredDataFlows) {
		return nil, outcomeCounts{}, "", errors.New("architecture data-flow coverage is incomplete")
	}
	by := map[DataFlowID]DataFlowReview{}
	totals := outcomeCounts{}
	for _, value := range values {
		if _, exists := by[value.ID]; exists || !validOutcome(value.Outcome) || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, outcomeCounts{}, "", errors.New("architecture data-flow review is invalid")
		}
		by[value.ID] = value
	}
	ordered := make([]DataFlowReview, 0, len(values))
	for _, id := range requiredDataFlows {
		value, exists := by[id]
		if !exists {
			return nil, outcomeCounts{}, "", errors.New("architecture data flow is missing")
		}
		ordered = append(ordered, value)
		addOutcome(&totals, value.Outcome)
	}
	return ordered, totals, outcomeFromCounts(totals), nil
}

func validateIntegrations(values []IntegrationReview, inventory []platforminventory.ExternalIntegration) ([]IntegrationReview, outcomeCounts, Outcome, error) {
	if len(values) != len(inventory) || len(values) != 3 {
		return nil, outcomeCounts{}, "", errors.New("architecture integration coverage is incomplete")
	}
	expected := map[string]bool{}
	for _, integration := range inventory {
		expected[string(integration.Kind)] = integration.Enabled
	}
	seen := map[string]bool{}
	totals := outcomeCounts{}
	ordered := append([]IntegrationReview(nil), values...)
	for _, value := range ordered {
		enabled, exists := expected[value.Kind]
		expectedDisposition := DispositionDisabled
		if enabled {
			expectedDisposition = DispositionApprovedContract
		}
		if !exists || seen[value.Kind] || value.Enabled != enabled || value.Disposition != expectedDisposition || !validOutcome(value.Outcome) || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, outcomeCounts{}, "", errors.New("architecture integration review is invalid")
		}
		seen[value.Kind] = true
		addOutcome(&totals, value.Outcome)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Kind < ordered[j].Kind })
	return ordered, totals, outcomeFromCounts(totals), nil
}

func validateChecks(values []Check) ([]Check, outcomeCounts, error) {
	if len(values) != len(requiredChecks) {
		return nil, outcomeCounts{}, errors.New("architecture checks are incomplete")
	}
	by := map[CheckID]Check{}
	totals := outcomeCounts{}
	for _, value := range values {
		if _, exists := by[value.ID]; exists || !validOutcome(value.Outcome) || !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, outcomeCounts{}, errors.New("architecture check is invalid")
		}
		by[value.ID] = value
	}
	ordered := make([]Check, 0, len(values))
	for _, id := range requiredChecks {
		value, exists := by[id]
		if !exists {
			return nil, outcomeCounts{}, errors.New("architecture required check is missing")
		}
		ordered = append(ordered, value)
		addOutcome(&totals, value.Outcome)
	}
	return ordered, totals, nil
}

func decodeStrictRegular(path string, target any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("architecture evidence path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("architecture evidence must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open architecture evidence")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("architecture evidence changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read architecture evidence")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return "", errors.New("architecture evidence JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("architecture evidence contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("architecture evidence changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("architecture evidence changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("architecture receipt path is required")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-architecture-review-*")
}

func validOutcome(value Outcome) bool {
	return value == OutcomePassed || value == OutcomeFailed || value == OutcomeInconclusive
}
func addOutcome(counts *outcomeCounts, value Outcome) {
	if value == OutcomePassed {
		counts.passed++
	} else if value == OutcomeFailed {
		counts.failed++
	} else {
		counts.inconclusive++
	}
}
func outcomeFromCounts(counts outcomeCounts) Outcome {
	if counts.failed > 0 {
		return OutcomeFailed
	}
	if counts.inconclusive > 0 {
		return OutcomeInconclusive
	}
	return OutcomePassed
}
func aggregateOutcomes(values ...Outcome) Outcome {
	counts := outcomeCounts{}
	for _, value := range values {
		addOutcome(&counts, value)
	}
	return outcomeFromCounts(counts)
}
func allOpaque(values ...string) bool {
	for _, value := range values {
		if !opaquePattern.MatchString(value) {
			return false
		}
	}
	return true
}
func allDigests(values ...string) bool {
	for _, value := range values {
		if !digestPattern.MatchString(value) {
			return false
		}
	}
	return true
}
func outcomeFor(values []Check, id CheckID) Outcome {
	for _, value := range values {
		if value.ID == id {
			return value.Outcome
		}
	}
	return ""
}
func evidenceFor(values []Check, id CheckID) string {
	for _, value := range values {
		if value.ID == id {
			return value.EvidenceSHA256
		}
	}
	return ""
}
