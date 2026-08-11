// Package programapprovalevidence normalizes content-free CP0-A/CP0-B
// architecture, blocker, economics, staffing, and accountable-review evidence.
// It does not approve either external checkpoint.
package programapprovalevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/externalintegrationevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchscopeevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	InputSchemaV1     = "agent-memory-checkpoint-zero-program-input-v1"
	ReceiptSchemaV1   = "agent-memory-checkpoint-zero-program-receipt-v1"
	maximumInputBytes = 256 << 10
	maximumAge        = 24 * time.Hour
	maximumCount      = 1_000_000_000
)

type Outcome string
type BlockerCategoryID string
type StaffingDomainID string
type CheckID string

const (
	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"

	BlockerOwnership           BlockerCategoryID = "infrastructure_ownership"
	BlockerTopology            BlockerCategoryID = "topology"
	BlockerExternalIntegration BlockerCategoryID = "external_integration"
	BlockerJurisdiction        BlockerCategoryID = "jurisdiction"

	StaffingOnCall  StaffingDomainID = "on_call"
	StaffingSupport StaffingDomainID = "support"
	StaffingNotice  StaffingDomainID = "notice"

	CheckLaunchScopeReady           CheckID = "launch_scope_ready"
	CheckTopologyInventoryValid     CheckID = "topology_inventory_valid"
	CheckIntegrationReviewReady     CheckID = "integration_review_ready"
	CheckDecisionRegisterReviewed   CheckID = "decision_register_reviewed"
	CheckBlockersReconciled         CheckID = "blockers_reconciled"
	CheckComponentRecoveryReviewed  CheckID = "component_recovery_exit_reviewed"
	CheckInfrastructureWithinCap    CheckID = "infrastructure_forecast_within_cap"
	CheckBetaEconomicsWithinCap     CheckID = "beta_economics_within_cap"
	CheckStaffingCoverageComplete   CheckID = "staffing_coverage_complete"
	CheckAccountableReviewsComplete CheckID = "accountable_cp0_reviews_complete"
)

var (
	digestPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	requiredBlockers = []BlockerCategoryID{BlockerOwnership, BlockerTopology, BlockerExternalIntegration, BlockerJurisdiction}
	requiredStaffing = []StaffingDomainID{StaffingOnCall, StaffingSupport, StaffingNotice}
	requiredChecks   = []CheckID{
		CheckLaunchScopeReady, CheckTopologyInventoryValid, CheckIntegrationReviewReady,
		CheckDecisionRegisterReviewed, CheckBlockersReconciled, CheckComponentRecoveryReviewed,
		CheckInfrastructureWithinCap, CheckBetaEconomicsWithinCap, CheckStaffingCoverageComplete,
		CheckAccountableReviewsComplete,
	}
)

type BlockerCategory struct {
	ID             BlockerCategoryID `json:"id"`
	TotalCount     int               `json:"total_count"`
	ResolvedCount  int               `json:"resolved_count"`
	DeferredCount  int               `json:"deferred_count"`
	OpenCount      int               `json:"open_count"`
	UnownedCount   int               `json:"unowned_count"`
	Outcome        Outcome           `json:"outcome"`
	EvidenceSHA256 string            `json:"evidence_sha256"`
}

type StaffingDomain struct {
	ID                      StaffingDomainID `json:"id"`
	RequiredCoverageMinutes int              `json:"required_coverage_minutes"`
	PrimaryCoveredMinutes   int              `json:"primary_covered_minutes"`
	BackupCoveredMinutes    int              `json:"backup_covered_minutes"`
	PrimarySlotCount        int              `json:"primary_slot_count"`
	BackupSlotCount         int              `json:"backup_slot_count"`
	Outcome                 Outcome          `json:"outcome"`
	EvidenceSHA256          string           `json:"evidence_sha256"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                                    string            `json:"schema"`
	Classification                            string            `json:"classification"`
	Environment                               string            `json:"environment"`
	ReviewID                                  string            `json:"review_id"`
	DecisionRegisterVersion                   string            `json:"decision_register_version"`
	TopologyVersion                           string            `json:"topology_version"`
	RecoveryPlanVersion                       string            `json:"recovery_plan_version"`
	ForecastVersion                           string            `json:"forecast_version"`
	BetaCapVersion                            string            `json:"beta_cap_version"`
	StaffingPlanVersion                       string            `json:"staffing_plan_version"`
	InventoryID                               string            `json:"inventory_id"`
	InventoryReceiptSHA256                    string            `json:"inventory_receipt_sha256"`
	LaunchScopeDecisionID                     string            `json:"launch_scope_decision_id"`
	LaunchScopeReceiptSHA256                  string            `json:"launch_scope_receipt_sha256"`
	IntegrationReviewID                       string            `json:"integration_review_id"`
	IntegrationReceiptSHA256                  string            `json:"integration_receipt_sha256"`
	DecisionRegisterSHA256                    string            `json:"decision_register_sha256"`
	TopologyReviewSHA256                      string            `json:"topology_review_sha256"`
	FacilityReviewSHA256                      string            `json:"facility_review_sha256"`
	RecoveryExitReviewSHA256                  string            `json:"recovery_exit_review_sha256"`
	IntegrationBoundarySHA256                 string            `json:"integration_boundary_sha256"`
	JurisdictionDecisionSHA256                string            `json:"jurisdiction_decision_sha256"`
	BlockerRegisterSHA256                     string            `json:"blocker_register_sha256"`
	CostForecastSHA256                        string            `json:"cost_forecast_sha256"`
	InfrastructureCostCapDecisionSHA256       string            `json:"infrastructure_cost_cap_decision_sha256"`
	BetaCapDecisionSHA256                     string            `json:"beta_cap_decision_sha256"`
	StaffingPlanSHA256                        string            `json:"staffing_plan_sha256"`
	CP0AReviewSHA256                          string            `json:"cp0a_review_sha256"`
	CP0BReviewSHA256                          string            `json:"cp0b_review_sha256"`
	ReviewedAt                                time.Time         `json:"reviewed_at"`
	GeneratedAt                               time.Time         `json:"generated_at"`
	ForecastMonthlyCostMicroUSD               int64             `json:"forecast_monthly_cost_micro_usd"`
	ApprovedInfrastructureMonthlyCapMicroUSD  int64             `json:"approved_infrastructure_monthly_cap_micro_usd"`
	BetaAccountCap                            int               `json:"beta_account_cap"`
	EstimatedWorstCaseBetaMonthlyCostMicroUSD int64             `json:"estimated_worst_case_beta_monthly_cost_micro_usd"`
	ApprovedWorstCaseBetaMonthlyCapMicroUSD   int64             `json:"approved_worst_case_beta_monthly_cap_micro_usd"`
	Ready                                     bool              `json:"ready"`
	Blockers                                  []BlockerCategory `json:"blockers"`
	Staffing                                  []StaffingDomain  `json:"staffing"`
	Checks                                    []Check           `json:"checks"`
}

type Receipt struct {
	Schema                                    string            `json:"schema"`
	Classification                            string            `json:"classification"`
	Environment                               string            `json:"environment"`
	ReviewID                                  string            `json:"review_id"`
	DecisionRegisterVersion                   string            `json:"decision_register_version"`
	TopologyVersion                           string            `json:"topology_version"`
	RecoveryPlanVersion                       string            `json:"recovery_plan_version"`
	ForecastVersion                           string            `json:"forecast_version"`
	BetaCapVersion                            string            `json:"beta_cap_version"`
	StaffingPlanVersion                       string            `json:"staffing_plan_version"`
	InventoryID                               string            `json:"inventory_id"`
	InventoryReceiptSHA256                    string            `json:"inventory_receipt_sha256"`
	LaunchScopeDecisionID                     string            `json:"launch_scope_decision_id"`
	LaunchScopeReceiptSHA256                  string            `json:"launch_scope_receipt_sha256"`
	IntegrationReviewID                       string            `json:"integration_review_id"`
	IntegrationReceiptSHA256                  string            `json:"integration_receipt_sha256"`
	InputSHA256                               string            `json:"input_sha256"`
	ReviewedAt                                time.Time         `json:"reviewed_at"`
	GeneratedAt                               time.Time         `json:"generated_at"`
	CollectedAt                               time.Time         `json:"collected_at"`
	ForecastMonthlyCostMicroUSD               int64             `json:"forecast_monthly_cost_micro_usd"`
	ApprovedInfrastructureMonthlyCapMicroUSD  int64             `json:"approved_infrastructure_monthly_cap_micro_usd"`
	BetaAccountCap                            int               `json:"beta_account_cap"`
	EstimatedWorstCaseBetaMonthlyCostMicroUSD int64             `json:"estimated_worst_case_beta_monthly_cost_micro_usd"`
	ApprovedWorstCaseBetaMonthlyCapMicroUSD   int64             `json:"approved_worst_case_beta_monthly_cap_micro_usd"`
	Ready                                     bool              `json:"ready"`
	BlockerCategoryCount                      int               `json:"blocker_category_count"`
	TotalBlockerCount                         int               `json:"total_blocker_count"`
	ResolvedBlockerCount                      int               `json:"resolved_blocker_count"`
	DeferredBlockerCount                      int               `json:"deferred_blocker_count"`
	OpenBlockerCount                          int               `json:"open_blocker_count"`
	UnownedBlockerCount                       int               `json:"unowned_blocker_count"`
	StaffingDomainCount                       int               `json:"staffing_domain_count"`
	CoveredStaffingDomainCount                int               `json:"covered_staffing_domain_count"`
	CheckCount                                int               `json:"check_count"`
	PassedCount                               int               `json:"passed_count"`
	FailedCount                               int               `json:"failed_count"`
	InconclusiveCount                         int               `json:"inconclusive_count"`
	EvidenceDigests                           map[string]string `json:"evidence_digests"`
	Blockers                                  []BlockerCategory `json:"blockers"`
	Staffing                                  []StaffingDomain  `json:"staffing"`
	Checks                                    []Check           `json:"checks"`
}

func RequiredBlockerCategories() []BlockerCategoryID {
	return append([]BlockerCategoryID(nil), requiredBlockers...)
}
func RequiredStaffingDomains() []StaffingDomainID {
	return append([]StaffingDomainID(nil), requiredStaffing...)
}
func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, launchScopeReceiptPath, integrationReceiptPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform inventory: %w", err)
	}
	scope, scopeDigest, err := launchscopeevidence.LoadReady(launchScopeReceiptPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready launch-scope receipt: %w", err)
	}
	integration, integrationDigest, err := externalintegrationevidence.LoadReady(integrationReceiptPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready integration receipt: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, scope, scopeDigest, integration, integrationDigest, input, inputDigest, now)
}

type blockerSummary struct{ total, resolved, deferred, open, unowned int }

func build(inventory platforminventory.Inventory, scope launchscopeevidence.Receipt, scopeDigest string, integration externalintegrationevidence.Receipt, integrationDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if input.Schema != InputSchemaV1 || input.Classification != "external_business" || input.Environment != string(inventory.Environment) ||
		!allOpaque(input.ReviewID, input.DecisionRegisterVersion, input.TopologyVersion, input.RecoveryPlanVersion, input.ForecastVersion, input.BetaCapVersion, input.StaffingPlanVersion) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.LaunchScopeDecisionID != scope.ScopeDecisionID || input.LaunchScopeReceiptSHA256 != scopeDigest ||
		input.IntegrationReviewID != integration.ReviewID || input.IntegrationReceiptSHA256 != integrationDigest || integration.InventoryID != inventory.InventoryID ||
		!allDigests(inputDigest, input.InventoryReceiptSHA256, input.LaunchScopeReceiptSHA256, input.IntegrationReceiptSHA256) {
		return Receipt{}, errors.New("checkpoint-zero identity or prerequisite binding is invalid")
	}
	digests := map[string]string{
		"decision_register": input.DecisionRegisterSHA256, "topology_review": input.TopologyReviewSHA256,
		"facility_review": input.FacilityReviewSHA256, "recovery_exit_review": input.RecoveryExitReviewSHA256,
		"integration_boundary": input.IntegrationBoundarySHA256, "jurisdiction_decision": input.JurisdictionDecisionSHA256,
		"blocker_register": input.BlockerRegisterSHA256, "cost_forecast": input.CostForecastSHA256,
		"infrastructure_cost_cap_decision": input.InfrastructureCostCapDecisionSHA256, "beta_cap_decision": input.BetaCapDecisionSHA256,
		"staffing_plan": input.StaffingPlanSHA256, "cp0a_review": input.CP0AReviewSHA256, "cp0b_review": input.CP0BReviewSHA256,
	}
	for _, digest := range digests {
		if !digestPattern.MatchString(digest) {
			return Receipt{}, errors.New("checkpoint-zero evidence digest is invalid")
		}
	}
	if now.IsZero() {
		return Receipt{}, errors.New("checkpoint-zero collection time is invalid")
	}
	now, reviewed, generated := now.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	latestPrerequisite := scope.CollectedAt.UTC()
	if integration.CollectedAt.After(latestPrerequisite) {
		latestPrerequisite = integration.CollectedAt.UTC()
	}
	if inventory.GeneratedAt.After(latestPrerequisite) {
		latestPrerequisite = inventory.GeneratedAt.UTC()
	}
	if reviewed.IsZero() || generated.IsZero() || reviewed.Before(latestPrerequisite) || generated.Before(reviewed) || generated.After(now) || generated.Before(now.Add(-maximumAge)) {
		return Receipt{}, errors.New("checkpoint-zero evidence timeline is invalid")
	}
	if input.ForecastMonthlyCostMicroUSD < 0 || input.ApprovedInfrastructureMonthlyCapMicroUSD < 0 || input.EstimatedWorstCaseBetaMonthlyCostMicroUSD < 0 || input.ApprovedWorstCaseBetaMonthlyCapMicroUSD < 0 || input.BetaAccountCap < 1 || input.BetaAccountCap > maximumCount {
		return Receipt{}, errors.New("checkpoint-zero economics aggregate is invalid")
	}
	blockers, blockerTotals, blockersPassed, err := validateBlockers(input.Blockers)
	if err != nil {
		return Receipt{}, err
	}
	staffing, coveredStaffing, staffingPassed, err := validateStaffing(input.Staffing)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	expected := map[CheckID]Outcome{
		CheckLaunchScopeReady: OutcomePassed, CheckTopologyInventoryValid: OutcomePassed, CheckIntegrationReviewReady: OutcomePassed,
		CheckBlockersReconciled: outcome(blockersPassed), CheckInfrastructureWithinCap: outcome(input.ForecastMonthlyCostMicroUSD <= input.ApprovedInfrastructureMonthlyCapMicroUSD),
		CheckBetaEconomicsWithinCap: outcome(input.EstimatedWorstCaseBetaMonthlyCostMicroUSD <= input.ApprovedWorstCaseBetaMonthlyCapMicroUSD), CheckStaffingCoverageComplete: outcome(staffingPassed),
	}
	for id, want := range expected {
		if outcomeFor(checks, id) != want {
			return Receipt{}, errors.New("checkpoint-zero check contradicts derived evidence")
		}
	}
	ready := blockersPassed && staffingPassed && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("checkpoint-zero readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, ReviewID: input.ReviewID,
		DecisionRegisterVersion: input.DecisionRegisterVersion, TopologyVersion: input.TopologyVersion, RecoveryPlanVersion: input.RecoveryPlanVersion, ForecastVersion: input.ForecastVersion, BetaCapVersion: input.BetaCapVersion, StaffingPlanVersion: input.StaffingPlanVersion,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, LaunchScopeDecisionID: input.LaunchScopeDecisionID, LaunchScopeReceiptSHA256: scopeDigest, IntegrationReviewID: input.IntegrationReviewID, IntegrationReceiptSHA256: integrationDigest, InputSHA256: inputDigest,
		ReviewedAt: reviewed, GeneratedAt: generated, CollectedAt: now, ForecastMonthlyCostMicroUSD: input.ForecastMonthlyCostMicroUSD, ApprovedInfrastructureMonthlyCapMicroUSD: input.ApprovedInfrastructureMonthlyCapMicroUSD,
		BetaAccountCap: input.BetaAccountCap, EstimatedWorstCaseBetaMonthlyCostMicroUSD: input.EstimatedWorstCaseBetaMonthlyCostMicroUSD, ApprovedWorstCaseBetaMonthlyCapMicroUSD: input.ApprovedWorstCaseBetaMonthlyCapMicroUSD,
		Ready: ready, BlockerCategoryCount: len(blockers), TotalBlockerCount: blockerTotals.total, ResolvedBlockerCount: blockerTotals.resolved, DeferredBlockerCount: blockerTotals.deferred, OpenBlockerCount: blockerTotals.open, UnownedBlockerCount: blockerTotals.unowned,
		StaffingDomainCount: len(staffing), CoveredStaffingDomainCount: coveredStaffing, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive,
		EvidenceDigests: digests, Blockers: blockers, Staffing: staffing, Checks: checks}, nil
}

func validateBlockers(values []BlockerCategory) ([]BlockerCategory, blockerSummary, bool, error) {
	if len(values) != len(requiredBlockers) {
		return nil, blockerSummary{}, false, errors.New("checkpoint-zero blocker categories are incomplete")
	}
	byID := map[BlockerCategoryID]BlockerCategory{}
	totals := blockerSummary{}
	allPassed := true
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, totals, false, errors.New("checkpoint-zero blocker category is duplicated")
		}
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, totals, false, errors.New("checkpoint-zero blocker digest is invalid")
		}
		for _, count := range []int{value.TotalCount, value.ResolvedCount, value.DeferredCount, value.OpenCount, value.UnownedCount} {
			if count < 0 || count > maximumCount {
				return nil, totals, false, errors.New("checkpoint-zero blocker aggregate is invalid")
			}
		}
		if value.TotalCount != value.ResolvedCount+value.DeferredCount+value.OpenCount || value.UnownedCount > value.TotalCount {
			return nil, totals, false, errors.New("checkpoint-zero blocker reconciliation is invalid")
		}
		derived := outcome(value.OpenCount == 0 && value.UnownedCount == 0)
		if value.Outcome != derived {
			return nil, totals, false, errors.New("checkpoint-zero blocker outcome contradicts counts")
		}
		allPassed = allPassed && derived == OutcomePassed
		totals.total += value.TotalCount
		totals.resolved += value.ResolvedCount
		totals.deferred += value.DeferredCount
		totals.open += value.OpenCount
		totals.unowned += value.UnownedCount
		byID[value.ID] = value
	}
	ordered := make([]BlockerCategory, 0, len(requiredBlockers))
	for _, id := range requiredBlockers {
		value, ok := byID[id]
		if !ok {
			return nil, totals, false, errors.New("required checkpoint-zero blocker category is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, totals, allPassed, nil
}

func validateStaffing(values []StaffingDomain) ([]StaffingDomain, int, bool, error) {
	if len(values) != len(requiredStaffing) {
		return nil, 0, false, errors.New("checkpoint-zero staffing domains are incomplete")
	}
	byID := map[StaffingDomainID]StaffingDomain{}
	covered := 0
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, false, errors.New("checkpoint-zero staffing domain is duplicated")
		}
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, false, errors.New("checkpoint-zero staffing digest is invalid")
		}
		for _, count := range []int{value.RequiredCoverageMinutes, value.PrimaryCoveredMinutes, value.BackupCoveredMinutes, value.PrimarySlotCount, value.BackupSlotCount} {
			if count < 0 || count > maximumCount {
				return nil, 0, false, errors.New("checkpoint-zero staffing aggregate is invalid")
			}
		}
		ok := value.RequiredCoverageMinutes > 0 && value.PrimaryCoveredMinutes >= value.RequiredCoverageMinutes && value.BackupCoveredMinutes >= value.RequiredCoverageMinutes && value.PrimarySlotCount > 0 && value.BackupSlotCount > 0
		if value.Outcome != outcome(ok) {
			return nil, 0, false, errors.New("checkpoint-zero staffing outcome contradicts coverage")
		}
		if ok {
			covered++
		}
		byID[value.ID] = value
	}
	ordered := make([]StaffingDomain, 0, len(requiredStaffing))
	for _, id := range requiredStaffing {
		value, ok := byID[id]
		if !ok {
			return nil, 0, false, errors.New("required checkpoint-zero staffing domain is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, covered, covered == len(requiredStaffing), nil
}

func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("checkpoint-zero checks are incomplete")
	}
	byID := map[CheckID]Check{}
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("checkpoint-zero check is duplicated")
		}
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("checkpoint-zero check digest is invalid")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("checkpoint-zero check outcome is invalid")
		}
		byID[value.ID] = value
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		value, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("required checkpoint-zero check is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, passed, failed, inconclusive, nil
}

func outcome(value bool) Outcome {
	if value {
		return OutcomePassed
	}
	return OutcomeFailed
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
		return "", errors.New("checkpoint-zero input path is required")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximumInputBytes {
		return "", errors.New("checkpoint-zero input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open checkpoint-zero input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		return "", errors.New("checkpoint-zero input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read checkpoint-zero input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("checkpoint-zero input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("checkpoint-zero input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("checkpoint-zero input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("checkpoint-zero input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("checkpoint-zero receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("checkpoint-zero receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect checkpoint-zero receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-checkpoint-zero-*")
}
