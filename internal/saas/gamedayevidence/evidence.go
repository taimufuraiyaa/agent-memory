// Package gamedayevidence normalizes release-bound, content-free P10.3-A
// operational game-day evidence without injecting faults or approving launch.
package gamedayevidence

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
	InputSchemaV1        = "agent-memory-staging-game-day-input-v1"
	ReceiptSchemaV1      = "agent-memory-staging-game-day-receipt-v1"
	maximumInputBytes    = 256 << 10
	maximumCollectionAge = 24 * time.Hour
	maximumDrillDuration = 6 * time.Hour
	maximumTargetSeconds = 86_400
)

type DrillID string
type CheckID string
type Outcome string

const (
	DrillDatabaseFailover   DrillID = "database_failover"
	DrillQueueBacklog       DrillID = "queue_backlog"
	DrillComponentFailure   DrillID = "self_managed_component_failure"
	DrillIntegrationOutage  DrillID = "external_integration_outage"
	DrillCredentialLeak     DrillID = "credential_leak"
	DrillIsolationAttempt   DrillID = "cross_tenant_attempt"
	DrillIncompleteDeletion DrillID = "incomplete_deletion"

	CheckFailureObserved       CheckID = "failure_injection_or_observation_verified"
	CheckAlertDelivered        CheckID = "installed_alert_delivery_verified"
	CheckResponderAcknowledged CheckID = "accountable_responder_acknowledged"
	CheckContainment           CheckID = "containment_verified"
	CheckServiceRecovered      CheckID = "service_recovery_verified"
	CheckImmutableAudit        CheckID = "immutable_audit_retained"
	CheckCommittedState        CheckID = "committed_state_and_rpo_preserved"
	CheckQueueIdempotency      CheckID = "queue_replay_idempotency_verified"
	CheckComponentRecovery     CheckID = "component_failover_or_replacement_verified"
	CheckIntegrationContinuity CheckID = "integration_degradation_or_disabled_state_verified"
	CheckCredentialRevocation  CheckID = "credential_revoked_and_post_revoke_denied"
	CheckIsolationSafety       CheckID = "cross_tenant_existence_and_content_signal_absent"
	CheckDeletionSafety        CheckID = "deletion_revocation_and_retry_visibility_verified"

	BundlePlatformBinding    CheckID = "platform_release_binding_verified"
	BundleScenarioCoverage   CheckID = "required_scenario_coverage_complete"
	BundleComponentCoverage  CheckID = "component_subject_coverage_complete"
	BundleIntegrationStates  CheckID = "integration_state_reconciliation_complete"
	BundleAlertResponse      CheckID = "alert_and_response_checks_passed"
	BundleApprovedTargets    CheckID = "approved_recovery_targets_met"
	BundleImmutableArtifacts CheckID = "immutable_drill_artifacts_complete"
	BundleAccountableReview  CheckID = "operations_security_review_complete"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	requiredDrills       = []DrillID{DrillDatabaseFailover, DrillQueueBacklog, DrillComponentFailure, DrillIntegrationOutage, DrillCredentialLeak, DrillIsolationAttempt, DrillIncompleteDeletion}
	requiredBundleChecks = []CheckID{BundlePlatformBinding, BundleScenarioCoverage, BundleComponentCoverage, BundleIntegrationStates, BundleAlertResponse, BundleApprovedTargets, BundleImmutableArtifacts, BundleAccountableReview}
	safetyChecks         = map[DrillID]CheckID{DrillDatabaseFailover: CheckCommittedState, DrillQueueBacklog: CheckQueueIdempotency, DrillComponentFailure: CheckComponentRecovery, DrillIntegrationOutage: CheckIntegrationContinuity, DrillCredentialLeak: CheckCredentialRevocation, DrillIsolationAttempt: CheckIsolationSafety, DrillIncompleteDeletion: CheckDeletionSafety}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type InputDrill struct {
	ID                         DrillID   `json:"id"`
	DrillID                    string    `json:"drill_id"`
	TargetPolicyVersion        string    `json:"target_policy_version"`
	TargetSeconds              int       `json:"target_seconds"`
	TargetDecisionSHA256       string    `json:"target_decision_sha256"`
	ReportSHA256               string    `json:"report_sha256"`
	EvidenceManifestSHA256     string    `json:"evidence_manifest_sha256"`
	StartedAt                  time.Time `json:"started_at"`
	DetectedAt                 time.Time `json:"detected_at"`
	AlertedAt                  time.Time `json:"alerted_at"`
	AcknowledgedAt             time.Time `json:"acknowledged_at"`
	ContainedAt                time.Time `json:"contained_at"`
	RecoveredAt                time.Time `json:"recovered_at"`
	CompletedAt                time.Time `json:"completed_at"`
	SubjectCount               int       `json:"subject_count"`
	EnabledSubjectCount        int       `json:"enabled_subject_count"`
	ExercisedSubjectCount      int       `json:"exercised_subject_count"`
	DisabledStateVerifiedCount int       `json:"disabled_state_verified_count"`
	Checks                     []Check   `json:"checks"`
}

type ReceiptDrill struct {
	InputDrill
	DetectionSeconds       int     `json:"detection_seconds"`
	AlertSeconds           int     `json:"alert_seconds"`
	AcknowledgementSeconds int     `json:"acknowledgement_seconds"`
	ContainmentSeconds     int     `json:"containment_seconds"`
	RecoverySeconds        int     `json:"recovery_seconds"`
	ElapsedSeconds         int     `json:"elapsed_seconds"`
	TargetMet              bool    `json:"target_met"`
	Outcome                Outcome `json:"outcome"`
}

type Input struct {
	Schema                         string       `json:"schema"`
	Classification                 string       `json:"classification"`
	Environment                    string       `json:"environment"`
	BundleID                       string       `json:"bundle_id"`
	ReviewVersion                  string       `json:"review_version"`
	InventoryID                    string       `json:"inventory_id"`
	InventoryReceiptSHA256         string       `json:"inventory_receipt_sha256"`
	PlanID                         string       `json:"plan_id"`
	PlanReceiptSHA256              string       `json:"plan_receipt_sha256"`
	ChangeID                       string       `json:"change_id"`
	ChangeReceiptSHA256            string       `json:"change_receipt_sha256"`
	ReleaseID                      string       `json:"release_id"`
	ReleaseReceiptSHA256           string       `json:"release_receipt_sha256"`
	OperationsSecurityReviewSHA256 string       `json:"operations_security_review_sha256"`
	GeneratedAt                    time.Time    `json:"generated_at"`
	Ready                          bool         `json:"ready"`
	Drills                         []InputDrill `json:"drills"`
	BundleChecks                   []Check      `json:"bundle_checks"`
}

type Receipt struct {
	Schema                         string         `json:"schema"`
	Classification                 string         `json:"classification"`
	Environment                    string         `json:"environment"`
	BundleID                       string         `json:"bundle_id"`
	ReviewVersion                  string         `json:"review_version"`
	InventoryID                    string         `json:"inventory_id"`
	InventoryReceiptSHA256         string         `json:"inventory_receipt_sha256"`
	PlanID                         string         `json:"plan_id"`
	PlanReceiptSHA256              string         `json:"plan_receipt_sha256"`
	ChangeID                       string         `json:"change_id"`
	ChangeReceiptSHA256            string         `json:"change_receipt_sha256"`
	ReleaseID                      string         `json:"release_id"`
	ReleaseReceiptSHA256           string         `json:"release_receipt_sha256"`
	OperationsSecurityReviewSHA256 string         `json:"operations_security_review_sha256"`
	InputSHA256                    string         `json:"input_sha256"`
	GeneratedAt                    time.Time      `json:"generated_at"`
	CollectedAt                    time.Time      `json:"collected_at"`
	Ready                          bool           `json:"ready"`
	DrillCount                     int            `json:"drill_count"`
	ComponentSubjectCount          int            `json:"component_subject_count"`
	IntegrationSubjectCount        int            `json:"integration_subject_count"`
	EnabledIntegrationCount        int            `json:"enabled_integration_count"`
	TargetBreachCount              int            `json:"target_breach_count"`
	CheckCount                     int            `json:"check_count"`
	PassedCount                    int            `json:"passed_count"`
	FailedCount                    int            `json:"failed_count"`
	InconclusiveCount              int            `json:"inconclusive_count"`
	BundleCheckCount               int            `json:"bundle_check_count"`
	BundlePassedCount              int            `json:"bundle_passed_count"`
	BundleFailedCount              int            `json:"bundle_failed_count"`
	BundleInconclusiveCount        int            `json:"bundle_inconclusive_count"`
	Drills                         []ReceiptDrill `json:"drills"`
	BundleChecks                   []Check        `json:"bundle_checks"`
}

func RequiredDrills() []DrillID       { return append([]DrillID(nil), requiredDrills...) }
func RequiredBundleChecks() []CheckID { return append([]CheckID(nil), requiredBundleChecks...) }
func RequiredChecks(id DrillID) []CheckID {
	return []CheckID{CheckFailureObserved, CheckAlertDelivered, CheckResponderAcknowledged, CheckContainment, CheckServiceRecovered, CheckImmutableAudit, safetyChecks[id]}
}

func Collect(inventoryPath, planPath, changePath, releasePath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform inventory: %w", err)
	}
	plan, err := platformplan.Load(planPath, inventory)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform plan: %w", err)
	}
	change, err := platformchange.Load(changePath, inventory, plan)
	if err != nil {
		return Receipt{}, fmt.Errorf("load platform change: %w", err)
	}
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load staging release: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, plan, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, plan platformplan.Plan, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded {
		return Receipt{}, errors.New("game-day platform or release chain is invalid")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !allOpaque(input.BundleID, input.ReviewVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256, releaseDigest, input.OperationsSecurityReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("game-day input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("game-day collection time is invalid")
	}
	now = now.UTC()
	generated := input.GeneratedAt.UTC()
	if generated.IsZero() || generated.Before(release.CompletedAt.UTC()) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("game-day bundle timeline is invalid")
	}
	enabled := 0
	for _, v := range inventory.ExternalIntegrations {
		if v.Enabled {
			enabled++
		}
	}
	drills, p, f, i, breaches, err := validateDrills(input.Drills, len(inventory.Components), len(inventory.ExternalIntegrations), enabled, release.CompletedAt.UTC(), generated)
	if err != nil {
		return Receipt{}, err
	}
	bundle, bp, bf, bi, err := validateChecks(input.BundleChecks, requiredBundleChecks)
	if err != nil {
		return Receipt{}, err
	}
	if evidenceFor(bundle, BundleAccountableReview) != input.OperationsSecurityReviewSHA256 {
		return Receipt{}, errors.New("game-day operations and security review binding is invalid")
	}
	alertOutcome := aggregateSelected(drills, []CheckID{CheckFailureObserved, CheckAlertDelivered, CheckResponderAcknowledged, CheckContainment, CheckServiceRecovered})
	artifactOutcome := aggregateSelected(drills, []CheckID{CheckImmutableAudit})
	targetOutcome := OutcomePassed
	if breaches > 0 {
		targetOutcome = OutcomeFailed
	}
	derived := map[CheckID]Outcome{BundlePlatformBinding: OutcomePassed, BundleScenarioCoverage: OutcomePassed, BundleComponentCoverage: OutcomePassed, BundleIntegrationStates: OutcomePassed, BundleAlertResponse: alertOutcome, BundleApprovedTargets: targetOutcome, BundleImmutableArtifacts: artifactOutcome}
	for id, outcome := range derived {
		if outcomeFor(bundle, id) != outcome {
			return Receipt{}, errors.New("game-day bundle check contradicts evidence")
		}
	}
	ready := p == len(requiredDrills)*7 && f == 0 && i == 0 && breaches == 0 && bp == len(requiredBundleChecks) && bf == 0 && bi == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("game-day readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, BundleID: input.BundleID, ReviewVersion: input.ReviewVersion, InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256, ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest, OperationsSecurityReviewSHA256: input.OperationsSecurityReviewSHA256, InputSHA256: inputDigest, GeneratedAt: generated, CollectedAt: now, Ready: ready, DrillCount: len(drills), ComponentSubjectCount: len(inventory.Components), IntegrationSubjectCount: len(inventory.ExternalIntegrations), EnabledIntegrationCount: enabled, TargetBreachCount: breaches, CheckCount: p + f + i, PassedCount: p, FailedCount: f, InconclusiveCount: i, BundleCheckCount: bp + bf + bi, BundlePassedCount: bp, BundleFailedCount: bf, BundleInconclusiveCount: bi, Drills: drills, BundleChecks: bundle}, nil
}

func evidenceFor(checks []Check, id CheckID) string {
	for _, check := range checks {
		if check.ID == id {
			return check.EvidenceSHA256
		}
	}
	return ""
}

func validateDrills(values []InputDrill, components, integrations, enabled int, earliest, generated time.Time) ([]ReceiptDrill, int, int, int, int, error) {
	if len(values) != len(requiredDrills) {
		return nil, 0, 0, 0, 0, errors.New("game-day scenario coverage is incomplete")
	}
	by := map[DrillID]InputDrill{}
	for _, v := range values {
		if _, ok := by[v.ID]; ok {
			return nil, 0, 0, 0, 0, errors.New("game-day scenario is duplicated")
		}
		by[v.ID] = v
	}
	ordered := make([]ReceiptDrill, 0, len(requiredDrills))
	passed, failed, inconclusive, breaches := 0, 0, 0, 0
	for _, id := range requiredDrills {
		v, ok := by[id]
		if !ok {
			return nil, 0, 0, 0, 0, errors.New("required game-day scenario is missing")
		}
		if !allOpaque(v.DrillID, v.TargetPolicyVersion) || v.TargetSeconds < 1 || v.TargetSeconds > maximumTargetSeconds || !allDigests(v.TargetDecisionSHA256, v.ReportSHA256, v.EvidenceManifestSHA256) {
			return nil, 0, 0, 0, 0, errors.New("game-day drill identity or artifact binding is invalid")
		}
		if err := validateSubjectCounts(v, id, components, integrations, enabled); err != nil {
			return nil, 0, 0, 0, 0, err
		}
		times := []time.Time{v.StartedAt.UTC(), v.DetectedAt.UTC(), v.AlertedAt.UTC(), v.AcknowledgedAt.UTC(), v.ContainedAt.UTC(), v.RecoveredAt.UTC(), v.CompletedAt.UTC()}
		for n, t := range times {
			if t.IsZero() || (n > 0 && t.Before(times[n-1])) {
				return nil, 0, 0, 0, 0, errors.New("game-day drill chronology is invalid")
			}
		}
		if times[0].Before(earliest) || times[6].After(generated) || times[6].Sub(times[0]) > maximumDrillDuration {
			return nil, 0, 0, 0, 0, errors.New("game-day drill window is invalid")
		}
		checks, cp, cf, ci, err := validateChecks(v.Checks, RequiredChecks(id))
		if err != nil {
			return nil, 0, 0, 0, 0, err
		}
		v.StartedAt, v.DetectedAt, v.AlertedAt, v.AcknowledgedAt, v.ContainedAt, v.RecoveredAt, v.CompletedAt = times[0], times[1], times[2], times[3], times[4], times[5], times[6]
		v.Checks = checks
		elapsed := seconds(times[6].Sub(times[0]))
		targetMet := elapsed <= v.TargetSeconds
		if !targetMet {
			breaches++
		}
		outcome := aggregateOutcome(cp, cf, ci, len(checks))
		ordered = append(ordered, ReceiptDrill{InputDrill: v, DetectionSeconds: seconds(times[1].Sub(times[0])), AlertSeconds: seconds(times[2].Sub(times[1])), AcknowledgementSeconds: seconds(times[3].Sub(times[2])), ContainmentSeconds: seconds(times[4].Sub(times[3])), RecoverySeconds: seconds(times[5].Sub(times[4])), ElapsedSeconds: elapsed, TargetMet: targetMet, Outcome: outcome})
		passed += cp
		failed += cf
		inconclusive += ci
	}
	return ordered, passed, failed, inconclusive, breaches, nil
}

func validateSubjectCounts(v InputDrill, id DrillID, components, integrations, enabled int) error {
	expectedSubjects, expectedEnabled, expectedExercised, expectedDisabled := 1, 0, 1, 0
	switch id {
	case DrillComponentFailure:
		expectedSubjects, expectedExercised = components, components
	case DrillIntegrationOutage:
		expectedSubjects, expectedEnabled, expectedExercised, expectedDisabled = integrations, enabled, enabled, integrations-enabled
	case DrillIsolationAttempt, DrillIncompleteDeletion:
		expectedSubjects, expectedExercised = 2, 2
	}
	if v.SubjectCount != expectedSubjects || v.EnabledSubjectCount != expectedEnabled || v.ExercisedSubjectCount != expectedExercised || v.DisabledStateVerifiedCount != expectedDisabled {
		return errors.New("game-day subject coverage contradicts inventory")
	}
	return nil
}
func validateChecks(values []Check, required []CheckID) ([]Check, int, int, int, error) {
	if len(values) != len(required) {
		return nil, 0, 0, 0, errors.New("game-day checks are incomplete")
	}
	by := map[CheckID]Check{}
	p, f, i := 0, 0, 0
	for _, v := range values {
		if !allDigests(v.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("game-day check digest is invalid")
		}
		if _, ok := by[v.ID]; ok {
			return nil, 0, 0, 0, errors.New("game-day check is duplicated")
		}
		switch v.Outcome {
		case OutcomePassed:
			p++
		case OutcomeFailed:
			f++
		case OutcomeInconclusive:
			i++
		default:
			return nil, 0, 0, 0, errors.New("game-day check outcome is invalid")
		}
		by[v.ID] = v
	}
	ordered := make([]Check, 0, len(required))
	for _, id := range required {
		v, ok := by[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("required game-day check is missing")
		}
		ordered = append(ordered, v)
	}
	return ordered, p, f, i, nil
}
func aggregateSelected(drills []ReceiptDrill, ids []CheckID) Outcome {
	failed, inconclusive := false, false
	wanted := map[CheckID]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	for _, d := range drills {
		for _, c := range d.Checks {
			if !wanted[c.ID] {
				continue
			}
			if c.Outcome == OutcomeFailed {
				failed = true
			}
			if c.Outcome == OutcomeInconclusive {
				inconclusive = true
			}
		}
	}
	if failed {
		return OutcomeFailed
	}
	if inconclusive {
		return OutcomeInconclusive
	}
	return OutcomePassed
}
func aggregateOutcome(p, f, i, total int) Outcome {
	if f > 0 {
		return OutcomeFailed
	}
	if i > 0 || p != total {
		return OutcomeInconclusive
	}
	return OutcomePassed
}
func outcomeFor(values []Check, id CheckID) Outcome {
	for _, v := range values {
		if v.ID == id {
			return v.Outcome
		}
	}
	return ""
}
func seconds(v time.Duration) int { return int(v / time.Second) }
func allDigests(values ...string) bool {
	for _, v := range values {
		if !digestPattern.MatchString(v) {
			return false
		}
	}
	return true
}
func allOpaque(values ...string) bool {
	for _, v := range values {
		if !opaquePattern.MatchString(v) {
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
		return "", errors.New("game-day input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("game-day input must be a bounded regular file")
	}
	if afterValidate != nil {
		afterValidate()
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("game-day input changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, maximumInputBytes+1))
	if err != nil || int64(len(b)) != opened.Size() {
		return "", errors.New("read game-day input")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(target); err != nil {
		return "", err
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("game-day input contains trailing data")
	}
	openedAfterRead, err := f.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("game-day input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("game-day input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("game-day receipt path is required")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-game-day-*")
}
