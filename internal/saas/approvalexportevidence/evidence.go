// Package approvalexportevidence normalizes the signed CP11-C public-beta
// approval-directory export without acquiring application or data-plane access.
package approvalexportevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchassetevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/publicbetagateevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

const (
	ManifestSchemaV1   = "agent-memory-public-beta-approval-export-manifest-v1"
	InputSchemaV1      = "agent-memory-public-beta-approval-export-input-v1"
	ReceiptSchemaV1    = "agent-memory-public-beta-approval-export-receipt-v1"
	maximumInputBytes  = 1 << 20
	maximumFileCount   = 256
	maximumEvidenceAge = 24 * time.Hour
)

type CheckID string
type Outcome string

const (
	CheckPrerequisites   CheckID = "prerequisite_receipts_ready"
	CheckCommonRelease   CheckID = "common_production_release"
	CheckTrustBundle     CheckID = "trust_bundle_valid"
	CheckCompleteExport  CheckID = "immutable_export_complete"
	CheckSignatures      CheckID = "authorized_signatures_valid"
	CheckEvidenceBinding CheckID = "reviewed_evidence_bound"
	CheckCurrent         CheckID = "approvals_current"
	CheckReleaseReview   CheckID = "release_authority_review_complete"
	OutcomePassed        Outcome = "passed"
	OutcomeFailed        Outcome = "failed"
	OutcomeInconclusive  Outcome = "inconclusive"
)

var (
	digestPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	filePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.json$`)
	requiredControls = []string{
		"beta_readiness", "external_signup", "legal_pages",
		"security_contact", "status_page", "support_policy",
	}
	requiredChecks = []CheckID{
		CheckPrerequisites, CheckCommonRelease, CheckTrustBundle,
		CheckCompleteExport, CheckSignatures, CheckEvidenceBinding,
		CheckCurrent, CheckReleaseReview,
	}
)

type ManifestFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type ExportManifest struct {
	Schema     string         `json:"schema"`
	ExportID   string         `json:"export_id"`
	Gate       string         `json:"gate"`
	ExportedAt time.Time      `json:"exported_at"`
	Files      []ManifestFile `json:"files"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                 string    `json:"schema"`
	Classification         string    `json:"classification"`
	Environment            string    `json:"environment"`
	ExportID               string    `json:"export_id"`
	ReviewID               string    `json:"review_id"`
	ReviewPolicyVersion    string    `json:"review_policy_version"`
	InventoryID            string    `json:"inventory_id"`
	InventoryReceiptSHA256 string    `json:"inventory_receipt_sha256"`
	PlanID                 string    `json:"plan_id"`
	PlanReceiptSHA256      string    `json:"plan_receipt_sha256"`
	ChangeID               string    `json:"change_id"`
	ChangeReceiptSHA256    string    `json:"change_receipt_sha256"`
	ReleaseID              string    `json:"release_id"`
	ReleaseReceiptSHA256   string    `json:"release_receipt_sha256"`
	LaunchAssetReviewID    string    `json:"launch_asset_review_id"`
	LaunchAssetSHA256      string    `json:"launch_asset_receipt_sha256"`
	BetaGateReviewID       string    `json:"beta_gate_review_id"`
	BetaGateSHA256         string    `json:"beta_gate_receipt_sha256"`
	TrustBundleSHA256      string    `json:"trust_bundle_sha256"`
	ApprovalExportSHA256   string    `json:"approval_export_sha256"`
	ReleaseReviewSHA256    string    `json:"release_authority_review_sha256"`
	ExportedAt             time.Time `json:"exported_at"`
	ReviewedAt             time.Time `json:"reviewed_at"`
	GeneratedAt            time.Time `json:"generated_at"`
	Ready                  bool      `json:"ready"`
	Checks                 []Check   `json:"checks"`
}

type ControlResult struct {
	Control string  `json:"control"`
	Outcome Outcome `json:"outcome"`
}

type Receipt struct {
	Input
	Schema                string          `json:"schema"`
	InputSHA256           string          `json:"input_sha256"`
	ManifestSHA256        string          `json:"manifest_sha256"`
	CollectedAt           time.Time       `json:"collected_at"`
	ApprovalArtifactCount int             `json:"approval_artifact_count"`
	RequiredControlCount  int             `json:"required_control_count"`
	VerifiedControlCount  int             `json:"verified_control_count"`
	MissingControlCount   int             `json:"missing_control_count"`
	RejectedControlCount  int             `json:"rejected_control_count"`
	ExpiredControlCount   int             `json:"expired_control_count"`
	MinimumExpirySeconds  int64           `json:"minimum_expiry_seconds"`
	ControlResults        []ControlResult `json:"control_results"`
	CheckCount            int             `json:"check_count"`
	PassedCount           int             `json:"passed_count"`
	FailedCount           int             `json:"failed_count"`
	InconclusiveCount     int             `json:"inconclusive_count"`
}

func RequiredControls() []string { return append([]string(nil), requiredControls...) }
func RequiredChecks() []CheckID  { return append([]CheckID(nil), requiredChecks...) }

func Collect(launchPath, gatePath, trustPath, approvalsDirectory, manifestPath, inputPath string, now time.Time) (Receipt, error) {
	launch, launchDigest, err := launchassetevidence.LoadReady(launchPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready launch-asset receipt: %w", err)
	}
	gate, gateDigest, err := publicbetagateevidence.LoadReady(gatePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready public-beta gate receipt: %w", err)
	}
	var bundle readiness.TrustBundle
	trustDigest, err := decodeStrictRegular(trustPath, &bundle)
	if err != nil {
		return Receipt{}, fmt.Errorf("load trust bundle: %w", err)
	}
	if err := readiness.ValidateTrustBundle(bundle); err != nil {
		return Receipt{}, err
	}
	var manifest ExportManifest
	manifestDigest, err := decodeStrictRegular(manifestPath, &manifest)
	if err != nil {
		return Receipt{}, fmt.Errorf("load export manifest: %w", err)
	}
	exportDigest, artifactCount, approvals, err := loadVerifiedExportFor(approvalsDirectory, manifest, ManifestSchemaV1, "public_beta")
	if err != nil {
		return Receipt{}, err
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(launch, launchDigest, gate, gateDigest, trustDigest, bundle, approvals, manifest, manifestDigest, exportDigest, artifactCount, input, inputDigest, now)
}

func build(launch launchassetevidence.Receipt, launchDigest string, gate publicbetagateevidence.Receipt, gateDigest, trustDigest string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, manifest ExportManifest, manifestDigest, exportDigest string, artifactCount int, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if launch.InventoryID != gate.InventoryID || launch.InventoryReceiptSHA256 != gate.InventoryReceiptSHA256 || launch.PlanID != gate.PlanID || launch.PlanReceiptSHA256 != gate.PlanReceiptSHA256 || launch.ChangeID != gate.ChangeID || launch.ChangeReceiptSHA256 != gate.ChangeReceiptSHA256 || launch.ReleaseID != gate.ReleaseID || launch.ReleaseReceiptSHA256 != gate.ReleaseReceiptSHA256 {
		return Receipt{}, errors.New("approval export prerequisites do not share one production release")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "production_external" || input.Environment != "production" || !allOpaque(input.ExportID, input.ReviewID, input.ReviewPolicyVersion, input.InventoryID, input.PlanID, input.ChangeID, input.ReleaseID, input.LaunchAssetReviewID, input.BetaGateReviewID) {
		return Receipt{}, errors.New("approval export identity is invalid")
	}
	if input.ExportID != manifest.ExportID || input.ExportedAt.UTC() != manifest.ExportedAt.UTC() || input.InventoryID != launch.InventoryID || input.InventoryReceiptSHA256 != launch.InventoryReceiptSHA256 || input.PlanID != launch.PlanID || input.PlanReceiptSHA256 != launch.PlanReceiptSHA256 || input.ChangeID != launch.ChangeID || input.ChangeReceiptSHA256 != launch.ChangeReceiptSHA256 || input.ReleaseID != launch.ReleaseID || input.ReleaseReceiptSHA256 != launch.ReleaseReceiptSHA256 || input.LaunchAssetReviewID != launch.ReviewID || input.LaunchAssetSHA256 != launchDigest || input.BetaGateReviewID != gate.GateReviewID || input.BetaGateSHA256 != gateDigest || input.TrustBundleSHA256 != trustDigest || input.ApprovalExportSHA256 != exportDigest || !allDigests(input.InventoryReceiptSHA256, input.PlanReceiptSHA256, input.ChangeReceiptSHA256, input.ReleaseReceiptSHA256, input.LaunchAssetSHA256, input.BetaGateSHA256, input.TrustBundleSHA256, input.ApprovalExportSHA256, input.ReleaseReviewSHA256, manifestDigest, inputDigest) {
		return Receipt{}, errors.New("approval export prerequisite or artifact binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("approval export collection time is invalid")
	}
	now = now.UTC()
	exported, reviewed, generated := input.ExportedAt.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if exported.IsZero() || exported.Before(launch.CollectedAt.UTC()) || exported.Before(gate.CollectedAt.UTC()) || reviewed.Before(exported) || reviewed.Sub(exported) > maximumEvidenceAge || generated.Before(reviewed) || generated.Before(now.Add(-maximumEvidenceAge)) || generated.After(now) {
		return Receipt{}, errors.New("approval export timeline is invalid")
	}
	report, err := readiness.VerifyApprovals("public_beta", requiredControls, bundle, approvals, now)
	if err != nil {
		return Receipt{}, err
	}
	bindingsReady := report.Ready
	if bindingsReady {
		for control, verified := range report.Verified {
			expected := launchDigest
			if control == "beta_readiness" {
				expected = gateDigest
			}
			if verified.EvidenceSHA256 != expected {
				return Receipt{}, errors.New("approval reviewed-evidence binding is invalid")
			}
		}
	}
	expectedOutcomes := map[CheckID]Outcome{
		CheckPrerequisites: OutcomePassed, CheckCommonRelease: OutcomePassed,
		CheckTrustBundle: OutcomePassed, CheckCompleteExport: OutcomePassed,
		CheckSignatures:      outcome(len(report.Missing) == 0),
		CheckEvidenceBinding: conditionalOutcome(bindingsReady),
		CheckCurrent:         outcome(len(report.Expired) == 0),
		CheckReleaseReview:   outcomeForInput(input.Checks, CheckReleaseReview),
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks, expectedOutcomes)
	if err != nil {
		return Receipt{}, err
	}
	ready := report.Ready && bindingsReady && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("approval export readiness contradicts evidence")
	}
	controls, minimumExpiry := summarizeControls(report, now)
	input.Schema, input.ExportedAt, input.ReviewedAt, input.GeneratedAt, input.Checks = ReceiptSchemaV1, exported, reviewed, generated, checks
	return Receipt{Input: input, Schema: ReceiptSchemaV1, InputSHA256: inputDigest, ManifestSHA256: manifestDigest, CollectedAt: now, ApprovalArtifactCount: artifactCount, RequiredControlCount: len(requiredControls), VerifiedControlCount: len(report.Verified), MissingControlCount: len(report.Missing), RejectedControlCount: len(report.Rejected), ExpiredControlCount: len(report.Expired), MinimumExpirySeconds: minimumExpiry, ControlResults: controls, CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive}, nil
}

func verifyExport(directory string, manifest ExportManifest) (string, int, error) {
	return verifyExportFor(directory, manifest, ManifestSchemaV1, "public_beta")
}

func verifyExportFor(directory string, manifest ExportManifest, expectedSchema, expectedGate string) (string, int, error) {
	digest, count, _, err := loadVerifiedExportForWithHook(directory, manifest, expectedSchema, expectedGate, nil)
	return digest, count, err
}

type exportFileSnapshot struct {
	name string
	path string
	info os.FileInfo
}

func loadVerifiedExportFor(directory string, manifest ExportManifest, expectedSchema, expectedGate string) (string, int, []readiness.SignedApproval, error) {
	return loadVerifiedExportForWithHook(directory, manifest, expectedSchema, expectedGate, nil)
}

func loadVerifiedExportForWithHook(directory string, manifest ExportManifest, expectedSchema, expectedGate string, afterSnapshot func()) (string, int, []readiness.SignedApproval, error) {
	if manifest.Schema != expectedSchema || manifest.Gate != expectedGate || !opaquePattern.MatchString(manifest.ExportID) || manifest.ExportedAt.IsZero() || len(manifest.Files) == 0 || len(manifest.Files) > maximumFileCount {
		return "", 0, nil, errors.New("approval export manifest is invalid")
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return "", 0, nil, errors.New("approval export directory is invalid")
	}
	snapshot, err := snapshotExportDirectory(directory)
	if err != nil {
		return "", 0, nil, err
	}
	if afterSnapshot != nil {
		afterSnapshot()
	}
	declared := map[string]string{}
	for _, file := range manifest.Files {
		if !filePattern.MatchString(file.Name) || !digestPattern.MatchString(file.SHA256) {
			return "", 0, nil, errors.New("approval export manifest artifact is invalid")
		}
		if _, duplicate := declared[file.Name]; duplicate {
			return "", 0, nil, errors.New("approval export manifest artifact is duplicated")
		}
		declared[file.Name] = file.SHA256
	}
	if len(snapshot) != len(declared) {
		return "", 0, nil, errors.New("approval export is incomplete")
	}
	approvals := make([]readiness.SignedApproval, 0, len(snapshot))
	for _, artifact := range snapshot {
		expectedDigest, ok := declared[artifact.name]
		if !ok {
			return "", 0, nil, errors.New("approval export is incomplete")
		}
		contents, err := readRegularExpected(artifact.path, artifact.info)
		if err != nil {
			return "", 0, nil, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(contents)) != expectedDigest {
			return "", 0, nil, errors.New("approval export artifact digest does not match")
		}
		var approval readiness.SignedApproval
		if err := decodeStrictJSON(contents, &approval); err != nil {
			return "", 0, nil, err
		}
		approvals = append(approvals, approval)
	}
	directoryAfter, err := os.Lstat(directory)
	if err != nil || !directoryAfter.IsDir() || directoryAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(directoryInfo, directoryAfter) {
		return "", 0, nil, errors.New("approval export directory changed during verification")
	}
	afterSnapshotState, err := snapshotExportDirectory(directory)
	if err != nil || !sameExportSnapshot(snapshot, afterSnapshotState) {
		return "", 0, nil, errors.New("approval export changed during verification")
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write([]byte(declared[name]))
		hash.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), len(names), approvals, nil
}

func snapshotExportDirectory(directory string) ([]exportFileSnapshot, error) {
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) == 0 || len(entries) > maximumFileCount {
		return nil, errors.New("read approval export directory")
	}
	snapshot := make([]exportFileSnapshot, 0, len(entries))
	for _, entry := range entries {
		if !filePattern.MatchString(entry.Name()) {
			return nil, errors.New("approval export contains an unsafe artifact")
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumInputBytes {
			return nil, errors.New("approval export contains an unsafe artifact")
		}
		snapshot = append(snapshot, exportFileSnapshot{name: entry.Name(), path: path, info: info})
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].name < snapshot[j].name })
	return snapshot, nil
}

func sameExportSnapshot(left, right []exportFileSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].name != right[index].name || left[index].info.Size() != right[index].info.Size() || !left[index].info.ModTime().Equal(right[index].info.ModTime()) || !os.SameFile(left[index].info, right[index].info) {
			return false
		}
	}
	return true
}

func validateChecks(values []Check, expected map[CheckID]Outcome) ([]Check, int, int, int, error) {
	return validateChecksFor(values, expected, requiredChecks)
}

func validateChecksFor(values []Check, expected map[CheckID]Outcome, order []CheckID) ([]Check, int, int, int, error) {
	if len(values) != len(order) {
		return nil, 0, 0, 0, errors.New("approval export checks are incomplete")
	}
	byID := map[CheckID]Check{}
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate || !digestPattern.MatchString(value.EvidenceSHA256) || (value.Outcome != OutcomePassed && value.Outcome != OutcomeFailed && value.Outcome != OutcomeInconclusive) {
			return nil, 0, 0, 0, errors.New("approval export check is invalid or duplicated")
		}
		byID[value.ID] = value
	}
	ordered := make([]Check, 0, len(order))
	passed, failed, inconclusive := 0, 0, 0
	for _, id := range order {
		value, ok := byID[id]
		if !ok || value.Outcome != expected[id] {
			return nil, 0, 0, 0, errors.New("approval export check contradicts derived evidence")
		}
		ordered = append(ordered, value)
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		default:
			inconclusive++
		}
	}
	return ordered, passed, failed, inconclusive, nil
}

func summarizeControls(report readiness.ApprovalReport, now time.Time) ([]ControlResult, int64) {
	missing, rejected, expired := stringSet(report.Missing), stringSet(report.Rejected), stringSet(report.Expired)
	results := make([]ControlResult, 0, len(requiredControls))
	minimum := int64(0)
	for _, control := range requiredControls {
		result := ControlResult{Control: control, Outcome: OutcomePassed}
		switch {
		case missing[control]:
			result.Outcome = OutcomeInconclusive
		case rejected[control] || expired[control]:
			result.Outcome = OutcomeFailed
		}
		if verified, ok := report.Verified[control]; ok {
			expires, _ := time.Parse(time.RFC3339Nano, verified.ExpiresAt)
			remaining := int64(expires.Sub(now) / time.Second)
			if minimum == 0 || remaining < minimum {
				minimum = remaining
			}
		}
		results = append(results, result)
	}
	return results, minimum
}

func outcome(value bool) Outcome {
	if value {
		return OutcomePassed
	}
	return OutcomeFailed
}
func conditionalOutcome(value bool) Outcome {
	if value {
		return OutcomePassed
	}
	return OutcomeInconclusive
}
func outcomeForInput(checks []Check, id CheckID) Outcome {
	for _, check := range checks {
		if check.ID == id {
			return check.Outcome
		}
	}
	return ""
}
func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
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

func digestRegular(path string) (string, error) {
	contents, err := readRegular(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func decodeStrictRegular(path string, destination any) (string, error) {
	contents, err := readRegular(path)
	if err != nil {
		return "", err
	}
	if err := decodeStrictJSON(contents, destination); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func decodeStrictJSON(contents []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("approval export JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("approval export JSON contains trailing data")
	}
	return nil
}

func readRegular(path string) ([]byte, error) {
	return readRegularExpected(path, nil)
}

func readRegularExpected(path string, expected os.FileInfo) ([]byte, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes || (expected != nil && (!os.SameFile(expected, validated) || expected.Size() != validated.Size() || !expected.ModTime().Equal(validated.ModTime()))) {
		return nil, errors.New("approval export evidence must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open approval export evidence")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return nil, errors.New("approval export evidence changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return nil, errors.New("read approval export evidence")
	}
	openedAfter, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfter) || openedAfter.Size() != opened.Size() || !openedAfter.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("approval export evidence changed while reading")
	}
	pathAfter, err := os.Lstat(path)
	if err != nil || !pathAfter.Mode().IsRegular() || pathAfter.Size() != opened.Size() || !pathAfter.ModTime().Equal(opened.ModTime()) || !os.SameFile(opened, pathAfter) {
		return nil, errors.New("approval export evidence changed while reading")
	}
	return contents, nil
}

func Publish(path string, receipt Receipt) error {
	return publishAny(path, receipt, "approval export receipt", ".agent-memory-approval-export-*")
}

func publishAny(path string, receipt any, label, pattern string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s path is required", label)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s destination already exists", label)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s destination", label)
	}
	return evidencepublish.JSON(path, receipt, pattern)
}
