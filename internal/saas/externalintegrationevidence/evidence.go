// Package externalintegrationevidence normalizes content-free P0.2-C data-
// purpose review evidence for explicitly enabled or disabled integrations.
package externalintegrationevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	InputSchemaV1        = "agent-memory-external-integration-review-input-v1"
	ReceiptSchemaV1      = "agent-memory-external-integration-review-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumCollectionAge = 24 * time.Hour
	maximumAggregate     = 1_000_000_000
)

type IntegrationKind string
type CheckID string
type Outcome string

const (
	IntegrationPayment IntegrationKind = "payment"
	IntegrationEmail   IntegrationKind = "email"
	IntegrationModel   IntegrationKind = "model"

	CheckInventoryBinding   CheckID = "inventory_binding_verified"
	CheckPurposeApproved    CheckID = "purpose_approved"
	CheckContractReviewed   CheckID = "contract_or_disabled_state_reviewed"
	CheckSettingsReviewed   CheckID = "retention_training_settings_reviewed"
	CheckTrafficAllowlisted CheckID = "traffic_allowlist_verified"
	CheckContentMinimized   CheckID = "content_minimization_verified"
	CheckAccountableReview  CheckID = "privacy_security_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	requiredIntegrations = []IntegrationKind{IntegrationPayment, IntegrationEmail, IntegrationModel}
	requiredChecks       = []CheckID{
		CheckInventoryBinding, CheckPurposeApproved, CheckContractReviewed,
		CheckSettingsReviewed, CheckTrafficAllowlisted, CheckContentMinimized,
		CheckAccountableReview,
	}
)

type IntegrationReview struct {
	Kind                            IntegrationKind `json:"kind"`
	Enabled                         bool            `json:"enabled"`
	ConfigurationVersion            string          `json:"configuration_version"`
	PurposeVersion                  string          `json:"purpose_version"`
	ConfigurationSHA256             string          `json:"configuration_sha256"`
	PurposeDecisionSHA256           string          `json:"purpose_decision_sha256"`
	ContractOrDisabledStateSHA256   string          `json:"contract_or_disabled_state_sha256"`
	RetentionTrainingSettingsSHA256 string          `json:"retention_training_settings_sha256"`
	TrafficExportSHA256             string          `json:"traffic_export_sha256"`
	ExitPlanSHA256                  string          `json:"exit_plan_sha256"`
	ApprovedDataFieldCount          int             `json:"approved_data_field_count"`
	SampledRequestCount             int             `json:"sampled_request_count"`
	CustomerContentByteCount        int             `json:"customer_content_byte_count"`
	UnapprovedFieldCount            int             `json:"unapproved_field_count"`
	UnallowlistedDestinationCount   int             `json:"unallowlisted_destination_count"`
	ProviderTrainingEnabled         bool            `json:"provider_training_enabled"`
	GeneralLoggingEnabled           bool            `json:"general_logging_enabled"`
	Outcome                         Outcome         `json:"outcome"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                    string              `json:"schema"`
	Classification            string              `json:"classification"`
	Environment               string              `json:"environment"`
	ReviewID                  string              `json:"review_id"`
	PolicyVersion             string              `json:"policy_version"`
	TrafficReviewVersion      string              `json:"traffic_review_version"`
	InventoryID               string              `json:"inventory_id"`
	InventoryReceiptSHA256    string              `json:"inventory_receipt_sha256"`
	DataPolicySHA256          string              `json:"data_policy_sha256"`
	IntegrationManifestSHA256 string              `json:"integration_manifest_sha256"`
	ReviewDecisionSHA256      string              `json:"review_decision_sha256"`
	ReviewedAt                time.Time           `json:"reviewed_at"`
	GeneratedAt               time.Time           `json:"generated_at"`
	Ready                     bool                `json:"ready"`
	Integrations              []IntegrationReview `json:"integrations"`
	Checks                    []Check             `json:"checks"`
}

type Receipt struct {
	Schema                        string              `json:"schema"`
	Classification                string              `json:"classification"`
	Environment                   string              `json:"environment"`
	ReviewID                      string              `json:"review_id"`
	PolicyVersion                 string              `json:"policy_version"`
	TrafficReviewVersion          string              `json:"traffic_review_version"`
	InventoryID                   string              `json:"inventory_id"`
	InventoryReceiptSHA256        string              `json:"inventory_receipt_sha256"`
	DataPolicySHA256              string              `json:"data_policy_sha256"`
	IntegrationManifestSHA256     string              `json:"integration_manifest_sha256"`
	ReviewDecisionSHA256          string              `json:"review_decision_sha256"`
	InputSHA256                   string              `json:"input_sha256"`
	ReviewedAt                    time.Time           `json:"reviewed_at"`
	GeneratedAt                   time.Time           `json:"generated_at"`
	CollectedAt                   time.Time           `json:"collected_at"`
	Ready                         bool                `json:"ready"`
	IntegrationCount              int                 `json:"integration_count"`
	EnabledCount                  int                 `json:"enabled_count"`
	DisabledCount                 int                 `json:"disabled_count"`
	PassedIntegrationCount        int                 `json:"passed_integration_count"`
	FailedIntegrationCount        int                 `json:"failed_integration_count"`
	InconclusiveIntegrationCount  int                 `json:"inconclusive_integration_count"`
	ApprovedDataFieldCount        int                 `json:"approved_data_field_count"`
	SampledRequestCount           int                 `json:"sampled_request_count"`
	CustomerContentByteCount      int                 `json:"customer_content_byte_count"`
	UnapprovedFieldCount          int                 `json:"unapproved_field_count"`
	UnallowlistedDestinationCount int                 `json:"unallowlisted_destination_count"`
	CheckCount                    int                 `json:"check_count"`
	PassedCount                   int                 `json:"passed_count"`
	FailedCount                   int                 `json:"failed_count"`
	InconclusiveCount             int                 `json:"inconclusive_count"`
	Integrations                  []IntegrationReview `json:"integrations"`
	Checks                        []Check             `json:"checks"`
}

func RequiredIntegrations() []IntegrationKind {
	return append([]IntegrationKind(nil), requiredIntegrations...)
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed platform inventory: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, input, inputDigest, now)
}

// LoadReady strictly reloads a published receipt, revalidates its inventory
// binding, re-derives every normalized field, and returns the SHA-256 of the
// exact receipt bytes. Downstream gates therefore cannot trust a ready flag in
// edited JSON.
func LoadReady(path string, inventory platforminventory.Inventory) (Receipt, string, error) {
	var receipt Receipt
	receiptDigest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	input := Input{
		Schema: InputSchemaV1, Classification: receipt.Classification, Environment: receipt.Environment,
		ReviewID: receipt.ReviewID, PolicyVersion: receipt.PolicyVersion, TrafficReviewVersion: receipt.TrafficReviewVersion,
		InventoryID: receipt.InventoryID, InventoryReceiptSHA256: receipt.InventoryReceiptSHA256,
		DataPolicySHA256: receipt.DataPolicySHA256, IntegrationManifestSHA256: receipt.IntegrationManifestSHA256,
		ReviewDecisionSHA256: receipt.ReviewDecisionSHA256, ReviewedAt: receipt.ReviewedAt,
		GeneratedAt: receipt.GeneratedAt, Ready: receipt.Ready, Integrations: receipt.Integrations, Checks: receipt.Checks,
	}
	rebuilt, err := build(inventory, input, receipt.InputSHA256, receipt.CollectedAt)
	if err != nil || !receipt.Ready || !reflect.DeepEqual(receipt, rebuilt) {
		return Receipt{}, "", errors.New("external-integration receipt is not a valid ready receipt")
	}
	return receipt, receiptDigest, nil
}

func build(inventory platforminventory.Inventory, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if input.Schema != InputSchemaV1 || input.Classification != "self_managed_external" || input.Environment != string(inventory.Environment) ||
		!allOpaque(input.ReviewID, input.PolicyVersion, input.TrafficReviewVersion) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 ||
		!allDigests(input.DataPolicySHA256, input.IntegrationManifestSHA256, input.ReviewDecisionSHA256, inputDigest) {
		return Receipt{}, errors.New("external-integration review identity or inventory binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("external-integration collection time is invalid")
	}
	now = now.UTC()
	reviewed, generated := input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if reviewed.IsZero() || generated.IsZero() || reviewed.Before(inventory.GeneratedAt.UTC()) || generated.Before(reviewed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("external-integration review timeline is invalid")
	}
	inventoryStates := make(map[IntegrationKind]bool, len(inventory.ExternalIntegrations))
	for _, value := range inventory.ExternalIntegrations {
		inventoryStates[IntegrationKind(value.Kind)] = value.Enabled
	}
	integrations, summary, err := validateIntegrations(input.Integrations, inventoryStates)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	if outcomeFor(checks, CheckSettingsReviewed) != settingsOutcome(integrations) ||
		outcomeFor(checks, CheckTrafficAllowlisted) != allowlistOutcome(integrations) ||
		outcomeFor(checks, CheckContentMinimized) != minimizationOutcome(integrations) {
		return Receipt{}, errors.New("external-integration check contradicts observations")
	}
	ready := summary.passed == len(requiredIntegrations) && summary.failed == 0 && summary.inconclusive == 0 && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("external-integration readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		ReviewID: input.ReviewID, PolicyVersion: input.PolicyVersion, TrafficReviewVersion: input.TrafficReviewVersion,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, DataPolicySHA256: input.DataPolicySHA256, IntegrationManifestSHA256: input.IntegrationManifestSHA256, ReviewDecisionSHA256: input.ReviewDecisionSHA256, InputSHA256: inputDigest,
		ReviewedAt: reviewed, GeneratedAt: generated, CollectedAt: now, Ready: ready,
		IntegrationCount: len(integrations), EnabledCount: summary.enabled, DisabledCount: len(integrations) - summary.enabled,
		PassedIntegrationCount: summary.passed, FailedIntegrationCount: summary.failed, InconclusiveIntegrationCount: summary.inconclusive,
		ApprovedDataFieldCount: summary.approvedFields, SampledRequestCount: summary.sampledRequests, CustomerContentByteCount: summary.customerContentBytes,
		UnapprovedFieldCount: summary.unapprovedFields, UnallowlistedDestinationCount: summary.unallowlistedDestinations,
		CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, Integrations: integrations, Checks: checks}, nil
}

type integrationSummary struct {
	enabled, passed, failed, inconclusive                 int
	approvedFields, sampledRequests, customerContentBytes int
	unapprovedFields, unallowlistedDestinations           int
}

func validateIntegrations(values []IntegrationReview, inventoryStates map[IntegrationKind]bool) ([]IntegrationReview, integrationSummary, error) {
	if len(values) != len(requiredIntegrations) {
		return nil, integrationSummary{}, errors.New("external-integration review set is incomplete")
	}
	byKind := make(map[IntegrationKind]IntegrationReview, len(values))
	summary := integrationSummary{}
	for _, value := range values {
		expectedEnabled, allowed := inventoryStates[value.Kind]
		if !allowed || value.Enabled != expectedEnabled || !allOpaque(value.ConfigurationVersion, value.PurposeVersion) ||
			!allDigests(value.ConfigurationSHA256, value.PurposeDecisionSHA256, value.ContractOrDisabledStateSHA256, value.RetentionTrainingSettingsSHA256, value.TrafficExportSHA256, value.ExitPlanSHA256) {
			return nil, integrationSummary{}, errors.New("external-integration review binding is invalid")
		}
		if _, duplicate := byKind[value.Kind]; duplicate {
			return nil, integrationSummary{}, errors.New("external-integration review is duplicated")
		}
		for _, count := range []int{value.ApprovedDataFieldCount, value.SampledRequestCount, value.CustomerContentByteCount, value.UnapprovedFieldCount, value.UnallowlistedDestinationCount} {
			if count < 0 || count > maximumAggregate {
				return nil, integrationSummary{}, errors.New("external-integration aggregate is invalid")
			}
		}
		derived := integrationOutcome(value)
		if value.Outcome != derived {
			return nil, integrationSummary{}, errors.New("external-integration outcome contradicts observations")
		}
		if value.Enabled {
			summary.enabled++
		}
		switch derived {
		case OutcomePassed:
			summary.passed++
		case OutcomeFailed:
			summary.failed++
		case OutcomeInconclusive:
			summary.inconclusive++
		}
		summary.approvedFields += value.ApprovedDataFieldCount
		summary.sampledRequests += value.SampledRequestCount
		summary.customerContentBytes += value.CustomerContentByteCount
		summary.unapprovedFields += value.UnapprovedFieldCount
		summary.unallowlistedDestinations += value.UnallowlistedDestinationCount
		byKind[value.Kind] = value
	}
	ordered := make([]IntegrationReview, 0, len(requiredIntegrations))
	for _, kind := range requiredIntegrations {
		value, exists := byKind[kind]
		if !exists {
			return nil, integrationSummary{}, errors.New("required external-integration review is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, summary, nil
}

func integrationOutcome(value IntegrationReview) Outcome {
	prohibited := value.CustomerContentByteCount > 0 || value.UnapprovedFieldCount > 0 || value.UnallowlistedDestinationCount > 0 || value.ProviderTrainingEnabled || value.GeneralLoggingEnabled
	if prohibited {
		return OutcomeFailed
	}
	if !value.Enabled {
		if value.ApprovedDataFieldCount > 0 || value.SampledRequestCount > 0 {
			return OutcomeFailed
		}
		return OutcomePassed
	}
	if value.ApprovedDataFieldCount == 0 || value.SampledRequestCount == 0 {
		return OutcomeInconclusive
	}
	return OutcomePassed
}

func settingsOutcome(values []IntegrationReview) Outcome {
	for _, value := range values {
		if value.ProviderTrainingEnabled || value.GeneralLoggingEnabled {
			return OutcomeFailed
		}
	}
	return OutcomePassed
}

func allowlistOutcome(values []IntegrationReview) Outcome {
	inconclusive := false
	for _, value := range values {
		if value.UnallowlistedDestinationCount > 0 {
			return OutcomeFailed
		}
		inconclusive = inconclusive || (value.Enabled && value.SampledRequestCount == 0)
	}
	if inconclusive {
		return OutcomeInconclusive
	}
	return OutcomePassed
}

func minimizationOutcome(values []IntegrationReview) Outcome {
	inconclusive := false
	for _, value := range values {
		if value.CustomerContentByteCount > 0 || value.UnapprovedFieldCount > 0 {
			return OutcomeFailed
		}
		inconclusive = inconclusive || (value.Enabled && (value.SampledRequestCount == 0 || value.ApprovedDataFieldCount == 0))
	}
	if inconclusive {
		return OutcomeInconclusive
	}
	return OutcomePassed
}

func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("external-integration checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(values))
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("external-integration check digest is invalid")
		}
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("external-integration check is duplicated")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("external-integration check outcome is invalid")
		}
		byID[value.ID] = value
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		value, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("required external-integration check is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, passed, failed, inconclusive, nil
}

func outcomeFor(values []Check, id CheckID) Outcome {
	for _, value := range values {
		if value.ID == id {
			return value.Outcome
		}
	}
	return ""
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

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("external-integration input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("external-integration input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open external-integration input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("external-integration input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read external-integration input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("external-integration input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("external-integration input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("external-integration input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("external-integration input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("external-integration receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("external-integration receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect external-integration receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-external-integration-*")
}
