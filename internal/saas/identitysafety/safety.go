// Package identitysafety normalizes content-free staging evidence for the
// identity-provider outage and credential-revocation CP2-B drills.
package identitysafety

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
	InputSchemaV1        = "agent-memory-staging-identity-safety-drills-v1"
	ReceiptSchemaV1      = "agent-memory-staging-identity-safety-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumDrillDuration = 4 * time.Hour
	maximumCollectionAge = 24 * time.Hour
	maximumRTOTarget     = 24 * time.Hour
)

type DrillID string
type CheckID string
type Outcome string

const (
	DrillIdentityProviderOutage DrillID = "identity_provider_outage"
	DrillCredentialRevocation   DrillID = "credential_revocation"

	CheckRealAlertDelivered       CheckID = "real_alert_delivered"
	CheckCachedKeyContinuity      CheckID = "cached_key_authentication_continued"
	CheckUnknownTrustFailedClosed CheckID = "unknown_or_invalid_trust_failed_closed"
	CheckContainmentExecuted      CheckID = "containment_executed"
	CheckServiceRecovered         CheckID = "service_recovered"
	CheckAbuseDetected            CheckID = "credential_abuse_detected"
	CheckIndependentApproval      CheckID = "independent_approval_verified"
	CheckProductionPathRevocation CheckID = "production_path_revocation_completed"
	CheckPostRevokeDenied         CheckID = "post_revoke_access_denied"
	CheckImmutableAuditRetained   CheckID = "immutable_audit_retained"
	CheckCustomerContentAbsent    CheckID = "customer_content_absent"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredDrills = []DrillID{DrillIdentityProviderOutage, DrillCredentialRevocation}
	requiredChecks = map[DrillID][]CheckID{
		DrillIdentityProviderOutage: {
			CheckRealAlertDelivered, CheckCachedKeyContinuity, CheckUnknownTrustFailedClosed,
			CheckContainmentExecuted, CheckServiceRecovered, CheckImmutableAuditRetained, CheckCustomerContentAbsent,
		},
		DrillCredentialRevocation: {
			CheckAbuseDetected, CheckRealAlertDelivered, CheckIndependentApproval, CheckProductionPathRevocation,
			CheckPostRevokeDenied, CheckContainmentExecuted, CheckImmutableAuditRetained, CheckCustomerContentAbsent,
		},
	}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Drill struct {
	ID                   DrillID   `json:"id"`
	ImpairmentAt         time.Time `json:"impairment_at"`
	DetectedAt           time.Time `json:"detected_at"`
	AlertedAt            time.Time `json:"alerted_at"`
	ContainedAt          time.Time `json:"contained_at"`
	RecoveredAt          time.Time `json:"recovered_at"`
	RTOTargetSeconds     int64     `json:"rto_target_seconds"`
	TargetApprovalSHA256 string    `json:"target_approval_sha256"`
	DetectionSeconds     int64     `json:"detection_seconds,omitempty"`
	AlertSeconds         int64     `json:"alert_seconds,omitempty"`
	ContainmentSeconds   int64     `json:"containment_seconds,omitempty"`
	RTOSeconds           int64     `json:"rto_seconds,omitempty"`
	Checks               []Check   `json:"checks"`
}

type Input struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	Environment            string    `json:"environment"`
	BundleID               string    `json:"bundle_id"`
	InventoryID            string    `json:"inventory_id"`
	InventoryReceiptSHA256 string    `json:"inventory_receipt_sha256"`
	PlanID                 string    `json:"plan_id"`
	PlanReceiptSHA256      string    `json:"plan_receipt_sha256"`
	ChangeID               string    `json:"change_id"`
	ChangeReceiptSHA256    string    `json:"change_receipt_sha256"`
	ReleaseID              string    `json:"release_id"`
	ReleaseReceiptSHA256   string    `json:"release_receipt_sha256"`
	Ready                  bool      `json:"ready"`
	GeneratedAt            time.Time `json:"generated_at"`
	Drills                 []Drill   `json:"drills"`
}

type Receipt struct {
	Schema                  string    `json:"schema"`
	Classification          string    `json:"classification"`
	Environment             string    `json:"environment"`
	BundleID                string    `json:"bundle_id"`
	InventoryID             string    `json:"inventory_id"`
	InventoryReceiptSHA256  string    `json:"inventory_receipt_sha256"`
	PlanID                  string    `json:"plan_id"`
	PlanReceiptSHA256       string    `json:"plan_receipt_sha256"`
	ChangeID                string    `json:"change_id"`
	ChangeReceiptSHA256     string    `json:"change_receipt_sha256"`
	ReleaseID               string    `json:"release_id"`
	ReleaseReceiptSHA256    string    `json:"release_receipt_sha256"`
	InputSHA256             string    `json:"input_sha256"`
	Ready                   bool      `json:"ready"`
	GeneratedAt             time.Time `json:"generated_at"`
	CollectedAt             time.Time `json:"collected_at"`
	DrillCount              int       `json:"drill_count"`
	CheckCount              int       `json:"check_count"`
	PassedCount             int       `json:"passed_count"`
	FailedCount             int       `json:"failed_count"`
	InconclusiveCount       int       `json:"inconclusive_count"`
	TargetBreachCount       int       `json:"target_breach_count"`
	MaximumRTOSeconds       int64     `json:"maximum_rto_seconds"`
	MaximumRTOTargetSeconds int64     `json:"maximum_rto_target_seconds"`
	Drills                  []Drill   `json:"drills"`
}

func RequiredChecks(drill DrillID) []CheckID { return append([]CheckID(nil), requiredChecks[drill]...) }

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
		plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID ||
		plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID ||
		change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 ||
		!platformchange.Assess(change).Ready || !digestPattern.MatchString(inventory.ReceiptSHA256) || !digestPattern.MatchString(plan.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("identity-safety platform chain is invalid or unready")
	}
	if !validPassedRelease(release, releaseDigest) {
		return Receipt{}, errors.New("identity-safety staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !opaquePattern.MatchString(input.BundleID) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 ||
		input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("identity-safety input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("identity-safety collection time is invalid")
	}
	now = now.UTC()
	generated := input.GeneratedAt.UTC()
	if generated.IsZero() || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) || generated.Before(release.CompletedAt.UTC()) {
		return Receipt{}, errors.New("identity-safety collection timeline is invalid")
	}
	drills, passed, failed, inconclusive, breaches, maxRTO, maxTarget, err := validateDrills(input.Drills, release.CompletedAt.UTC(), generated)
	if err != nil {
		return Receipt{}, err
	}
	ready := passed == totalRequiredChecks() && failed == 0 && inconclusive == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("identity-safety readiness contradicts drill evidence")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, BundleID: input.BundleID,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256,
		ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		InputSHA256: inputDigest, Ready: ready, GeneratedAt: generated, CollectedAt: now, DrillCount: len(drills), CheckCount: passed + failed + inconclusive,
		PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, TargetBreachCount: breaches, MaximumRTOSeconds: maxRTO,
		MaximumRTOTargetSeconds: maxTarget, Drills: drills,
	}, nil
}

func validPassedRelease(release platformrollback.ReleaseReceipt, digest string) bool {
	return release.Schema == "agent-memory-kubernetes-release-receipt-v1" && release.Environment == "staging" && release.Namespace == "agent-memory-staging" &&
		opaquePattern.MatchString(release.ReleaseID) && release.Outcome == "passed" && release.Migration.Outcome == "complete" && release.Rollouts.Outcome == "healthy" &&
		!release.Rollback.Attempted && !release.Rollback.Succeeded && !release.StartedAt.IsZero() && !release.CompletedAt.Before(release.StartedAt) && digestPattern.MatchString(digest)
}

func validateDrills(drills []Drill, earliest, generated time.Time) ([]Drill, int, int, int, int, int64, int64, error) {
	if len(drills) != len(requiredDrills) {
		return nil, 0, 0, 0, 0, 0, 0, errors.New("identity-safety drills are incomplete")
	}
	byID := make(map[DrillID]Drill, len(drills))
	for _, drill := range drills {
		if _, duplicate := byID[drill.ID]; duplicate {
			return nil, 0, 0, 0, 0, 0, 0, errors.New("identity-safety drill is duplicated")
		}
		byID[drill.ID] = drill
	}
	ordered := make([]Drill, 0, len(requiredDrills))
	passed, failed, inconclusive, breaches := 0, 0, 0, 0
	var maxRTO, maxTarget int64
	for _, id := range requiredDrills {
		drill, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, 0, 0, 0, errors.New("identity-safety required drill is missing")
		}
		if err := deriveTimeline(&drill, earliest, generated); err != nil {
			return nil, 0, 0, 0, 0, 0, 0, err
		}
		checks, p, f, i, err := validateChecks(id, drill.Checks)
		if err != nil {
			return nil, 0, 0, 0, 0, 0, 0, err
		}
		drill.Checks = checks
		passed, failed, inconclusive = passed+p, failed+f, inconclusive+i
		if drill.RTOSeconds > drill.RTOTargetSeconds {
			breaches++
		}
		if drill.RTOSeconds > maxRTO {
			maxRTO = drill.RTOSeconds
		}
		if drill.RTOTargetSeconds > maxTarget {
			maxTarget = drill.RTOTargetSeconds
		}
		ordered = append(ordered, drill)
	}
	return ordered, passed, failed, inconclusive, breaches, maxRTO, maxTarget, nil
}

func deriveTimeline(drill *Drill, earliest, generated time.Time) error {
	drill.ImpairmentAt, drill.DetectedAt, drill.AlertedAt = drill.ImpairmentAt.UTC(), drill.DetectedAt.UTC(), drill.AlertedAt.UTC()
	drill.ContainedAt, drill.RecoveredAt = drill.ContainedAt.UTC(), drill.RecoveredAt.UTC()
	if drill.ImpairmentAt.IsZero() || drill.ImpairmentAt.Before(earliest) || drill.DetectedAt.Before(drill.ImpairmentAt) || drill.AlertedAt.Before(drill.DetectedAt) ||
		drill.ContainedAt.Before(drill.AlertedAt) || drill.RecoveredAt.Before(drill.ContainedAt) || drill.RecoveredAt.After(generated) ||
		drill.RecoveredAt.Sub(drill.ImpairmentAt) > maximumDrillDuration || drill.RTOTargetSeconds <= 0 ||
		time.Duration(drill.RTOTargetSeconds)*time.Second > maximumRTOTarget || !digestPattern.MatchString(drill.TargetApprovalSHA256) ||
		drill.DetectionSeconds != 0 || drill.AlertSeconds != 0 || drill.ContainmentSeconds != 0 || drill.RTOSeconds != 0 {
		return errors.New("identity-safety drill timeline or target is invalid")
	}
	drill.DetectionSeconds = int64(drill.DetectedAt.Sub(drill.ImpairmentAt) / time.Second)
	drill.AlertSeconds = int64(drill.AlertedAt.Sub(drill.ImpairmentAt) / time.Second)
	drill.ContainmentSeconds = int64(drill.ContainedAt.Sub(drill.ImpairmentAt) / time.Second)
	drill.RTOSeconds = int64(drill.RecoveredAt.Sub(drill.ImpairmentAt) / time.Second)
	return nil
}

func validateChecks(drill DrillID, checks []Check) ([]Check, int, int, int, error) {
	required := requiredChecks[drill]
	if len(checks) != len(required) {
		return nil, 0, 0, 0, errors.New("identity-safety checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	passed, failed, inconclusive := 0, 0, 0
	for _, check := range checks {
		if !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("identity-safety check evidence digest is invalid")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("identity-safety check outcome is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("identity-safety check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(required))
	for _, id := range required {
		check, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("identity-safety required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func totalRequiredChecks() int {
	total := 0
	for _, id := range requiredDrills {
		total += len(requiredChecks[id])
	}
	return total
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("identity-safety input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("identity-safety input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open identity-safety input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("identity-safety input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read identity-safety input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("identity-safety input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("identity-safety input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("identity-safety input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("identity-safety input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("identity-safety receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("identity-safety receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect identity-safety receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-identity-safety-*")
}
