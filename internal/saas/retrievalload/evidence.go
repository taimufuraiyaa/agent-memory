// Package retrievalload normalizes content-free deployed staging retrieval
// load, latency, model-route, and cost evidence for CP5-C.
package retrievalload

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
)

const (
	InputSchemaV1         = "agent-memory-staging-retrieval-load-input-v1"
	ReceiptSchemaV1       = "agent-memory-staging-retrieval-load-receipt-v1"
	maximumInputBytes     = 128 << 10
	maximumRunSpan        = 24 * time.Hour
	maximumCollectionAge  = 24 * time.Hour
	maximumCount          = 100_000_000
	searchP95Microseconds = int64(800_000)
	maximumLatencyMicros  = int64((24 * time.Hour) / time.Microsecond)
	maximumCostMicroUSD   = int64(1_000_000_000_000_000)
)

type CheckID string
type Outcome string

const (
	CheckRepresentativeCorpus CheckID = "representative_corpus_verified"
	CheckDeploymentSite       CheckID = "deployed_site_verified"
	CheckApprovedModelRoute   CheckID = "approved_model_route_exercised"
	CheckConcurrency          CheckID = "concurrency_profile_completed"
	CheckLatencyDistribution  CheckID = "latency_distribution_complete"
	CheckSearchP95            CheckID = "search_p95_target_met"
	CheckCostAttribution      CheckID = "model_cost_attribution_complete"
	CheckModelCost            CheckID = "model_cost_target_met"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckRepresentativeCorpus, CheckDeploymentSite, CheckApprovedModelRoute, CheckConcurrency, CheckLatencyDistribution, CheckSearchP95, CheckCostAttribution, CheckModelCost}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                                   string    `json:"schema"`
	Classification                           string    `json:"classification"`
	Environment                              string    `json:"environment"`
	RunID                                    string    `json:"run_id"`
	WorkloadVersion                          string    `json:"workload_version"`
	DeploymentSiteVersion                    string    `json:"deployment_site_version"`
	ModelRouteVersion                        string    `json:"model_route_version"`
	TargetVersion                            string    `json:"target_version"`
	InventoryID                              string    `json:"inventory_id"`
	InventoryReceiptSHA256                   string    `json:"inventory_receipt_sha256"`
	PlanID                                   string    `json:"plan_id"`
	PlanReceiptSHA256                        string    `json:"plan_receipt_sha256"`
	ChangeID                                 string    `json:"change_id"`
	ChangeReceiptSHA256                      string    `json:"change_receipt_sha256"`
	ReleaseID                                string    `json:"release_id"`
	ReleaseReceiptSHA256                     string    `json:"release_receipt_sha256"`
	WorkloadManifestSHA256                   string    `json:"workload_manifest_sha256"`
	LoadReportSHA256                         string    `json:"load_report_sha256"`
	ModelCostReportSHA256                    string    `json:"model_cost_report_sha256"`
	TargetDecisionSHA256                     string    `json:"target_decision_sha256"`
	TargetApprovedAt                         time.Time `json:"target_approved_at"`
	RunStartedAt                             time.Time `json:"run_started_at"`
	RunCompletedAt                           time.Time `json:"run_completed_at"`
	GeneratedAt                              time.Time `json:"generated_at"`
	CorpusSourceCount                        int       `json:"corpus_source_count"`
	CorpusPassageCount                       int       `json:"corpus_passage_count"`
	RequestCount                             int       `json:"request_count"`
	Concurrency                              int       `json:"concurrency"`
	ErrorCount                               int       `json:"error_count"`
	ModelCallCount                           int       `json:"model_call_count"`
	P50LatencyMicroseconds                   int64     `json:"p50_latency_microseconds"`
	P95LatencyMicroseconds                   int64     `json:"p95_latency_microseconds"`
	P99LatencyMicroseconds                   int64     `json:"p99_latency_microseconds"`
	MaximumModelCostMicroUSDPer1000Requests  int64     `json:"maximum_model_cost_microusd_per_1000_requests"`
	ObservedModelCostMicroUSDPer1000Requests int64     `json:"observed_model_cost_microusd_per_1000_requests"`
	Ready                                    bool      `json:"ready"`
	Checks                                   []Check   `json:"checks"`
}

type Receipt struct {
	Schema                                   string    `json:"schema"`
	Classification                           string    `json:"classification"`
	Environment                              string    `json:"environment"`
	RunID                                    string    `json:"run_id"`
	WorkloadVersion                          string    `json:"workload_version"`
	DeploymentSiteVersion                    string    `json:"deployment_site_version"`
	ModelRouteVersion                        string    `json:"model_route_version"`
	TargetVersion                            string    `json:"target_version"`
	InventoryID                              string    `json:"inventory_id"`
	InventoryReceiptSHA256                   string    `json:"inventory_receipt_sha256"`
	PlanID                                   string    `json:"plan_id"`
	PlanReceiptSHA256                        string    `json:"plan_receipt_sha256"`
	ChangeID                                 string    `json:"change_id"`
	ChangeReceiptSHA256                      string    `json:"change_receipt_sha256"`
	ReleaseID                                string    `json:"release_id"`
	ReleaseReceiptSHA256                     string    `json:"release_receipt_sha256"`
	WorkloadManifestSHA256                   string    `json:"workload_manifest_sha256"`
	LoadReportSHA256                         string    `json:"load_report_sha256"`
	ModelCostReportSHA256                    string    `json:"model_cost_report_sha256"`
	TargetDecisionSHA256                     string    `json:"target_decision_sha256"`
	InputSHA256                              string    `json:"input_sha256"`
	TargetApprovedAt                         time.Time `json:"target_approved_at"`
	RunStartedAt                             time.Time `json:"run_started_at"`
	RunCompletedAt                           time.Time `json:"run_completed_at"`
	GeneratedAt                              time.Time `json:"generated_at"`
	CollectedAt                              time.Time `json:"collected_at"`
	CorpusSourceCount                        int       `json:"corpus_source_count"`
	CorpusPassageCount                       int       `json:"corpus_passage_count"`
	RequestCount                             int       `json:"request_count"`
	Concurrency                              int       `json:"concurrency"`
	ErrorCount                               int       `json:"error_count"`
	ModelCallCount                           int       `json:"model_call_count"`
	P50LatencyMicroseconds                   int64     `json:"p50_latency_microseconds"`
	P95LatencyMicroseconds                   int64     `json:"p95_latency_microseconds"`
	P99LatencyMicroseconds                   int64     `json:"p99_latency_microseconds"`
	SearchP95TargetMicroseconds              int64     `json:"search_p95_target_microseconds"`
	MaximumModelCostMicroUSDPer1000Requests  int64     `json:"maximum_model_cost_microusd_per_1000_requests"`
	ObservedModelCostMicroUSDPer1000Requests int64     `json:"observed_model_cost_microusd_per_1000_requests"`
	Ready                                    bool      `json:"ready"`
	CheckCount                               int       `json:"check_count"`
	PassedCount                              int       `json:"passed_count"`
	FailedCount                              int       `json:"failed_count"`
	InconclusiveCount                        int       `json:"inconclusive_count"`
	MetricBreachCount                        int       `json:"metric_breach_count"`
	Checks                                   []Check   `json:"checks"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

// LoadReadyReceipt reloads a published CP5-C receipt by exact opened bytes. It
// accepts only a canonical ready receipt so downstream gates cannot promote a
// valid-unready load result.
func LoadReadyReceipt(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil {
		return Receipt{}, "", err
	}
	canonical := len(checks) == len(receipt.Checks)
	for index := range checks {
		canonical = canonical && checks[index].ID == receipt.Checks[index].ID && checks[index].Outcome == OutcomePassed
	}
	validIdentity := receipt.Schema == ReceiptSchemaV1 && receipt.Classification == "staging_external" && receipt.Environment == "staging" &&
		opaquePattern.MatchString(receipt.RunID) && opaquePattern.MatchString(receipt.WorkloadVersion) && opaquePattern.MatchString(receipt.DeploymentSiteVersion) && opaquePattern.MatchString(receipt.ModelRouteVersion) && opaquePattern.MatchString(receipt.TargetVersion) &&
		opaquePattern.MatchString(receipt.InventoryID) && opaquePattern.MatchString(receipt.PlanID) && opaquePattern.MatchString(receipt.ChangeID) && opaquePattern.MatchString(receipt.ReleaseID)
	validDigests := digestPattern.MatchString(receipt.InventoryReceiptSHA256) && digestPattern.MatchString(receipt.PlanReceiptSHA256) && digestPattern.MatchString(receipt.ChangeReceiptSHA256) && digestPattern.MatchString(receipt.ReleaseReceiptSHA256) && digestPattern.MatchString(receipt.WorkloadManifestSHA256) && digestPattern.MatchString(receipt.LoadReportSHA256) && digestPattern.MatchString(receipt.ModelCostReportSHA256) && digestPattern.MatchString(receipt.TargetDecisionSHA256) && digestPattern.MatchString(receipt.InputSHA256)
	validCounts := validPositiveCount(receipt.CorpusSourceCount) && validPositiveCount(receipt.CorpusPassageCount) && validPositiveCount(receipt.RequestCount) && validPositiveCount(receipt.Concurrency) && receipt.Concurrency <= receipt.RequestCount && receipt.ErrorCount == 0 && validPositiveCount(receipt.ModelCallCount)
	validLatency := receipt.P50LatencyMicroseconds >= 0 && receipt.P50LatencyMicroseconds <= receipt.P95LatencyMicroseconds && receipt.P95LatencyMicroseconds <= receipt.P99LatencyMicroseconds && receipt.P99LatencyMicroseconds <= maximumLatencyMicros && receipt.SearchP95TargetMicroseconds == searchP95Microseconds && receipt.P95LatencyMicroseconds < receipt.SearchP95TargetMicroseconds
	validCost := receipt.MaximumModelCostMicroUSDPer1000Requests > 0 && receipt.MaximumModelCostMicroUSDPer1000Requests <= maximumCostMicroUSD && receipt.ObservedModelCostMicroUSDPer1000Requests >= 0 && receipt.ObservedModelCostMicroUSDPer1000Requests <= receipt.MaximumModelCostMicroUSDPer1000Requests
	validTimeline := !receipt.TargetApprovedAt.IsZero() && !receipt.RunStartedAt.IsZero() && !receipt.RunCompletedAt.IsZero() && !receipt.GeneratedAt.IsZero() && !receipt.CollectedAt.IsZero() && !receipt.TargetApprovedAt.After(receipt.RunStartedAt) && !receipt.RunCompletedAt.Before(receipt.RunStartedAt) && receipt.RunCompletedAt.Sub(receipt.RunStartedAt) <= maximumRunSpan && !receipt.GeneratedAt.Before(receipt.RunCompletedAt) && !receipt.CollectedAt.Before(receipt.GeneratedAt)
	validSummary := receipt.Ready && receipt.CheckCount == len(requiredChecks) && receipt.PassedCount == len(requiredChecks) && passed == len(requiredChecks) && receipt.FailedCount == 0 && failed == 0 && receipt.InconclusiveCount == 0 && inconclusive == 0 && receipt.MetricBreachCount == 0
	if !canonical || !validIdentity || !validDigests || !validCounts || !validLatency || !validCost || !validTimeline || !validSummary {
		return Receipt{}, "", errors.New("retrieval load receipt is invalid or unready")
	}
	return receipt, digest, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed infrastructure plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed infrastructure change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load passed staging release: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging ||
		plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready ||
		!digestPattern.MatchString(inventory.ReceiptSHA256) || !digestPattern.MatchString(plan.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("retrieval load platform chain is invalid or unready")
	}
	if !validPassedRelease(release, releaseDigest) {
		return Receipt{}, errors.New("retrieval load staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		!opaquePattern.MatchString(input.RunID) || !opaquePattern.MatchString(input.WorkloadVersion) || !opaquePattern.MatchString(input.DeploymentSiteVersion) || !opaquePattern.MatchString(input.ModelRouteVersion) || !opaquePattern.MatchString(input.TargetVersion) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest ||
		!digestPattern.MatchString(input.WorkloadManifestSHA256) || !digestPattern.MatchString(input.LoadReportSHA256) || !digestPattern.MatchString(input.ModelCostReportSHA256) || !digestPattern.MatchString(input.TargetDecisionSHA256) || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("retrieval load input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("retrieval load collection time is invalid")
	}
	now = now.UTC()
	approved, started, completed, generated := input.TargetApprovedAt.UTC(), input.RunStartedAt.UTC(), input.RunCompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if approved.IsZero() || started.IsZero() || completed.IsZero() || generated.IsZero() || approved.After(started) || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumRunSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("retrieval load timeline is invalid")
	}
	if !validPositiveCount(input.CorpusSourceCount) || !validPositiveCount(input.CorpusPassageCount) || !validPositiveCount(input.RequestCount) || !validPositiveCount(input.Concurrency) || input.Concurrency > input.RequestCount || !validZeroCount(input.ErrorCount) || input.ErrorCount > input.RequestCount || !validZeroCount(input.ModelCallCount) || !validLatencyOrder(input) || input.MaximumModelCostMicroUSDPer1000Requests <= 0 || input.MaximumModelCostMicroUSDPer1000Requests > maximumCostMicroUSD || input.ObservedModelCostMicroUSDPer1000Requests < 0 || input.ObservedModelCostMicroUSDPer1000Requests > maximumCostMicroUSD {
		return Receipt{}, errors.New("retrieval load aggregate metrics are invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	p95Met := input.P95LatencyMicroseconds < searchP95Microseconds
	costMet := input.ObservedModelCostMicroUSDPer1000Requests <= input.MaximumModelCostMicroUSDPer1000Requests
	zeroErrors := input.ErrorCount == 0
	modelRouteExercised := input.ModelCallCount > 0
	if contradictsMetric(checks, CheckSearchP95, p95Met) || contradictsMetric(checks, CheckModelCost, costMet) || contradictsFailure(checks, CheckConcurrency, zeroErrors) || contradictsFailure(checks, CheckApprovedModelRoute, modelRouteExercised) {
		return Receipt{}, errors.New("retrieval load outcome contradicts aggregate observation")
	}
	breaches := 0
	for _, met := range []bool{zeroErrors, modelRouteExercised, p95Met, costMet} {
		if !met {
			breaches++
		}
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("retrieval load readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, RunID: input.RunID, WorkloadVersion: input.WorkloadVersion, DeploymentSiteVersion: input.DeploymentSiteVersion, ModelRouteVersion: input.ModelRouteVersion, TargetVersion: input.TargetVersion,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256, ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		WorkloadManifestSHA256: input.WorkloadManifestSHA256, LoadReportSHA256: input.LoadReportSHA256, ModelCostReportSHA256: input.ModelCostReportSHA256, TargetDecisionSHA256: input.TargetDecisionSHA256, InputSHA256: inputDigest,
		TargetApprovedAt: approved, RunStartedAt: started, RunCompletedAt: completed, GeneratedAt: generated, CollectedAt: now, CorpusSourceCount: input.CorpusSourceCount, CorpusPassageCount: input.CorpusPassageCount, RequestCount: input.RequestCount, Concurrency: input.Concurrency, ErrorCount: input.ErrorCount, ModelCallCount: input.ModelCallCount,
		P50LatencyMicroseconds: input.P50LatencyMicroseconds, P95LatencyMicroseconds: input.P95LatencyMicroseconds, P99LatencyMicroseconds: input.P99LatencyMicroseconds, SearchP95TargetMicroseconds: searchP95Microseconds, MaximumModelCostMicroUSDPer1000Requests: input.MaximumModelCostMicroUSDPer1000Requests, ObservedModelCostMicroUSDPer1000Requests: input.ObservedModelCostMicroUSDPer1000Requests,
		Ready: ready, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, MetricBreachCount: breaches, Checks: checks}, nil
}

func validPassedRelease(release platformrollback.ReleaseReceipt, digest string) bool {
	return release.Schema == "agent-memory-kubernetes-release-receipt-v1" && release.Environment == "staging" && release.Namespace == "agent-memory-staging" && opaquePattern.MatchString(release.ReleaseID) && release.Outcome == "passed" && release.Migration.Outcome == "complete" && release.Rollouts.Outcome == "healthy" && !release.Rollback.Attempted && !release.Rollback.Succeeded && !release.StartedAt.IsZero() && !release.CompletedAt.Before(release.StartedAt) && digestPattern.MatchString(digest)
}
func validPositiveCount(value int) bool { return value > 0 && value <= maximumCount }
func validZeroCount(value int) bool     { return value >= 0 && value <= maximumCount }
func validLatencyOrder(input Input) bool {
	return input.P50LatencyMicroseconds >= 0 && input.P50LatencyMicroseconds <= input.P95LatencyMicroseconds && input.P95LatencyMicroseconds <= input.P99LatencyMicroseconds && input.P99LatencyMicroseconds <= maximumLatencyMicros
}

func validateChecks(checks []Check) ([]Check, int, int, int, error) {
	if len(checks) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("retrieval load checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	passed, failed, inconclusive := 0, 0, 0
	for _, check := range checks {
		if !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("retrieval load check evidence digest is invalid")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("retrieval load check outcome is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("retrieval load check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("retrieval load required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}
func checkOutcome(checks []Check, id CheckID) Outcome {
	for _, check := range checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}
func contradictsMetric(checks []Check, id CheckID, met bool) bool {
	outcome := checkOutcome(checks, id)
	if outcome == OutcomeInconclusive {
		return false
	}
	return outcome == "" || (outcome == OutcomePassed) != met
}
func contradictsFailure(checks []Check, id CheckID, met bool) bool {
	outcome := checkOutcome(checks, id)
	if !met {
		return outcome != OutcomeFailed
	}
	return outcome == ""
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("retrieval load input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("retrieval load input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open retrieval load input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("retrieval load input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read retrieval load input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("retrieval load input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("retrieval load input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retrieval load input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retrieval load input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("retrieval load receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("retrieval load receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect retrieval load receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-retrieval-load-*")
}
