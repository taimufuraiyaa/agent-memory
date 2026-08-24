// Package alertevidence normalizes content-free staging SLO and cost alert
// routing, acknowledgement, escalation, and resolution evidence for P10.3-B.
package alertevidence

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
	InputSchemaV1        = "agent-memory-staging-alert-routing-input-v1"
	ReceiptSchemaV1      = "agent-memory-staging-alert-routing-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumBundleSpan    = 24 * time.Hour
	maximumCollectionAge = 24 * time.Hour
	maximumTargetSeconds = int64((4 * time.Hour) / time.Second)
)

type AlertID string
type Severity string
type Outcome string

const (
	AlertAPIErrorBudget AlertID  = "api_error_budget_burn"
	AlertAPILatency     AlertID  = "api_latency_high"
	AlertWorkerFailures AlertID  = "worker_failures"
	AlertQueueFailures  AlertID  = "queue_failures"
	AlertObjectFailures AlertID  = "object_storage_failures"
	AlertModelFailures  AlertID  = "model_gateway_failures"
	AlertCostSpike      AlertID  = "cost_spike"
	SeverityPage        Severity = "page"
	SeverityTicket      Severity = "ticket"
	OutcomePassed       Outcome  = "passed"
	OutcomeFailed       Outcome  = "failed"
	OutcomeInconclusive Outcome  = "inconclusive"
)

type AlertDefinition struct {
	ID             AlertID
	PrometheusName string
	Severity       Severity
}

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredAlerts = []AlertDefinition{
		{AlertAPIErrorBudget, "AgentMemoryAPIErrorBudgetBurn", SeverityPage},
		{AlertAPILatency, "AgentMemoryAPILatencyHigh", SeverityTicket},
		{AlertWorkerFailures, "AgentMemoryWorkerFailures", SeverityPage},
		{AlertQueueFailures, "AgentMemoryQueueFailures", SeverityPage},
		{AlertObjectFailures, "AgentMemoryObjectStorageFailures", SeverityPage},
		{AlertModelFailures, "AgentMemoryModelGatewayFailures", SeverityTicket},
		{AlertCostSpike, "AgentMemoryCostSpike", SeverityTicket},
	}
)

type InputAlert struct {
	ID                            AlertID   `json:"id"`
	Severity                      Severity  `json:"severity"`
	OwnerSlotVersion              string    `json:"owner_slot_version"`
	TriggeredAt                   time.Time `json:"triggered_at"`
	DeliveredAt                   time.Time `json:"delivered_at"`
	EscalatedAt                   time.Time `json:"escalated_at"`
	AcknowledgedAt                time.Time `json:"acknowledged_at"`
	ResolvedAt                    time.Time `json:"resolved_at"`
	MaximumDeliverySeconds        int64     `json:"maximum_delivery_seconds"`
	MaximumAcknowledgementSeconds int64     `json:"maximum_acknowledgement_seconds"`
	Outcome                       Outcome   `json:"outcome"`
	EvidenceSHA256                string    `json:"evidence_sha256"`
}

type ReceiptAlert struct {
	InputAlert
	DeliverySeconds        int64 `json:"delivery_seconds"`
	EscalationSeconds      int64 `json:"escalation_seconds"`
	AcknowledgementSeconds int64 `json:"acknowledgement_seconds"`
	ResolutionSeconds      int64 `json:"resolution_seconds"`
}

type Input struct {
	Schema                 string       `json:"schema"`
	Classification         string       `json:"classification"`
	Environment            string       `json:"environment"`
	BundleID               string       `json:"bundle_id"`
	RuleSetVersion         string       `json:"rule_set_version"`
	RouteVersion           string       `json:"route_version"`
	RosterVersion          string       `json:"roster_version"`
	TargetVersion          string       `json:"target_version"`
	InventoryID            string       `json:"inventory_id"`
	InventoryReceiptSHA256 string       `json:"inventory_receipt_sha256"`
	PlanID                 string       `json:"plan_id"`
	PlanReceiptSHA256      string       `json:"plan_receipt_sha256"`
	ChangeID               string       `json:"change_id"`
	ChangeReceiptSHA256    string       `json:"change_receipt_sha256"`
	ReleaseID              string       `json:"release_id"`
	ReleaseReceiptSHA256   string       `json:"release_receipt_sha256"`
	RuleExportSHA256       string       `json:"rule_export_sha256"`
	RouteExportSHA256      string       `json:"route_export_sha256"`
	OwnerRosterSHA256      string       `json:"owner_roster_sha256"`
	SyntheticReportSHA256  string       `json:"synthetic_report_sha256"`
	TargetDecisionSHA256   string       `json:"target_decision_sha256"`
	StartedAt              time.Time    `json:"started_at"`
	CompletedAt            time.Time    `json:"completed_at"`
	GeneratedAt            time.Time    `json:"generated_at"`
	Ready                  bool         `json:"ready"`
	Alerts                 []InputAlert `json:"alerts"`
}

type Receipt struct {
	Schema                              string         `json:"schema"`
	Classification                      string         `json:"classification"`
	Environment                         string         `json:"environment"`
	BundleID                            string         `json:"bundle_id"`
	RuleSetVersion                      string         `json:"rule_set_version"`
	RouteVersion                        string         `json:"route_version"`
	RosterVersion                       string         `json:"roster_version"`
	TargetVersion                       string         `json:"target_version"`
	InventoryID                         string         `json:"inventory_id"`
	InventoryReceiptSHA256              string         `json:"inventory_receipt_sha256"`
	PlanID                              string         `json:"plan_id"`
	PlanReceiptSHA256                   string         `json:"plan_receipt_sha256"`
	ChangeID                            string         `json:"change_id"`
	ChangeReceiptSHA256                 string         `json:"change_receipt_sha256"`
	ReleaseID                           string         `json:"release_id"`
	ReleaseReceiptSHA256                string         `json:"release_receipt_sha256"`
	RuleExportSHA256                    string         `json:"rule_export_sha256"`
	RouteExportSHA256                   string         `json:"route_export_sha256"`
	OwnerRosterSHA256                   string         `json:"owner_roster_sha256"`
	SyntheticReportSHA256               string         `json:"synthetic_report_sha256"`
	TargetDecisionSHA256                string         `json:"target_decision_sha256"`
	InputSHA256                         string         `json:"input_sha256"`
	StartedAt                           time.Time      `json:"started_at"`
	CompletedAt                         time.Time      `json:"completed_at"`
	GeneratedAt                         time.Time      `json:"generated_at"`
	CollectedAt                         time.Time      `json:"collected_at"`
	Ready                               bool           `json:"ready"`
	AlertCount                          int            `json:"alert_count"`
	PassedCount                         int            `json:"passed_count"`
	FailedCount                         int            `json:"failed_count"`
	InconclusiveCount                   int            `json:"inconclusive_count"`
	TargetBreachCount                   int            `json:"target_breach_count"`
	MaximumDeliverySeconds              int64          `json:"maximum_delivery_seconds"`
	MaximumDeliveryTargetSeconds        int64          `json:"maximum_delivery_target_seconds"`
	MaximumAcknowledgementSeconds       int64          `json:"maximum_acknowledgement_seconds"`
	MaximumAcknowledgementTargetSeconds int64          `json:"maximum_acknowledgement_target_seconds"`
	Alerts                              []ReceiptAlert `json:"alerts"`
}

func RequiredAlerts() []AlertDefinition { return append([]AlertDefinition(nil), requiredAlerts...) }

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
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
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready {
		return Receipt{}, errors.New("alert-routing platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("alert-routing release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !allOpaque(input.BundleID, input.RuleSetVersion, input.RouteVersion, input.RosterVersion, input.TargetVersion) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(input.RuleExportSHA256, input.RouteExportSHA256, input.OwnerRosterSHA256, input.SyntheticReportSHA256, input.TargetDecisionSHA256, inputDigest) {
		return Receipt{}, errors.New("alert-routing input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("alert-routing collection time is invalid")
	}
	now = now.UTC()
	started, completed, generated := input.StartedAt.UTC(), input.CompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if started.IsZero() || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumBundleSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("alert-routing bundle timeline is invalid")
	}
	alerts, passed, failed, inconclusive, breaches, maxDelivery, maxDeliveryTarget, maxAck, maxAckTarget, err := validateAlerts(input.Alerts, started, completed)
	if err != nil {
		return Receipt{}, err
	}
	ready := passed == len(requiredAlerts) && failed == 0 && inconclusive == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("alert-routing readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, BundleID: input.BundleID, RuleSetVersion: input.RuleSetVersion, RouteVersion: input.RouteVersion, RosterVersion: input.RosterVersion, TargetVersion: input.TargetVersion, InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256, ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest, RuleExportSHA256: input.RuleExportSHA256, RouteExportSHA256: input.RouteExportSHA256, OwnerRosterSHA256: input.OwnerRosterSHA256, SyntheticReportSHA256: input.SyntheticReportSHA256, TargetDecisionSHA256: input.TargetDecisionSHA256, InputSHA256: inputDigest, StartedAt: started, CompletedAt: completed, GeneratedAt: generated, CollectedAt: now, Ready: ready, AlertCount: len(alerts), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, TargetBreachCount: breaches, MaximumDeliverySeconds: maxDelivery, MaximumDeliveryTargetSeconds: maxDeliveryTarget, MaximumAcknowledgementSeconds: maxAck, MaximumAcknowledgementTargetSeconds: maxAckTarget, Alerts: alerts}, nil
}

func validateAlerts(input []InputAlert, started, completed time.Time) ([]ReceiptAlert, int, int, int, int, int64, int64, int64, int64, error) {
	if len(input) != len(requiredAlerts) {
		return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("alert-routing tests are incomplete")
	}
	byID := make(map[AlertID]InputAlert, len(input))
	for _, alert := range input {
		if _, duplicate := byID[alert.ID]; duplicate {
			return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("alert-routing test is duplicated")
		}
		byID[alert.ID] = alert
	}
	ordered := make([]ReceiptAlert, 0, len(requiredAlerts))
	passed, failed, inconclusive, breaches := 0, 0, 0, 0
	var maxDelivery, maxDeliveryTarget, maxAck, maxAckTarget int64
	for _, definition := range requiredAlerts {
		alert, exists := byID[definition.ID]
		if !exists || alert.Severity != definition.Severity || !opaquePattern.MatchString(alert.OwnerSlotVersion) || !digestPattern.MatchString(alert.EvidenceSHA256) {
			return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("alert-routing fixed identity is invalid")
		}
		triggered, delivered, escalated, acknowledged, resolved := alert.TriggeredAt.UTC(), alert.DeliveredAt.UTC(), alert.EscalatedAt.UTC(), alert.AcknowledgedAt.UTC(), alert.ResolvedAt.UTC()
		if triggered.Before(started) || delivered.Before(triggered) || escalated.Before(delivered) || acknowledged.Before(escalated) || resolved.Before(acknowledged) || resolved.After(completed) || alert.MaximumDeliverySeconds <= 0 || alert.MaximumDeliverySeconds > maximumTargetSeconds || alert.MaximumAcknowledgementSeconds <= 0 || alert.MaximumAcknowledgementSeconds > maximumTargetSeconds {
			return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("alert-routing test timeline or target is invalid")
		}
		delivery := int64(delivered.Sub(triggered) / time.Second)
		escalation := int64(escalated.Sub(triggered) / time.Second)
		ack := int64(acknowledged.Sub(triggered) / time.Second)
		resolution := int64(resolved.Sub(triggered) / time.Second)
		breach := delivery > alert.MaximumDeliverySeconds || ack > alert.MaximumAcknowledgementSeconds
		switch alert.Outcome {
		case OutcomePassed:
			if breach {
				return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("passed alert-routing test breaches target")
			}
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			if breach {
				return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("known alert-routing target breach is not failed")
			}
			inconclusive++
		default:
			return nil, 0, 0, 0, 0, 0, 0, 0, 0, errors.New("alert-routing outcome is invalid")
		}
		if breach {
			breaches++
		}
		maxDelivery = max64(maxDelivery, delivery)
		maxDeliveryTarget = max64(maxDeliveryTarget, alert.MaximumDeliverySeconds)
		maxAck = max64(maxAck, ack)
		maxAckTarget = max64(maxAckTarget, alert.MaximumAcknowledgementSeconds)
		alert.TriggeredAt, alert.DeliveredAt, alert.EscalatedAt, alert.AcknowledgedAt, alert.ResolvedAt = triggered, delivered, escalated, acknowledged, resolved
		ordered = append(ordered, ReceiptAlert{InputAlert: alert, DeliverySeconds: delivery, EscalationSeconds: escalation, AcknowledgementSeconds: ack, ResolutionSeconds: resolution})
	}
	return ordered, passed, failed, inconclusive, breaches, maxDelivery, maxDeliveryTarget, maxAck, maxAckTarget, nil
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
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("alert-routing input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("alert-routing input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open alert-routing input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("alert-routing input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read alert-routing input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("alert-routing input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("alert-routing input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("alert-routing input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("alert-routing input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("alert-routing receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("alert-routing receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect alert-routing receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-alert-routing-*")
}
