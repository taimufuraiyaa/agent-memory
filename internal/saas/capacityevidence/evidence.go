// Package capacityevidence normalizes content-free installed-site private-beta
// capacity and worst-case unit-economics evidence for CP10-C.
package capacityevidence

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

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrievalload"
)

const (
	InputSchemaV1         = "agent-memory-staging-capacity-economics-input-v1"
	ReceiptSchemaV1       = "agent-memory-staging-capacity-economics-receipt-v1"
	maximumInputBytes     = 128 << 10
	maximumAssessmentSpan = 7 * 24 * time.Hour
	maximumCollectionAge  = 24 * time.Hour
	maximumCount          = 100_000_000
	maximumRate           = int64(1_000_000_000_000)
	maximumCostMicroUSD   = int64(1_000_000_000_000_000)
	maximumInt64          = int64(^uint64(0) >> 1)
)

type CheckID string
type Outcome string

const (
	CheckBetaCapApproval   CheckID = "beta_cap_approved"
	CheckRetrievalLoad     CheckID = "deployed_retrieval_load_ready"
	CheckInstalledCapacity CheckID = "installed_capacity_measured"
	CheckTenantHeadroom    CheckID = "tenant_concurrency_headroom_met"
	CheckRequestHeadroom   CheckID = "retrieval_throughput_headroom_met"
	CheckQuotaEnvelope     CheckID = "quota_envelope_reconciled"
	CheckEconomics         CheckID = "worst_case_economics_reconciled"
	CheckMonthlyCost       CheckID = "monthly_cost_ceiling_met"
	OutcomePassed          Outcome = "passed"
	OutcomeFailed          Outcome = "failed"
	OutcomeInconclusive    Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckBetaCapApproval, CheckRetrievalLoad, CheckInstalledCapacity, CheckTenantHeadroom, CheckRequestHeadroom, CheckQuotaEnvelope, CheckEconomics, CheckMonthlyCost}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                                string    `json:"schema"`
	Classification                        string    `json:"classification"`
	Environment                           string    `json:"environment"`
	AssessmentID                          string    `json:"assessment_id"`
	CapacityModelVersion                  string    `json:"capacity_model_version"`
	EntitlementVersion                    string    `json:"entitlement_version"`
	EconomicsVersion                      string    `json:"economics_version"`
	BetaCapVersion                        string    `json:"beta_cap_version"`
	InventoryID                           string    `json:"inventory_id"`
	InventoryReceiptSHA256                string    `json:"inventory_receipt_sha256"`
	PlanID                                string    `json:"plan_id"`
	PlanReceiptSHA256                     string    `json:"plan_receipt_sha256"`
	ChangeID                              string    `json:"change_id"`
	ChangeReceiptSHA256                   string    `json:"change_receipt_sha256"`
	ReleaseID                             string    `json:"release_id"`
	ReleaseReceiptSHA256                  string    `json:"release_receipt_sha256"`
	RetrievalLoadRunID                    string    `json:"retrieval_load_run_id"`
	RetrievalLoadReceiptSHA256            string    `json:"retrieval_load_receipt_sha256"`
	InstalledLaunchPolicySHA256           string    `json:"installed_launch_policy_sha256"`
	EntitlementSnapshotSHA256             string    `json:"entitlement_snapshot_sha256"`
	CapacityReportSHA256                  string    `json:"capacity_report_sha256"`
	EconomicsReportSHA256                 string    `json:"economics_report_sha256"`
	DecisionSHA256                        string    `json:"decision_sha256"`
	DecisionApprovedAt                    time.Time `json:"decision_approved_at"`
	AssessmentStartedAt                   time.Time `json:"assessment_started_at"`
	AssessmentCompletedAt                 time.Time `json:"assessment_completed_at"`
	GeneratedAt                           time.Time `json:"generated_at"`
	BetaAccountCap                        int       `json:"beta_account_cap"`
	PlannedPeakConcurrentTenants          int       `json:"planned_peak_concurrent_tenants"`
	SupportedConcurrentTenants            int       `json:"supported_concurrent_tenants"`
	PlannedPeakRetrievalRequestsPerMinute int64     `json:"planned_peak_retrieval_requests_per_minute"`
	SustainedRetrievalRequestsPerMinute   int64     `json:"sustained_retrieval_requests_per_minute"`
	FixedMonthlyCostMicroUSD              int64     `json:"fixed_monthly_cost_microusd"`
	VariableMonthlyCostPerTenantMicroUSD  int64     `json:"variable_monthly_cost_per_tenant_microusd"`
	EstimatedWorstCaseMonthlyCostMicroUSD int64     `json:"estimated_worst_case_monthly_cost_microusd"`
	ApprovedMonthlyCostCeilingMicroUSD    int64     `json:"approved_monthly_cost_ceiling_microusd"`
	Ready                                 bool      `json:"ready"`
	Checks                                []Check   `json:"checks"`
}

type Receipt struct {
	Input
	InputSHA256       string    `json:"input_sha256"`
	CollectedAt       time.Time `json:"collected_at"`
	CheckCount        int       `json:"check_count"`
	PassedCount       int       `json:"passed_count"`
	FailedCount       int       `json:"failed_count"`
	InconclusiveCount int       `json:"inconclusive_count"`
	MetricBreachCount int       `json:"metric_breach_count"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, planPath, changePath, releasePath, loadPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load infrastructure plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load infrastructure change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load passed release: %w", err)
	}
	load, loadDigest, err := retrievalload.LoadReadyReceipt(loadPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load retrieval evidence: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, load, loadDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, load retrievalload.Receipt, loadDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready {
		return Receipt{}, errors.New("capacity platform chain is invalid or unready")
	}
	if !validRelease(release, releaseDigest) {
		return Receipt{}, errors.New("capacity release is invalid or unready")
	}
	if !validLoad(load, loadDigest, inventory, plan, change, release, releaseDigest) {
		return Receipt{}, errors.New("capacity retrieval load binding is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !allOpaque(input.AssessmentID, input.CapacityModelVersion, input.EntitlementVersion, input.EconomicsVersion, input.BetaCapVersion) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || input.RetrievalLoadRunID != load.RunID || input.RetrievalLoadReceiptSHA256 != loadDigest || !allDigests(input.InstalledLaunchPolicySHA256, input.EntitlementSnapshotSHA256, input.CapacityReportSHA256, input.EconomicsReportSHA256, input.DecisionSHA256, inputDigest) {
		return Receipt{}, errors.New("capacity input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("capacity collection time is invalid")
	}
	now = now.UTC()
	approved, started, completed, generated := input.DecisionApprovedAt.UTC(), input.AssessmentStartedAt.UTC(), input.AssessmentCompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if load.CollectedAt.UTC().After(earliest) {
		earliest = load.CollectedAt.UTC()
	}
	if approved.IsZero() || started.IsZero() || completed.IsZero() || generated.IsZero() || approved.After(started) || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumAssessmentSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("capacity assessment timeline is invalid")
	}
	if !validPositive(input.BetaAccountCap) || !validPositive(input.PlannedPeakConcurrentTenants) || !validPositive(input.SupportedConcurrentTenants) || input.PlannedPeakRetrievalRequestsPerMinute <= 0 || input.PlannedPeakRetrievalRequestsPerMinute > maximumRate || input.SustainedRetrievalRequestsPerMinute <= 0 || input.SustainedRetrievalRequestsPerMinute > maximumRate || input.FixedMonthlyCostMicroUSD < 0 || input.FixedMonthlyCostMicroUSD > maximumCostMicroUSD || input.VariableMonthlyCostPerTenantMicroUSD <= 0 || input.VariableMonthlyCostPerTenantMicroUSD > maximumCostMicroUSD || input.EstimatedWorstCaseMonthlyCostMicroUSD <= 0 || input.EstimatedWorstCaseMonthlyCostMicroUSD > maximumCostMicroUSD || input.ApprovedMonthlyCostCeilingMicroUSD <= 0 || input.ApprovedMonthlyCostCeilingMicroUSD > maximumCostMicroUSD {
		return Receipt{}, errors.New("capacity aggregate metrics are invalid")
	}
	derived, ok := deriveCost(input.FixedMonthlyCostMicroUSD, input.VariableMonthlyCostPerTenantMicroUSD, input.BetaAccountCap)
	if !ok || derived != input.EstimatedWorstCaseMonthlyCostMicroUSD {
		return Receipt{}, errors.New("capacity worst-case cost derivation is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	tenantMet := input.SupportedConcurrentTenants >= input.PlannedPeakConcurrentTenants
	requestMet := input.SustainedRetrievalRequestsPerMinute >= input.PlannedPeakRetrievalRequestsPerMinute
	costMet := derived <= input.ApprovedMonthlyCostCeilingMicroUSD
	if contradictsMetric(checks, CheckTenantHeadroom, tenantMet) || contradictsMetric(checks, CheckRequestHeadroom, requestMet) || contradictsMetric(checks, CheckMonthlyCost, costMet) {
		return Receipt{}, errors.New("capacity outcome contradicts aggregate observation")
	}
	breaches := 0
	for _, met := range []bool{tenantMet, requestMet, costMet} {
		if !met {
			breaches++
		}
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("capacity readiness contradicts evidence")
	}
	output := input
	output.Schema = ReceiptSchemaV1
	output.ReleaseReceiptSHA256 = releaseDigest
	output.RetrievalLoadReceiptSHA256 = loadDigest
	output.DecisionApprovedAt = approved
	output.AssessmentStartedAt = started
	output.AssessmentCompletedAt = completed
	output.GeneratedAt = generated
	output.EstimatedWorstCaseMonthlyCostMicroUSD = derived
	output.Ready = ready
	output.Checks = checks
	return Receipt{Input: output, InputSHA256: inputDigest, CollectedAt: now, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, MetricBreachCount: breaches}, nil
}

func validRelease(r platformrollback.ReleaseReceipt, d string) bool {
	return r.Schema == "agent-memory-kubernetes-release-receipt-v1" && r.Environment == "staging" && r.Namespace == "agent-memory-staging" && opaquePattern.MatchString(r.ReleaseID) && r.Outcome == "passed" && r.Migration.Outcome == "complete" && r.Rollouts.Outcome == "healthy" && !r.Rollback.Attempted && !r.Rollback.Succeeded && digestPattern.MatchString(d)
}
func validLoad(l retrievalload.Receipt, d string, i platforminventory.Inventory, p platformplan.Plan, c platformchange.Receipt, r platformrollback.ReleaseReceipt, rd string) bool {
	return l.Schema == retrievalload.ReceiptSchemaV1 && l.Classification == "staging_external" && l.Environment == "staging" && l.Ready && l.CheckCount == 8 && l.PassedCount == 8 && l.FailedCount == 0 && l.InconclusiveCount == 0 && l.MetricBreachCount == 0 && l.InventoryID == i.InventoryID && l.InventoryReceiptSHA256 == i.ReceiptSHA256 && l.PlanID == p.PlanID && l.PlanReceiptSHA256 == p.ReceiptSHA256 && l.ChangeID == c.ChangeID && l.ChangeReceiptSHA256 == c.ReceiptSHA256 && l.ReleaseID == r.ReleaseID && l.ReleaseReceiptSHA256 == rd && digestPattern.MatchString(d)
}
func allOpaque(v ...string) bool {
	for _, x := range v {
		if !opaquePattern.MatchString(x) {
			return false
		}
	}
	return true
}
func allDigests(v ...string) bool {
	for _, x := range v {
		if !digestPattern.MatchString(x) {
			return false
		}
	}
	return true
}
func validPositive(v int) bool { return v > 0 && v <= maximumCount }
func deriveCost(fixed, variable int64, cap int) (int64, bool) {
	if fixed < 0 || variable < 0 || cap <= 0 || variable > (maximumInt64-fixed)/int64(cap) {
		return 0, false
	}
	total := fixed + variable*int64(cap)
	return total, total <= maximumCostMicroUSD
}

func validateChecks(checks []Check) ([]Check, int, int, int, error) {
	if len(checks) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("capacity checks are incomplete")
	}
	byID := map[CheckID]Check{}
	passed, failed, inconclusive := 0, 0, 0
	for _, check := range checks {
		if !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("capacity check digest is invalid")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("capacity check outcome is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("capacity check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("capacity required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}
func checkOutcome(checks []Check, id CheckID) Outcome {
	for _, c := range checks {
		if c.ID == id {
			return c.Outcome
		}
	}
	return ""
}
func contradictsMetric(checks []Check, id CheckID, met bool) bool {
	o := checkOutcome(checks, id)
	if o == OutcomeInconclusive {
		return false
	}
	return o == "" || (o == OutcomePassed) != met
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("capacity input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("capacity input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open capacity input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("capacity input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read capacity input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("capacity input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("capacity input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("capacity input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("capacity input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("capacity receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("capacity receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect capacity receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-capacity-evidence-*")
}
