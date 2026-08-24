// Package objectcustody normalizes content-free evidence from a deployed
// staging object-custody review without accessing policies or telemetry.
package objectcustody

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
	InputSchemaV1        = "agent-memory-staging-object-custody-review-v1"
	ReceiptSchemaV1      = "agent-memory-staging-object-custody-receipt-v1"
	maximumInputBytes    = 64 << 10
	maximumReviewSpan    = 8 * time.Hour
	maximumCollectionAge = 24 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckPolicyExport             CheckID = "deployed_object_policy_export_verified"
	CheckAPICapabilities          CheckID = "api_object_capabilities_verified"
	CheckWorkerCapabilities       CheckID = "worker_object_capabilities_verified"
	CheckReconcilerCapabilities   CheckID = "reconciler_object_capabilities_verified"
	CheckVaultImmutability        CheckID = "vault_immutability_verified"
	CheckQuarantinePromotion      CheckID = "quarantine_promotion_and_removal_verified"
	CheckAuditArchiveImmutability CheckID = "audit_archive_immutability_verified"
	CheckLogsContentFree          CheckID = "source_content_absent_from_logs"
	CheckTracesContentFree        CheckID = "source_content_absent_from_traces"
	CheckTelemetryAccess          CheckID = "telemetry_access_restricted"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredChecks = []CheckID{
		CheckPolicyExport,
		CheckAPICapabilities,
		CheckWorkerCapabilities,
		CheckReconcilerCapabilities,
		CheckVaultImmutability,
		CheckQuarantinePromotion,
		CheckAuditArchiveImmutability,
		CheckLogsContentFree,
		CheckTracesContentFree,
		CheckTelemetryAccess,
	}
)

type ReviewWindow struct {
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                 string       `json:"schema"`
	Classification         string       `json:"classification"`
	Environment            string       `json:"environment"`
	ReviewID               string       `json:"review_id"`
	InventoryID            string       `json:"inventory_id"`
	InventoryReceiptSHA256 string       `json:"inventory_receipt_sha256"`
	ChangeID               string       `json:"change_id"`
	ChangeReceiptSHA256    string       `json:"change_receipt_sha256"`
	ReleaseID              string       `json:"release_id"`
	ReleaseReceiptSHA256   string       `json:"release_receipt_sha256"`
	Ready                  bool         `json:"ready"`
	GeneratedAt            time.Time    `json:"generated_at"`
	Review                 ReviewWindow `json:"review"`
	Checks                 []Check      `json:"checks"`
}

type Receipt struct {
	Schema                 string       `json:"schema"`
	Classification         string       `json:"classification"`
	Environment            string       `json:"environment"`
	ReviewID               string       `json:"review_id"`
	InventoryID            string       `json:"inventory_id"`
	InventoryReceiptSHA256 string       `json:"inventory_receipt_sha256"`
	ChangeID               string       `json:"change_id"`
	ChangeReceiptSHA256    string       `json:"change_receipt_sha256"`
	ReleaseID              string       `json:"release_id"`
	ReleaseReceiptSHA256   string       `json:"release_receipt_sha256"`
	InputSHA256            string       `json:"input_sha256"`
	Ready                  bool         `json:"ready"`
	GeneratedAt            time.Time    `json:"generated_at"`
	CollectedAt            time.Time    `json:"collected_at"`
	Review                 ReviewWindow `json:"review"`
	CheckCount             int          `json:"check_count"`
	PassedCount            int          `json:"passed_count"`
	FailedCount            int          `json:"failed_count"`
	Checks                 []Check      `json:"checks"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, planPath, changePath, releasePath, reviewPath string, now time.Time) (Receipt, error) {
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
	inputDigest, err := decodeStrictRegular(reviewPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, change, release, releaseDigest, input, inputDigest, now)
}

func build(inventory platforminventory.Inventory, change platformchange.Receipt, release platformrollback.ReleaseReceipt, releaseDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging ||
		change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment ||
		change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 ||
		!platformchange.Assess(change).Ready || !digestPattern.MatchString(inventory.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("object custody platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" ||
		release.Namespace != "agent-memory-staging" || !opaquePattern.MatchString(release.ReleaseID) ||
		release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" ||
		release.Rollback.Attempted || release.Rollback.Succeeded || release.CompletedAt.Before(release.StartedAt) ||
		!digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("object custody staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		!opaquePattern.MatchString(input.ReviewID) || input.InventoryID != inventory.InventoryID ||
		input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.ChangeID != change.ChangeID ||
		input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID ||
		input.ReleaseReceiptSHA256 != releaseDigest || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("object custody review identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("object custody collection time is invalid")
	}
	now = now.UTC()
	review := ReviewWindow{StartedAt: input.Review.StartedAt.UTC(), CompletedAt: input.Review.CompletedAt.UTC()}
	generated := input.GeneratedAt.UTC()
	if review.StartedAt.IsZero() || review.CompletedAt.IsZero() || generated.IsZero() ||
		review.StartedAt.Before(change.GeneratedAt.UTC()) || review.StartedAt.Before(release.CompletedAt.UTC()) ||
		review.CompletedAt.Before(review.StartedAt) || review.CompletedAt.Sub(review.StartedAt) > maximumReviewSpan ||
		generated.Before(review.CompletedAt) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("object custody review timeline is invalid")
	}
	checks, passed, failed, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	ready := failed == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("object custody readiness contradicts checks")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, ReviewID: input.ReviewID,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256,
		ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256,
		ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: input.ReleaseReceiptSHA256, InputSHA256: inputDigest,
		Ready: ready, GeneratedAt: generated, CollectedAt: now, Review: review,
		CheckCount: len(checks), PassedCount: passed, FailedCount: failed, Checks: checks,
	}, nil
}

func validateChecks(checks []Check) ([]Check, int, int, error) {
	if len(checks) != len(requiredChecks) {
		return nil, 0, 0, errors.New("object custody checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(checks))
	passed := 0
	for _, check := range checks {
		if (check.Outcome != OutcomePassed && check.Outcome != OutcomeFailed) || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, errors.New("object custody check is invalid")
		}
		if _, duplicate := byID[check.ID]; duplicate {
			return nil, 0, 0, errors.New("object custody check is duplicated")
		}
		byID[check.ID] = check
		if check.Outcome == OutcomePassed {
			passed++
		}
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, 0, 0, errors.New("object custody required check is missing")
		}
		ordered = append(ordered, check)
	}
	return ordered, passed, len(checks) - passed, nil
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("object custody review path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("object custody review must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open object custody review")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("object custody review changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read object custody review")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("object custody review JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("object custody review contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("object custody review changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("object custody review changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("object custody receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("object custody receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect object custody receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-object-custody-*")
}
