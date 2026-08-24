// Package supportevidence normalizes content-free production support-channel
// staffing evidence for P11.1-A.
package supportevidence

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
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	InputSchemaV1        = "agent-memory-production-support-staffing-input-v1"
	ReceiptSchemaV1      = "agent-memory-production-support-staffing-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumPeriod        = 31 * 24 * time.Hour
	maximumCollectionAge = 24 * time.Hour
	maximumMinutes       = 31 * 24 * 60
	maximumSlots         = 100_000
)

type CheckID string
type DrillID string
type Outcome string

const (
	CheckChannelInventory CheckID = "channel_inventory_published"
	CheckResponsePolicy   CheckID = "response_policy_published"
	CheckCoverageRoster   CheckID = "coverage_roster_complete"
	CheckFeedbackRoute    CheckID = "feedback_delivery_acknowledgement"
	CheckIncidentRoute    CheckID = "incident_delivery_acknowledgement"
	CheckBackupEscalation CheckID = "backup_escalation_complete"
	DrillCustomerFeedback DrillID = "customer_feedback"
	DrillSecurityIncident DrillID = "security_incident"
	OutcomePassed         Outcome = "passed"
	OutcomeFailed         Outcome = "failed"
	OutcomeInconclusive   Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckChannelInventory, CheckResponsePolicy, CheckCoverageRoster, CheckFeedbackRoute, CheckIncidentRoute, CheckBackupEscalation}
	requiredDrills = []DrillID{DrillCustomerFeedback, DrillSecurityIncident}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}
type Drill struct {
	ID                            DrillID   `json:"id"`
	OwnerSlotVersion              string    `json:"owner_slot_version"`
	SubmittedAt                   time.Time `json:"submitted_at"`
	DeliveredAt                   time.Time `json:"delivered_at"`
	EscalatedAt                   time.Time `json:"escalated_at"`
	AcknowledgedAt                time.Time `json:"acknowledged_at"`
	ResolvedAt                    time.Time `json:"resolved_at"`
	MaximumDeliverySeconds        int64     `json:"maximum_delivery_seconds"`
	MaximumAcknowledgementSeconds int64     `json:"maximum_acknowledgement_seconds"`
	Outcome                       Outcome   `json:"outcome"`
	EvidenceSHA256                string    `json:"evidence_sha256"`
}
type DrillResult struct {
	Drill
	DeliverySeconds        int64 `json:"delivery_seconds"`
	AcknowledgementSeconds int64 `json:"acknowledgement_seconds"`
}

type Input struct {
	Schema                     string    `json:"schema"`
	Classification             string    `json:"classification"`
	Environment                string    `json:"environment"`
	ReviewID                   string    `json:"review_id"`
	ChannelInventoryVersion    string    `json:"channel_inventory_version"`
	CoverageRosterVersion      string    `json:"coverage_roster_version"`
	ResponsePolicyVersion      string    `json:"response_policy_version"`
	TargetVersion              string    `json:"target_version"`
	InventoryID                string    `json:"inventory_id"`
	InventoryReceiptSHA256     string    `json:"inventory_receipt_sha256"`
	PlanID                     string    `json:"plan_id"`
	PlanReceiptSHA256          string    `json:"plan_receipt_sha256"`
	ChangeID                   string    `json:"change_id"`
	ChangeReceiptSHA256        string    `json:"change_receipt_sha256"`
	ReleaseID                  string    `json:"release_id"`
	ReleaseReceiptSHA256       string    `json:"release_receipt_sha256"`
	ChannelInventorySHA256     string    `json:"channel_inventory_sha256"`
	CoverageRosterSHA256       string    `json:"coverage_roster_sha256"`
	ResponsePolicySHA256       string    `json:"response_policy_sha256"`
	TargetDecisionSHA256       string    `json:"target_decision_sha256"`
	EscalationTestReportSHA256 string    `json:"escalation_test_report_sha256"`
	TargetApprovedAt           time.Time `json:"target_approved_at"`
	PeriodStart                time.Time `json:"period_start"`
	PeriodEnd                  time.Time `json:"period_end"`
	ReviewedAt                 time.Time `json:"reviewed_at"`
	GeneratedAt                time.Time `json:"generated_at"`
	RequiredCoverageMinutes    int       `json:"required_coverage_minutes"`
	PrimaryCoveredMinutes      int       `json:"primary_covered_minutes"`
	BackupCoveredMinutes       int       `json:"backup_covered_minutes"`
	PrimarySlotCount           int       `json:"primary_slot_count"`
	BackupSlotCount            int       `json:"backup_slot_count"`
	Ready                      bool      `json:"ready"`
	Drills                     []Drill   `json:"drills"`
	Checks                     []Check   `json:"checks"`
}
type Receipt struct {
	Input
	Schema            string        `json:"schema"`
	InputSHA256       string        `json:"input_sha256"`
	CollectedAt       time.Time     `json:"collected_at"`
	CoverageComplete  bool          `json:"coverage_complete"`
	DrillResults      []DrillResult `json:"drill_results"`
	TargetBreachCount int           `json:"target_breach_count"`
	CheckCount        int           `json:"check_count"`
	PassedCount       int           `json:"passed_count"`
	FailedCount       int           `json:"failed_count"`
	InconclusiveCount int           `json:"inconclusive_count"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }
func RequiredDrills() []DrillID { return append([]DrillID(nil), requiredDrills...) }

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
	inv, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inv)
	if err != nil {
		return Receipt{}, fmt.Errorf("load production plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inv, plan)
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
	return build(inv, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inv platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inv.Schema != platforminventory.SchemaV1 || inv.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inv.Environment || plan.InventoryID != inv.InventoryID || plan.InventoryReceiptSHA256 != inv.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inv.Environment || change.InventoryID != inv.InventoryID || change.InventoryReceiptSHA256 != inv.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inv.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return Receipt{}, errors.New("support staffing production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("support staffing production release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ReviewID, input.ChannelInventoryVersion, input.CoverageRosterVersion, input.ResponsePolicyVersion, input.TargetVersion) || input.InventoryID != inv.InventoryID || input.InventoryReceiptSHA256 != inv.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(input.ChannelInventorySHA256, input.CoverageRosterSHA256, input.ResponsePolicySHA256, input.TargetDecisionSHA256, input.EscalationTestReportSHA256, inputDigest) {
		return Receipt{}, errors.New("support staffing identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("support staffing collection time is invalid")
	}
	now = now.UTC()
	approved, start, end, reviewed, generated := input.TargetApprovedAt.UTC(), input.PeriodStart.UTC(), input.PeriodEnd.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if approved.IsZero() || approved.After(start) || start.Before(earliest) || !end.After(start) || end.Sub(start) > maximumPeriod || reviewed.Before(end) || reviewed.Before(now.Add(-maximumCollectionAge)) || generated.Before(reviewed) || generated.After(now) {
		return Receipt{}, errors.New("support staffing timeline is invalid")
	}
	if input.RequiredCoverageMinutes <= 0 || input.RequiredCoverageMinutes > maximumMinutes || input.PrimaryCoveredMinutes < 0 || input.PrimaryCoveredMinutes > maximumMinutes || input.BackupCoveredMinutes < 0 || input.BackupCoveredMinutes > maximumMinutes || input.PrimarySlotCount <= 0 || input.PrimarySlotCount > maximumSlots || input.BackupSlotCount <= 0 || input.BackupSlotCount > maximumSlots {
		return Receipt{}, errors.New("support staffing coverage is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	drills, breaches, err := validateDrills(input.Drills)
	if err != nil {
		return Receipt{}, err
	}
	coverage := input.PrimaryCoveredMinutes >= input.RequiredCoverageMinutes && input.BackupCoveredMinutes >= input.RequiredCoverageMinutes
	if !coverage && outcomeFor(checks, CheckCoverageRoster) != OutcomeFailed {
		return Receipt{}, errors.New("support staffing coverage outcome contradicts aggregate observation")
	}
	drillsPassed := 0
	for _, d := range drills {
		checkID := CheckFeedbackRoute
		if d.ID == DrillSecurityIncident {
			checkID = CheckIncidentRoute
		}
		if d.SubmittedAt.Before(start) || d.ResolvedAt.After(end) {
			return Receipt{}, errors.New("support staffing drill falls outside review period")
		}
		if d.Outcome == OutcomePassed {
			drillsPassed++
		} else if outcomeFor(checks, checkID) == OutcomePassed || outcomeFor(checks, CheckBackupEscalation) == OutcomePassed {
			return Receipt{}, errors.New("support staffing drill outcome contradicts check outcome")
		}
		breach := d.DeliverySeconds > d.MaximumDeliverySeconds || d.AcknowledgementSeconds > d.MaximumAcknowledgementSeconds
		if breach && (d.Outcome != OutcomeFailed || outcomeFor(checks, checkID) != OutcomeFailed || outcomeFor(checks, CheckBackupEscalation) != OutcomeFailed) {
			return Receipt{}, errors.New("support staffing drill outcome contradicts aggregate observation")
		}
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && drillsPassed == len(requiredDrills) && coverage && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("support staffing readiness contradicts evidence")
	}
	input.Schema, input.TargetApprovedAt, input.PeriodStart, input.PeriodEnd, input.ReviewedAt, input.GeneratedAt, input.Checks = ReceiptSchemaV1, approved, start, end, reviewed, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, CoverageComplete: coverage, DrillResults: drills, TargetBreachCount: breaches, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("support staffing checks are incomplete")
	}
	byID := map[CheckID]Check{}
	for _, c := range input {
		if _, ok := byID[c.ID]; ok || !digestPattern.MatchString(c.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("support staffing check is invalid or duplicated")
		}
		byID[c.ID] = c
	}
	ordered := make([]Check, 0, len(requiredChecks))
	p, f, i := 0, 0, 0
	for _, id := range requiredChecks {
		c, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("support staffing check is missing")
		}
		switch c.Outcome {
		case OutcomePassed:
			p++
		case OutcomeFailed:
			f++
		case OutcomeInconclusive:
			i++
		default:
			return nil, 0, 0, 0, errors.New("support staffing outcome is invalid")
		}
		ordered = append(ordered, c)
	}
	return ordered, p, f, i, nil
}
func validateDrills(input []Drill) ([]DrillResult, int, error) {
	if len(input) != len(requiredDrills) {
		return nil, 0, errors.New("support staffing drills are incomplete")
	}
	byID := map[DrillID]Drill{}
	for _, d := range input {
		if _, ok := byID[d.ID]; ok || !allOpaque(d.OwnerSlotVersion) || !digestPattern.MatchString(d.EvidenceSHA256) {
			return nil, 0, errors.New("support staffing drill is invalid or duplicated")
		}
		byID[d.ID] = d
	}
	results := make([]DrillResult, 0, len(requiredDrills))
	breaches := 0
	for _, id := range requiredDrills {
		d, ok := byID[id]
		if !ok {
			return nil, 0, errors.New("support staffing drill is missing")
		}
		s, del, e, a, r := d.SubmittedAt.UTC(), d.DeliveredAt.UTC(), d.EscalatedAt.UTC(), d.AcknowledgedAt.UTC(), d.ResolvedAt.UTC()
		if s.IsZero() || del.Before(s) || e.Before(del) || a.Before(e) || r.Before(a) || d.MaximumDeliverySeconds <= 0 || d.MaximumDeliverySeconds > 86400 || d.MaximumAcknowledgementSeconds <= 0 || d.MaximumAcknowledgementSeconds > 86400 {
			return nil, 0, errors.New("support staffing drill timeline or target is invalid")
		}
		delivery := int64(del.Sub(s) / time.Second)
		ack := int64(a.Sub(s) / time.Second)
		breach := delivery > d.MaximumDeliverySeconds || ack > d.MaximumAcknowledgementSeconds
		if breach {
			breaches++
		}
		if d.Outcome != OutcomePassed && d.Outcome != OutcomeFailed && d.Outcome != OutcomeInconclusive {
			return nil, 0, errors.New("support staffing drill outcome is invalid")
		}
		d.SubmittedAt, d.DeliveredAt, d.EscalatedAt, d.AcknowledgedAt, d.ResolvedAt = s, del, e, a, r
		results = append(results, DrillResult{Drill: d, DeliverySeconds: delivery, AcknowledgementSeconds: ack})
	}
	return results, breaches, nil
}
func outcomeFor(checks []Check, id CheckID) Outcome {
	for _, c := range checks {
		if c.ID == id {
			return c.Outcome
		}
	}
	return ""
}
func allDigests(v ...string) bool {
	for _, s := range v {
		if !digestPattern.MatchString(s) {
			return false
		}
	}
	return true
}
func allOpaque(v ...string) bool {
	for _, s := range v {
		if !opaquePattern.MatchString(s) || strings.Contains(s, "@") {
			return false
		}
	}
	return true
}
func decodeStrictRegular(path string, target any) (string, error) {
	return decodeStrictRegularWithHook(path, target, nil)
}

func decodeStrictRegularWithHook(path string, target any, afterValidate func()) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("support staffing input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("open support staffing input: %w", err)
	}
	if !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("support staffing input must be a bounded regular file")
	}
	if afterValidate != nil {
		afterValidate()
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open support staffing input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("support staffing input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(data)) != opened.Size() || len(data) > maximumInputBytes {
		return "", errors.New("read support staffing input")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return "", fmt.Errorf("decode support staffing input: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return "", errors.New("support staffing input has trailing data")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("support staffing input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("support staffing input changed while reading")
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}
func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("support staffing receipt path is required")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("support staffing receipt path is a symlink")
		}
		return errors.New("support staffing receipt already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return evidencepublish.JSON(path, receipt, ".support-staffing-*")
}
