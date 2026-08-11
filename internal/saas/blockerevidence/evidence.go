// Package blockerevidence normalizes content-free private-beta incident and
// launch-blocker review evidence for CP10-B.
package blockerevidence

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
	InputSchemaV1        = "agent-memory-private-beta-blocker-review-input-v1"
	ReceiptSchemaV1      = "agent-memory-private-beta-blocker-review-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumCollectionAge = 24 * time.Hour
	maximumCount         = 100_000_000
)

type CheckID string
type Outcome string

const (
	CheckFindingExportComplete   CheckID = "finding_export_complete"
	CheckIncidentExportComplete  CheckID = "incident_export_complete"
	CheckClassificationComplete  CheckID = "severity_and_launch_blocker_classification_complete"
	CheckIncidentCommanderReview CheckID = "incident_commander_review_complete"
	CheckProductReview           CheckID = "product_review_complete"
	OutcomePassed                Outcome = "passed"
	OutcomeFailed                Outcome = "failed"
	OutcomeInconclusive          Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{CheckFindingExportComplete, CheckIncidentExportComplete, CheckClassificationComplete, CheckIncidentCommanderReview, CheckProductReview}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                       string    `json:"schema"`
	Classification               string    `json:"classification"`
	Environment                  string    `json:"environment"`
	ReviewID                     string    `json:"review_id"`
	RegisterVersion              string    `json:"register_version"`
	ClassificationPolicyVersion  string    `json:"classification_policy_version"`
	ReviewVersion                string    `json:"review_version"`
	InventoryID                  string    `json:"inventory_id"`
	InventoryReceiptSHA256       string    `json:"inventory_receipt_sha256"`
	PlanID                       string    `json:"plan_id"`
	PlanReceiptSHA256            string    `json:"plan_receipt_sha256"`
	ChangeID                     string    `json:"change_id"`
	ChangeReceiptSHA256          string    `json:"change_receipt_sha256"`
	ReleaseID                    string    `json:"release_id"`
	ReleaseReceiptSHA256         string    `json:"release_receipt_sha256"`
	FindingExportSHA256          string    `json:"finding_export_sha256"`
	IncidentExportSHA256         string    `json:"incident_export_sha256"`
	ClassificationPolicySHA256   string    `json:"classification_policy_sha256"`
	ReviewDecisionSHA256         string    `json:"review_decision_sha256"`
	SnapshotAt                   time.Time `json:"snapshot_at"`
	ReviewedAt                   time.Time `json:"reviewed_at"`
	GeneratedAt                  time.Time `json:"generated_at"`
	OpenFindingCount             int       `json:"open_finding_count"`
	OpenIncidentCount            int       `json:"open_incident_count"`
	SeverityOneIncidentCount     int       `json:"severity_one_incident_count"`
	UnresolvedLaunchBlockerCount int       `json:"unresolved_launch_blocker_count"`
	ReviewedOpenItemCount        int       `json:"reviewed_open_item_count"`
	Ready                        bool      `json:"ready"`
	Checks                       []Check   `json:"checks"`
}

type Receipt struct {
	Input
	Schema                 string    `json:"schema"`
	InputSHA256            string    `json:"input_sha256"`
	CollectedAt            time.Time `json:"collected_at"`
	OpenItemCount          int       `json:"open_item_count"`
	BlockerCount           int       `json:"blocker_count"`
	ReviewCoverageComplete bool      `json:"review_coverage_complete"`
	CheckCount             int       `json:"check_count"`
	PassedCount            int       `json:"passed_count"`
	FailedCount            int       `json:"failed_count"`
	InconclusiveCount      int       `json:"inconclusive_count"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

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
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging ||
		plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready ||
		!allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return Receipt{}, errors.New("blocker-review platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("blocker-review release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		!allOpaque(input.ReviewID, input.RegisterVersion, input.ClassificationPolicyVersion, input.ReviewVersion) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest ||
		!allDigests(input.FindingExportSHA256, input.IncidentExportSHA256, input.ClassificationPolicySHA256, input.ReviewDecisionSHA256, inputDigest) {
		return Receipt{}, errors.New("blocker-review identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("blocker-review collection time is invalid")
	}
	now = now.UTC()
	snapshot, reviewed, generated := input.SnapshotAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if snapshot.IsZero() || snapshot.Before(earliest) || snapshot.Before(now.Add(-maximumCollectionAge)) || reviewed.Before(snapshot) || generated.Before(reviewed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("blocker-review timeline is invalid")
	}
	counts := []int{input.OpenFindingCount, input.OpenIncidentCount, input.SeverityOneIncidentCount, input.UnresolvedLaunchBlockerCount, input.ReviewedOpenItemCount}
	for _, count := range counts {
		if count < 0 || count > maximumCount {
			return Receipt{}, errors.New("blocker-review count is invalid")
		}
	}
	if input.SeverityOneIncidentCount > input.OpenIncidentCount || input.UnresolvedLaunchBlockerCount > input.OpenFindingCount+input.OpenIncidentCount {
		return Receipt{}, errors.New("blocker-review aggregate count is contradictory")
	}
	openItems := input.OpenFindingCount + input.OpenIncidentCount
	if openItems > maximumCount || input.ReviewedOpenItemCount > openItems {
		return Receipt{}, errors.New("blocker-review coverage count is invalid")
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	blockers := input.SeverityOneIncidentCount + input.UnresolvedLaunchBlockerCount
	coverageComplete := input.ReviewedOpenItemCount == openItems
	classification := outcomeFor(checks, CheckClassificationComplete)
	incidentReview := outcomeFor(checks, CheckIncidentCommanderReview)
	productReview := outcomeFor(checks, CheckProductReview)
	if blockers > 0 && classification != OutcomeFailed || !coverageComplete && incidentReview == OutcomePassed && productReview == OutcomePassed {
		return Receipt{}, errors.New("blocker-review outcome contradicts aggregate observation")
	}
	ready := passed == len(requiredChecks) && failed == 0 && inconclusive == 0 && blockers == 0 && coverageComplete
	if input.Ready != ready {
		return Receipt{}, errors.New("blocker-review readiness contradicts evidence")
	}
	input.Schema = ReceiptSchemaV1
	input.SnapshotAt, input.ReviewedAt, input.GeneratedAt, input.Checks = snapshot, reviewed, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, OpenItemCount: openItems, BlockerCount: blockers, ReviewCoverageComplete: coverageComplete, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("blocker-review checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(input))
	for _, check := range input {
		if _, duplicate := byID[check.ID]; duplicate || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("blocker-review check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("blocker-review check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("blocker-review outcome is invalid")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, failed, inconclusive, nil
}

func outcomeFor(checks []Check, id CheckID) Outcome {
	for _, check := range checks {
		if check.ID == id {
			return check.Outcome
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
		return "", errors.New("blocker-review input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("blocker-review input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open blocker-review input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("blocker-review input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read blocker-review input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("blocker-review input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("blocker-review input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("blocker-review input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("blocker-review input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("blocker-review receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("blocker-review receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect blocker-review receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-blocker-review-*")
}
