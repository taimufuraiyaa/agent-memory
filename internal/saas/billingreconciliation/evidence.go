// Package billingreconciliation normalizes content-free production payment,
// invoice, settlement, and usage-ledger reconciliation evidence for P11.2-A.
package billingreconciliation

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
	InputSchemaV1        = "agent-memory-production-billing-reconciliation-input-v1"
	ReceiptSchemaV1      = "agent-memory-production-billing-reconciliation-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumPeriod        = 31 * 24 * time.Hour
	maximumCollectionAge = 24 * time.Hour
	maximumCount         = 100_000_000
	maximumQuantity      = int64(1_000_000_000_000_000)
	maximumMoneyMicroUSD = int64(1_000_000_000_000_000)
)

type CheckID string
type Outcome string

const (
	CheckProcessorExport          CheckID = "processor_export_complete"
	CheckInvoiceReconciliation    CheckID = "invoice_reconciliation_complete"
	CheckSettlementReconciliation CheckID = "settlement_reconciliation_complete"
	CheckUsageRecomputation       CheckID = "usage_ledger_recomputed"
	CheckSampleCoverage           CheckID = "sample_coverage_complete"
	CheckWebhookOrdering          CheckID = "webhook_ordering_reviewed"
	CheckVarianceTargets          CheckID = "variance_targets_approved"
	CheckFinanceEngineeringReview CheckID = "finance_engineering_review_complete"
	OutcomePassed                 Outcome = "passed"
	OutcomeFailed                 Outcome = "failed"
	OutcomeInconclusive           Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckProcessorExport, CheckInvoiceReconciliation, CheckSettlementReconciliation, CheckUsageRecomputation, CheckSampleCoverage, CheckWebhookOrdering, CheckVarianceTargets, CheckFinanceEngineeringReview}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                            string    `json:"schema"`
	Classification                    string    `json:"classification"`
	Environment                       string    `json:"environment"`
	ReconciliationID                  string    `json:"reconciliation_id"`
	ProcessorExportVersion            string    `json:"processor_export_version"`
	LedgerExportVersion               string    `json:"ledger_export_version"`
	RecomputationVersion              string    `json:"recomputation_version"`
	TargetVersion                     string    `json:"target_version"`
	InventoryID                       string    `json:"inventory_id"`
	InventoryReceiptSHA256            string    `json:"inventory_receipt_sha256"`
	PlanID                            string    `json:"plan_id"`
	PlanReceiptSHA256                 string    `json:"plan_receipt_sha256"`
	ChangeID                          string    `json:"change_id"`
	ChangeReceiptSHA256               string    `json:"change_receipt_sha256"`
	ReleaseID                         string    `json:"release_id"`
	ReleaseReceiptSHA256              string    `json:"release_receipt_sha256"`
	ProcessorInvoiceExportSHA256      string    `json:"processor_invoice_export_sha256"`
	ProcessorSettlementExportSHA256   string    `json:"processor_settlement_export_sha256"`
	InvoiceLedgerExportSHA256         string    `json:"invoice_ledger_export_sha256"`
	UsageLedgerExportSHA256           string    `json:"usage_ledger_export_sha256"`
	UsageRecomputationSHA256          string    `json:"usage_recomputation_sha256"`
	WebhookOrderingReportSHA256       string    `json:"webhook_ordering_report_sha256"`
	TargetDecisionSHA256              string    `json:"target_decision_sha256"`
	TargetApprovedAt                  time.Time `json:"target_approved_at"`
	PeriodStart                       time.Time `json:"period_start"`
	PeriodEnd                         time.Time `json:"period_end"`
	ReconciledAt                      time.Time `json:"reconciled_at"`
	GeneratedAt                       time.Time `json:"generated_at"`
	Currency                          string    `json:"currency"`
	TenantSampleCount                 int       `json:"tenant_sample_count"`
	ProcessorInvoiceCount             int       `json:"processor_invoice_count"`
	MatchedInvoiceCount               int       `json:"matched_invoice_count"`
	ProcessorSettlementCount          int       `json:"processor_settlement_count"`
	MatchedSettlementCount            int       `json:"matched_settlement_count"`
	UsageSampleCount                  int       `json:"usage_sample_count"`
	MatchedUsageSampleCount           int       `json:"matched_usage_sample_count"`
	ProviderInvoicedMicroUSD          int64     `json:"provider_invoiced_microusd"`
	LedgerInvoicedMicroUSD            int64     `json:"ledger_invoiced_microusd"`
	ProviderSettledMicroUSD           int64     `json:"provider_settled_microusd"`
	LedgerSettledMicroUSD             int64     `json:"ledger_settled_microusd"`
	AuthoritativeUsageQuantity        int64     `json:"authoritative_usage_quantity"`
	RecomputedUsageQuantity           int64     `json:"recomputed_usage_quantity"`
	MaximumInvoiceVarianceMicroUSD    int64     `json:"maximum_invoice_variance_microusd"`
	MaximumSettlementVarianceMicroUSD int64     `json:"maximum_settlement_variance_microusd"`
	MaximumUsageVarianceQuantity      int64     `json:"maximum_usage_variance_quantity"`
	Ready                             bool      `json:"ready"`
	Checks                            []Check   `json:"checks"`
}

type Receipt struct {
	Input
	Schema                     string    `json:"schema"`
	InputSHA256                string    `json:"input_sha256"`
	CollectedAt                time.Time `json:"collected_at"`
	InvoiceVarianceMicroUSD    int64     `json:"invoice_variance_microusd"`
	SettlementVarianceMicroUSD int64     `json:"settlement_variance_microusd"`
	UsageVarianceQuantity      int64     `json:"usage_variance_quantity"`
	CoverageComplete           bool      `json:"coverage_complete"`
	VarianceBreachCount        int       `json:"variance_breach_count"`
	CheckCount                 int       `json:"check_count"`
	PassedCount                int       `json:"passed_count"`
	FailedCount                int       `json:"failed_count"`
	InconclusiveCount          int       `json:"inconclusive_count"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

// LoadReady strictly reloads a normalized ready receipt and returns the exact
// file digest for downstream shared-window gate binding.
func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || receipt.Currency != "USD" || !receipt.Ready || !receipt.CoverageComplete || receipt.VarianceBreachCount != 0 || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("billing reconciliation receipt is not ready")
	}
	if !allOpaque(receipt.ReconciliationID, receipt.ProcessorExportVersion, receipt.LedgerExportVersion, receipt.RecomputationVersion, receipt.TargetVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID) {
		return Receipt{}, "", errors.New("billing reconciliation receipt identity is invalid")
	}
	if !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.ProcessorInvoiceExportSHA256, receipt.ProcessorSettlementExportSHA256, receipt.InvoiceLedgerExportSHA256, receipt.UsageLedgerExportSHA256, receipt.UsageRecomputationSHA256, receipt.WebhookOrderingReportSHA256, receipt.TargetDecisionSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("billing reconciliation receipt binding is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("billing reconciliation receipt checks are invalid")
	}
	counts := []int{receipt.TenantSampleCount, receipt.ProcessorInvoiceCount, receipt.MatchedInvoiceCount, receipt.ProcessorSettlementCount, receipt.MatchedSettlementCount, receipt.UsageSampleCount, receipt.MatchedUsageSampleCount}
	for _, count := range counts {
		if count <= 0 || count > maximumCount {
			return Receipt{}, "", errors.New("billing reconciliation receipt sample count is invalid")
		}
	}
	if receipt.MatchedInvoiceCount != receipt.ProcessorInvoiceCount || receipt.MatchedSettlementCount != receipt.ProcessorSettlementCount || receipt.MatchedUsageSampleCount != receipt.UsageSampleCount {
		return Receipt{}, "", errors.New("billing reconciliation receipt coverage is invalid")
	}
	values := []int64{receipt.ProviderInvoicedMicroUSD, receipt.LedgerInvoicedMicroUSD, receipt.ProviderSettledMicroUSD, receipt.LedgerSettledMicroUSD}
	for _, value := range values {
		if value < 0 || value > maximumMoneyMicroUSD {
			return Receipt{}, "", errors.New("billing reconciliation receipt monetary total is invalid")
		}
	}
	if receipt.AuthoritativeUsageQuantity < 0 || receipt.AuthoritativeUsageQuantity > maximumQuantity || receipt.RecomputedUsageQuantity < 0 || receipt.RecomputedUsageQuantity > maximumQuantity || receipt.MaximumInvoiceVarianceMicroUSD <= 0 || receipt.MaximumInvoiceVarianceMicroUSD > maximumMoneyMicroUSD || receipt.MaximumSettlementVarianceMicroUSD <= 0 || receipt.MaximumSettlementVarianceMicroUSD > maximumMoneyMicroUSD || receipt.MaximumUsageVarianceQuantity <= 0 || receipt.MaximumUsageVarianceQuantity > maximumQuantity {
		return Receipt{}, "", errors.New("billing reconciliation receipt target is invalid")
	}
	invoiceVariance := absoluteDifference(receipt.ProviderInvoicedMicroUSD, receipt.LedgerInvoicedMicroUSD)
	settlementVariance := absoluteDifference(receipt.ProviderSettledMicroUSD, receipt.LedgerSettledMicroUSD)
	usageVariance := absoluteDifference(receipt.AuthoritativeUsageQuantity, receipt.RecomputedUsageQuantity)
	if invoiceVariance > receipt.MaximumInvoiceVarianceMicroUSD || settlementVariance > receipt.MaximumSettlementVarianceMicroUSD || usageVariance > receipt.MaximumUsageVarianceQuantity || receipt.InvoiceVarianceMicroUSD != invoiceVariance || receipt.SettlementVarianceMicroUSD != settlementVariance || receipt.UsageVarianceQuantity != usageVariance {
		return Receipt{}, "", errors.New("billing reconciliation receipt derivation is invalid")
	}
	approved, start, end := receipt.TargetApprovedAt.UTC(), receipt.PeriodStart.UTC(), receipt.PeriodEnd.UTC()
	reconciled, generated, collected := receipt.ReconciledAt.UTC(), receipt.GeneratedAt.UTC(), receipt.CollectedAt.UTC()
	if approved.IsZero() || approved.After(start) || !end.After(start) || end.Sub(start) > maximumPeriod || reconciled.Before(end) || generated.Before(reconciled) || collected.Before(generated) || collected.IsZero() {
		return Receipt{}, "", errors.New("billing reconciliation receipt timeline is invalid")
	}
	receipt.TargetApprovedAt, receipt.PeriodStart, receipt.PeriodEnd = approved, start, end
	receipt.ReconciledAt, receipt.GeneratedAt, receipt.CollectedAt, receipt.Checks = reconciled, generated, collected, checks
	return receipt, digest, nil
}

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedReleaseForEnvironment(releasePath, "production")
	if err != nil {
		return Receipt{}, fmt.Errorf("load production release: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || !paymentEnabled(inventory) || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return Receipt{}, errors.New("billing reconciliation production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("billing reconciliation production release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || input.Currency != "USD" || !allOpaque(input.ReconciliationID, input.ProcessorExportVersion, input.LedgerExportVersion, input.RecomputationVersion, input.TargetVersion) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(input.ProcessorInvoiceExportSHA256, input.ProcessorSettlementExportSHA256, input.InvoiceLedgerExportSHA256, input.UsageLedgerExportSHA256, input.UsageRecomputationSHA256, input.WebhookOrderingReportSHA256, input.TargetDecisionSHA256, inputDigest) {
		return Receipt{}, errors.New("billing reconciliation identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("billing reconciliation collection time is invalid")
	}
	now = now.UTC()
	approved, start, end, reconciled, generated := input.TargetApprovedAt.UTC(), input.PeriodStart.UTC(), input.PeriodEnd.UTC(), input.ReconciledAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if approved.IsZero() || approved.After(start) || start.Before(earliest) || !end.After(start) || end.Sub(start) > maximumPeriod || reconciled.Before(end) || reconciled.Before(now.Add(-maximumCollectionAge)) || generated.Before(reconciled) || generated.After(now) {
		return Receipt{}, errors.New("billing reconciliation timeline is invalid")
	}
	counts := []int{input.TenantSampleCount, input.ProcessorInvoiceCount, input.MatchedInvoiceCount, input.ProcessorSettlementCount, input.MatchedSettlementCount, input.UsageSampleCount, input.MatchedUsageSampleCount}
	for _, count := range counts {
		if count <= 0 || count > maximumCount {
			return Receipt{}, errors.New("billing reconciliation sample count is invalid")
		}
	}
	values := []int64{input.ProviderInvoicedMicroUSD, input.LedgerInvoicedMicroUSD, input.ProviderSettledMicroUSD, input.LedgerSettledMicroUSD}
	for _, value := range values {
		if value < 0 || value > maximumMoneyMicroUSD {
			return Receipt{}, errors.New("billing reconciliation monetary total is invalid")
		}
	}
	quantities := []int64{input.AuthoritativeUsageQuantity, input.RecomputedUsageQuantity}
	for _, value := range quantities {
		if value < 0 || value > maximumQuantity {
			return Receipt{}, errors.New("billing reconciliation usage quantity is invalid")
		}
	}
	if input.MaximumInvoiceVarianceMicroUSD <= 0 || input.MaximumInvoiceVarianceMicroUSD > maximumMoneyMicroUSD || input.MaximumSettlementVarianceMicroUSD <= 0 || input.MaximumSettlementVarianceMicroUSD > maximumMoneyMicroUSD || input.MaximumUsageVarianceQuantity <= 0 || input.MaximumUsageVarianceQuantity > maximumQuantity {
		return Receipt{}, errors.New("billing reconciliation variance target is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	invoiceVariance := absoluteDifference(input.ProviderInvoicedMicroUSD, input.LedgerInvoicedMicroUSD)
	settlementVariance := absoluteDifference(input.ProviderSettledMicroUSD, input.LedgerSettledMicroUSD)
	usageVariance := absoluteDifference(input.AuthoritativeUsageQuantity, input.RecomputedUsageQuantity)
	coverage := input.MatchedInvoiceCount == input.ProcessorInvoiceCount && input.MatchedSettlementCount == input.ProcessorSettlementCount && input.MatchedUsageSampleCount == input.UsageSampleCount
	breaches := 0
	for _, breach := range []bool{invoiceVariance > input.MaximumInvoiceVarianceMicroUSD, settlementVariance > input.MaximumSettlementVarianceMicroUSD, usageVariance > input.MaximumUsageVarianceQuantity} {
		if breach {
			breaches++
		}
	}
	if (invoiceVariance > input.MaximumInvoiceVarianceMicroUSD && outcomeFor(checks, CheckInvoiceReconciliation) != OutcomeFailed) || (settlementVariance > input.MaximumSettlementVarianceMicroUSD && outcomeFor(checks, CheckSettlementReconciliation) != OutcomeFailed) || (usageVariance > input.MaximumUsageVarianceQuantity && outcomeFor(checks, CheckUsageRecomputation) != OutcomeFailed) || (!coverage && outcomeFor(checks, CheckSampleCoverage) != OutcomeFailed) {
		return Receipt{}, errors.New("billing reconciliation outcome contradicts aggregate observation")
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && coverage && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("billing reconciliation readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.TargetApprovedAt, input.PeriodStart, input.PeriodEnd, input.ReconciledAt, input.GeneratedAt, input.Checks = approved, start, end, reconciled, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, InvoiceVarianceMicroUSD: invoiceVariance, SettlementVarianceMicroUSD: settlementVariance, UsageVarianceQuantity: usageVariance, CoverageComplete: coverage, VarianceBreachCount: breaches, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}
func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("billing reconciliation checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("billing reconciliation check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("billing reconciliation check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("billing reconciliation outcome is invalid")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}
func paymentEnabled(inventory platforminventory.Inventory) bool {
	for _, integration := range inventory.ExternalIntegrations {
		if integration.Kind == platforminventory.IntegrationPayment {
			return integration.Enabled
		}
	}
	return false
}
func outcomeFor(checks []Check, id CheckID) Outcome {
	for _, check := range checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}
func absoluteDifference(a, b int64) int64 {
	if a >= b {
		return a - b
	}
	return b - a
}
func allOpaque(values ...string) bool {
	for _, v := range values {
		if !opaquePattern.MatchString(v) {
			return false
		}
	}
	return true
}
func allDigests(values ...string) bool {
	for _, v := range values {
		if !digestPattern.MatchString(v) {
			return false
		}
	}
	return true
}
func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("billing reconciliation input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("billing reconciliation input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open billing reconciliation input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("billing reconciliation input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read billing reconciliation input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("billing reconciliation input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("billing reconciliation input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("billing reconciliation input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("billing reconciliation input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("billing reconciliation receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("billing reconciliation receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect billing reconciliation receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-billing-reconciliation-*")
}
