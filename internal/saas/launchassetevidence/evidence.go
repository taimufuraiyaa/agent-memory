package launchassetevidence

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
	InputSchemaV1        = "agent-memory-production-launch-assets-input-v1"
	ReceiptSchemaV1      = "agent-memory-production-launch-assets-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumProbeAge      = 15 * time.Minute
	maximumCollectionAge = 24 * time.Hour
	maximumCount         = 1_000_000
)

type AssetID string
type CheckID string
type Outcome string

const (
	AssetExternalSignup      AssetID = "external_signup"
	AssetTermsOfService      AssetID = "terms_of_service"
	AssetPrivacyNotice       AssetID = "privacy_notice"
	AssetContentRightsPolicy AssetID = "content_rights_policy"
	AssetStatusPage          AssetID = "status_page"
	AssetSupportPolicy       AssetID = "support_policy"
	AssetSecurityContact     AssetID = "security_contact"
	CheckManifestComplete    CheckID = "launch_asset_manifest_complete"
	CheckCopyReview          CheckID = "immutable_copy_review_complete"
	CheckLiveProbeCoverage   CheckID = "live_probe_coverage_complete"
	CheckOwnership           CheckID = "asset_ownership_complete"
	CheckMonitoring          CheckID = "monitoring_routes_tested"
	CheckProductReview       CheckID = "product_review_complete"
	CheckCounselReview       CheckID = "counsel_review_complete"
	CheckSupportReview       CheckID = "support_review_complete"
	CheckSecurityReview      CheckID = "security_review_complete"
	OutcomePassed            Outcome = "passed"
	OutcomeFailed            Outcome = "failed"
	OutcomeInconclusive      Outcome = "inconclusive"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredAssets = []AssetID{AssetExternalSignup, AssetTermsOfService, AssetPrivacyNotice, AssetContentRightsPolicy, AssetStatusPage, AssetSupportPolicy, AssetSecurityContact}
	requiredChecks = []CheckID{CheckManifestComplete, CheckCopyReview, CheckLiveProbeCoverage, CheckOwnership, CheckMonitoring, CheckProductReview, CheckCounselReview, CheckSupportReview, CheckSecurityReview}
	owners         = map[AssetID]string{AssetExternalSignup: "product", AssetTermsOfService: "product_counsel", AssetPrivacyNotice: "product_counsel", AssetContentRightsPolicy: "product_counsel", AssetStatusPage: "operations", AssetSupportPolicy: "support_operations", AssetSecurityContact: "security"}
)

type Asset struct {
	ID                     AssetID   `json:"id"`
	OwnerGroup             string    `json:"owner_group"`
	PublicURLSHA256        string    `json:"public_url_sha256"`
	RenderedCopySHA256     string    `json:"rendered_copy_sha256"`
	MonitoringConfigSHA256 string    `json:"monitoring_config_sha256"`
	RouteTestSHA256        string    `json:"route_test_sha256"`
	OwnerDecisionSHA256    string    `json:"owner_decision_sha256"`
	ObservedAt             time.Time `json:"observed_at"`
	HTTPStatus             int       `json:"http_status"`
	ProbeCount             int       `json:"probe_count"`
	SuccessfulProbeCount   int       `json:"successful_probe_count"`
}
type AssetResult struct {
	Asset
	ProbeAgeSeconds int64 `json:"probe_age_seconds"`
	Live            bool  `json:"live"`
}
type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}
type Input struct {
	Schema                  string    `json:"schema"`
	Classification          string    `json:"classification"`
	Environment             string    `json:"environment"`
	ReviewID                string    `json:"review_id"`
	ManifestVersion         string    `json:"manifest_version"`
	ProbeVersion            string    `json:"probe_version"`
	CopyReviewVersion       string    `json:"copy_review_version"`
	MonitoringReviewVersion string    `json:"monitoring_review_version"`
	InventoryID             string    `json:"inventory_id"`
	InventoryReceiptSHA256  string    `json:"inventory_receipt_sha256"`
	PlanID                  string    `json:"plan_id"`
	PlanReceiptSHA256       string    `json:"plan_receipt_sha256"`
	ChangeID                string    `json:"change_id"`
	ChangeReceiptSHA256     string    `json:"change_receipt_sha256"`
	ReleaseID               string    `json:"release_id"`
	ReleaseReceiptSHA256    string    `json:"release_receipt_sha256"`
	ManifestSHA256          string    `json:"manifest_sha256"`
	AccountableReviewSHA256 string    `json:"accountable_review_sha256"`
	SnapshotAt              time.Time `json:"snapshot_at"`
	ReviewedAt              time.Time `json:"reviewed_at"`
	GeneratedAt             time.Time `json:"generated_at"`
	Ready                   bool      `json:"ready"`
	Assets                  []Asset   `json:"assets"`
	Checks                  []Check   `json:"checks"`
}
type Receipt struct {
	Input
	Schema            string        `json:"schema"`
	InputSHA256       string        `json:"input_sha256"`
	CollectedAt       time.Time     `json:"collected_at"`
	AssetCount        int           `json:"asset_count"`
	LiveAssetCount    int           `json:"live_asset_count"`
	StaleAssetCount   int           `json:"stale_asset_count"`
	AssetResults      []AssetResult `json:"asset_results"`
	CheckCount        int           `json:"check_count"`
	PassedCount       int           `json:"passed_count"`
	FailedCount       int           `json:"failed_count"`
	InconclusiveCount int           `json:"inconclusive_count"`
}

func RequiredAssets() []AssetID  { return append([]AssetID(nil), requiredAssets...) }
func RequiredChecks() []CheckID  { return append([]CheckID(nil), requiredChecks...) }
func OwnerFor(id AssetID) string { return owners[id] }

func LoadReady(path string) (Receipt, string, error) {
	var receipt Receipt
	digest, err := decodeStrictRegular(path, &receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	if receipt.Schema != ReceiptSchemaV1 || receipt.Classification != "production_external" || receipt.Environment != "production" || !receipt.Ready || receipt.AssetCount != len(requiredAssets) || receipt.LiveAssetCount != len(requiredAssets) || receipt.StaleAssetCount != 0 || receipt.CheckCount != len(requiredChecks) || receipt.PassedCount != len(requiredChecks) || receipt.FailedCount != 0 || receipt.InconclusiveCount != 0 {
		return Receipt{}, "", errors.New("launch assets receipt is not ready")
	}
	if !allOpaque(receipt.ReviewID, receipt.ManifestVersion, receipt.ProbeVersion, receipt.CopyReviewVersion, receipt.MonitoringReviewVersion, receipt.InventoryID, receipt.PlanID, receipt.ChangeID, receipt.ReleaseID) || !allDigests(receipt.InventoryReceiptSHA256, receipt.PlanReceiptSHA256, receipt.ChangeReceiptSHA256, receipt.ReleaseReceiptSHA256, receipt.ManifestSHA256, receipt.AccountableReviewSHA256, receipt.InputSHA256, digest) {
		return Receipt{}, "", errors.New("launch assets receipt identity or binding is invalid")
	}
	results, live, stale, err := evaluateAssets(receipt.Assets, receipt.SnapshotAt.UTC())
	if err != nil || live != len(requiredAssets) || stale != 0 || len(results) != len(receipt.AssetResults) {
		return Receipt{}, "", errors.New("launch assets receipt observations are invalid")
	}
	for index := range results {
		if receipt.AssetResults[index] != results[index] {
			return Receipt{}, "", errors.New("launch assets receipt derivation is invalid")
		}
	}
	checks, passed, failed, inconclusive, err := validateChecks(receipt.Checks)
	if err != nil || passed != len(requiredChecks) || failed != 0 || inconclusive != 0 {
		return Receipt{}, "", errors.New("launch assets receipt checks are invalid")
	}
	if receipt.ReviewedAt.Before(receipt.SnapshotAt) || receipt.GeneratedAt.Before(receipt.ReviewedAt) || receipt.CollectedAt.Before(receipt.GeneratedAt) || receipt.CollectedAt.IsZero() {
		return Receipt{}, "", errors.New("launch assets receipt timeline is invalid")
	}
	receipt.AssetResults, receipt.Checks = results, checks
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
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Production || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !allDigests(inventory.ReceiptSHA256, plan.ReceiptSHA256, change.ReceiptSHA256) {
		return Receipt{}, errors.New("launch assets production platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "production" || release.Namespace != "agent-memory-production" || release.Outcome != "passed" || release.Migration.Outcome != "complete" || release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("launch assets production release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ReviewID, input.ManifestVersion, input.ProbeVersion, input.CopyReviewVersion, input.MonitoringReviewVersion) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !allDigests(input.ManifestSHA256, input.AccountableReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("launch assets identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("launch assets collection time is invalid")
	}
	now = now.UTC()
	snapshot, reviewed, generated := input.SnapshotAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if snapshot.Before(release.CompletedAt.UTC()) || reviewed.Before(snapshot) || reviewed.Sub(snapshot) > maximumCollectionAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumCollectionAge)) || generated.After(now) {
		return Receipt{}, errors.New("launch assets timeline is invalid")
	}
	results, live, stale, err := evaluateAssets(input.Assets, snapshot)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	if live != len(requiredAssets) && outcomeFor(checks, CheckLiveProbeCoverage) != OutcomeFailed {
		return Receipt{}, errors.New("launch assets live check contradicts observations")
	}
	ready := live == len(requiredAssets) && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("launch assets readiness contradicts evidence")
	}
	ordered := make([]Asset, 0, len(results))
	for _, result := range results {
		ordered = append(ordered, result.Asset)
	}
	input.Schema = ReceiptSchemaV1
	input.SnapshotAt, input.ReviewedAt, input.GeneratedAt, input.Assets, input.Checks = snapshot, reviewed, generated, ordered, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, CollectedAt: now, AssetCount: len(results), LiveAssetCount: live, StaleAssetCount: stale, AssetResults: results, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func evaluateAssets(input []Asset, snapshot time.Time) ([]AssetResult, int, int, error) {
	if len(input) != len(requiredAssets) {
		return nil, 0, 0, errors.New("launch assets are incomplete")
	}
	byID := map[AssetID]Asset{}
	for _, asset := range input {
		if _, dup := byID[asset.ID]; dup || owners[asset.ID] == "" || asset.OwnerGroup != owners[asset.ID] || !allDigests(asset.PublicURLSHA256, asset.RenderedCopySHA256, asset.MonitoringConfigSHA256, asset.RouteTestSHA256, asset.OwnerDecisionSHA256) || asset.HTTPStatus < 0 || asset.HTTPStatus > 599 || asset.ProbeCount <= 0 || asset.ProbeCount > maximumCount || asset.SuccessfulProbeCount < 0 || asset.SuccessfulProbeCount > asset.ProbeCount || asset.ObservedAt.IsZero() || asset.ObservedAt.After(snapshot) {
			return nil, 0, 0, errors.New("launch asset is invalid or duplicated")
		}
		byID[asset.ID] = asset
	}
	results := make([]AssetResult, 0, len(requiredAssets))
	live, stale := 0, 0
	for _, id := range requiredAssets {
		asset, ok := byID[id]
		if !ok {
			return nil, 0, 0, errors.New("launch asset is missing")
		}
		age := snapshot.Sub(asset.ObservedAt.UTC())
		isStale := age > maximumProbeAge
		if isStale {
			stale++
		}
		isLive := asset.HTTPStatus == 200 && asset.SuccessfulProbeCount == asset.ProbeCount && !isStale
		if isLive {
			live++
		}
		asset.ObservedAt = asset.ObservedAt.UTC()
		results = append(results, AssetResult{Asset: asset, ProbeAgeSeconds: int64(age / time.Second), Live: isLive})
	}
	return results, live, stale, nil
}
func validateChecks(input []Check) ([]Check, int, int, int, error) {
	if len(input) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("launch asset checks are incomplete")
	}
	byID := map[CheckID]Check{}
	for _, check := range input {
		if _, dup := byID[check.ID]; dup || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("launch asset check is invalid or duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range requiredChecks {
		check, ok := byID[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("launch asset check is missing")
		}
		switch check.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("launch asset check outcome is invalid")
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
func allDigests(values ...string) bool {
	for _, value := range values {
		if !digestPattern.MatchString(value) {
			return false
		}
	}
	return true
}
func allOpaque(values ...string) bool {
	for _, value := range values {
		if !opaquePattern.MatchString(value) || strings.Contains(value, "@") {
			return false
		}
	}
	return true
}
func decodeStrictRegular(path string, destination any) (string, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("launch assets input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open launch assets input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("launch assets input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return "", errors.New("read launch assets input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("launch assets input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("launch assets input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("launch assets input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("launch assets input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("launch assets receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("launch assets receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect launch assets receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-launch-assets-*")
}
