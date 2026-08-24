// Package privacyreviewevidence normalizes content-free CP7-A Privacy and
// Counsel review evidence without interpreting law or granting approval.
package privacyreviewevidence

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
)

const (
	InputSchemaV1        = "agent-memory-privacy-review-input-v1"
	ReceiptSchemaV1      = "agent-memory-privacy-review-receipt-v1"
	maximumInputBytes    = 128 << 10
	maximumCollectionAge = 24 * time.Hour
)

type SurfaceID string
type ContractID string
type CheckID string
type Outcome string

const (
	SurfacePrivacyOverview    SurfaceID  = "privacy_overview"
	SurfaceSourceCustody      SurfaceID  = "source_custody"
	SurfaceSourceDetails      SurfaceID  = "source_details"
	SurfaceSourceDeletion     SurfaceID  = "source_deletion"
	ContractRightsAttestation ContractID = "rights_attestation"
	ContractPrivacyOverview   ContractID = "privacy_overview"
	ContractSourceDeletion    ContractID = "source_deletion"
	ContractAccountDeletion   ContractID = "account_deletion"
	ContractPortableExport    ContractID = "portable_export"
	CheckRenderedSurfaces     CheckID    = "rendered_surface_coverage_complete"
	CheckCopyReview           CheckID    = "customer_copy_review_complete"
	CheckAccessibility        CheckID    = "accessibility_review_complete"
	CheckReceiptContracts     CheckID    = "receipt_contract_coverage_complete"
	CheckLifecycleDistinction CheckID    = "revocation_and_purge_distinction_reviewed"
	CheckConsentExport        CheckID    = "consent_and_export_disclosure_reviewed"
	CheckPrivacySignoff       CheckID    = "privacy_signed_review_complete"
	CheckCounselSignoff       CheckID    = "counsel_signed_review_complete"
	OutcomePassed             Outcome    = "passed"
	OutcomeFailed             Outcome    = "failed"
	OutcomeInconclusive       Outcome    = "inconclusive"
)

var (
	digestPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredSurfaces  = []SurfaceID{SurfacePrivacyOverview, SurfaceSourceCustody, SurfaceSourceDetails, SurfaceSourceDeletion}
	requiredContracts = []ContractID{ContractRightsAttestation, ContractPrivacyOverview, ContractSourceDeletion, ContractAccountDeletion, ContractPortableExport}
	requiredChecks    = []CheckID{CheckRenderedSurfaces, CheckCopyReview, CheckAccessibility, CheckReceiptContracts, CheckLifecycleDistinction, CheckConsentExport, CheckPrivacySignoff, CheckCounselSignoff}
)

type Surface struct {
	ID                        SurfaceID `json:"id"`
	RenderedSHA256            string    `json:"rendered_sha256"`
	CopySHA256                string    `json:"copy_sha256"`
	AccessibilityReviewSHA256 string    `json:"accessibility_review_sha256"`
	Outcome                   Outcome   `json:"outcome"`
}
type Contract struct {
	ID                        ContractID `json:"id"`
	SchemaSHA256              string     `json:"schema_sha256"`
	CompatibilityReviewSHA256 string     `json:"compatibility_review_sha256"`
	Outcome                   Outcome    `json:"outcome"`
}
type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                       string     `json:"schema"`
	Classification               string     `json:"classification"`
	Environment                  string     `json:"environment"`
	ReviewID                     string     `json:"review_id"`
	DashboardBuildVersion        string     `json:"dashboard_build_version"`
	OpenAPIVersion               string     `json:"openapi_version"`
	ReceiptManifestVersion       string     `json:"receipt_manifest_version"`
	DashboardBuildManifestSHA256 string     `json:"dashboard_build_manifest_sha256"`
	OpenAPISHA256                string     `json:"openapi_sha256"`
	ReceiptSchemaManifestSHA256  string     `json:"receipt_schema_manifest_sha256"`
	PrivacySignedReviewSHA256    string     `json:"privacy_signed_review_sha256"`
	CounselSignedReviewSHA256    string     `json:"counsel_signed_review_sha256"`
	ReviewStartedAt              time.Time  `json:"review_started_at"`
	ReviewCompletedAt            time.Time  `json:"review_completed_at"`
	GeneratedAt                  time.Time  `json:"generated_at"`
	Ready                        bool       `json:"ready"`
	Surfaces                     []Surface  `json:"surfaces"`
	Contracts                    []Contract `json:"contracts"`
	Checks                       []Check    `json:"checks"`
}

type Receipt struct {
	Schema                       string     `json:"schema"`
	Classification               string     `json:"classification"`
	Environment                  string     `json:"environment"`
	ReviewID                     string     `json:"review_id"`
	DashboardBuildVersion        string     `json:"dashboard_build_version"`
	OpenAPIVersion               string     `json:"openapi_version"`
	ReceiptManifestVersion       string     `json:"receipt_manifest_version"`
	DashboardBuildManifestSHA256 string     `json:"dashboard_build_manifest_sha256"`
	OpenAPISHA256                string     `json:"openapi_sha256"`
	ReceiptSchemaManifestSHA256  string     `json:"receipt_schema_manifest_sha256"`
	PrivacySignedReviewSHA256    string     `json:"privacy_signed_review_sha256"`
	CounselSignedReviewSHA256    string     `json:"counsel_signed_review_sha256"`
	InputSHA256                  string     `json:"input_sha256"`
	ReviewStartedAt              time.Time  `json:"review_started_at"`
	ReviewCompletedAt            time.Time  `json:"review_completed_at"`
	GeneratedAt                  time.Time  `json:"generated_at"`
	CollectedAt                  time.Time  `json:"collected_at"`
	Ready                        bool       `json:"ready"`
	SurfaceCount                 int        `json:"surface_count"`
	SurfacePassedCount           int        `json:"surface_passed_count"`
	SurfaceFailedCount           int        `json:"surface_failed_count"`
	SurfaceInconclusiveCount     int        `json:"surface_inconclusive_count"`
	ContractCount                int        `json:"contract_count"`
	ContractPassedCount          int        `json:"contract_passed_count"`
	ContractFailedCount          int        `json:"contract_failed_count"`
	ContractInconclusiveCount    int        `json:"contract_inconclusive_count"`
	CheckCount                   int        `json:"check_count"`
	PassedCount                  int        `json:"passed_count"`
	FailedCount                  int        `json:"failed_count"`
	InconclusiveCount            int        `json:"inconclusive_count"`
	Surfaces                     []Surface  `json:"surfaces"`
	Contracts                    []Contract `json:"contracts"`
	Checks                       []Check    `json:"checks"`
}

func RequiredSurfaces() []SurfaceID   { return append([]SurfaceID(nil), requiredSurfaces...) }
func RequiredContracts() []ContractID { return append([]ContractID(nil), requiredContracts...) }
func RequiredChecks() []CheckID       { return append([]CheckID(nil), requiredChecks...) }

func Collect(path string, now time.Time) (Receipt, error) {
	var input Input
	digest, err := decodeStrictRegular(path, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(input, digest, now)
}

func build(input Input, inputDigest string, now time.Time) (Receipt, error) {
	if input.Schema != InputSchemaV1 || input.Classification != "external_business" || input.Environment != "external" || !allOpaque(input.ReviewID, input.DashboardBuildVersion, input.OpenAPIVersion, input.ReceiptManifestVersion) || !allDigests(input.DashboardBuildManifestSHA256, input.OpenAPISHA256, input.ReceiptSchemaManifestSHA256, input.PrivacySignedReviewSHA256, input.CounselSignedReviewSHA256, inputDigest) {
		return Receipt{}, errors.New("privacy review identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("privacy review collection time is invalid")
	}
	now = now.UTC()
	started, completed, generated := input.ReviewStartedAt.UTC(), input.ReviewCompletedAt.UTC(), input.GeneratedAt.UTC()
	if started.IsZero() || completed.Before(started) || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("privacy review timeline is invalid")
	}
	surfaces, sp, sf, si, err := validateSurfaces(input.Surfaces)
	if err != nil {
		return Receipt{}, err
	}
	contracts, cp, cf, ci, err := validateContracts(input.Contracts)
	if err != nil {
		return Receipt{}, err
	}
	checks, p, f, i, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	if outcomeFor(checks, CheckRenderedSurfaces) != aggregateOutcome(sp, sf, si, len(requiredSurfaces)) || outcomeFor(checks, CheckReceiptContracts) != aggregateOutcome(cp, cf, ci, len(requiredContracts)) {
		return Receipt{}, errors.New("privacy review check contradicts reviewed artifacts")
	}
	ready := sp == len(requiredSurfaces) && sf == 0 && si == 0 && cp == len(requiredContracts) && cf == 0 && ci == 0 && p == len(requiredChecks) && f == 0 && i == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("privacy review readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, ReviewID: input.ReviewID, DashboardBuildVersion: input.DashboardBuildVersion, OpenAPIVersion: input.OpenAPIVersion, ReceiptManifestVersion: input.ReceiptManifestVersion, DashboardBuildManifestSHA256: input.DashboardBuildManifestSHA256, OpenAPISHA256: input.OpenAPISHA256, ReceiptSchemaManifestSHA256: input.ReceiptSchemaManifestSHA256, PrivacySignedReviewSHA256: input.PrivacySignedReviewSHA256, CounselSignedReviewSHA256: input.CounselSignedReviewSHA256, InputSHA256: inputDigest, ReviewStartedAt: started, ReviewCompletedAt: completed, GeneratedAt: generated, CollectedAt: now, Ready: ready, SurfaceCount: len(surfaces), SurfacePassedCount: sp, SurfaceFailedCount: sf, SurfaceInconclusiveCount: si, ContractCount: len(contracts), ContractPassedCount: cp, ContractFailedCount: cf, ContractInconclusiveCount: ci, CheckCount: len(checks), PassedCount: p, FailedCount: f, InconclusiveCount: i, Surfaces: surfaces, Contracts: contracts, Checks: checks}, nil
}

func validateSurfaces(values []Surface) ([]Surface, int, int, int, error) {
	if len(values) != len(requiredSurfaces) {
		return nil, 0, 0, 0, errors.New("privacy review surfaces are incomplete")
	}
	by := map[SurfaceID]Surface{}
	p, f, i := 0, 0, 0
	for _, v := range values {
		if !allDigests(v.RenderedSHA256, v.CopySHA256, v.AccessibilityReviewSHA256) {
			return nil, 0, 0, 0, errors.New("privacy review surface digest is invalid")
		}
		if _, ok := by[v.ID]; ok {
			return nil, 0, 0, 0, errors.New("privacy review surface is duplicated")
		}
		var err error
		p, f, i, err = count(v.Outcome, p, f, i)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		by[v.ID] = v
	}
	ordered := make([]Surface, 0, len(requiredSurfaces))
	for _, id := range requiredSurfaces {
		v, ok := by[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("required privacy review surface is missing")
		}
		ordered = append(ordered, v)
	}
	return ordered, p, f, i, nil
}
func validateContracts(values []Contract) ([]Contract, int, int, int, error) {
	if len(values) != len(requiredContracts) {
		return nil, 0, 0, 0, errors.New("privacy review contracts are incomplete")
	}
	by := map[ContractID]Contract{}
	p, f, i := 0, 0, 0
	for _, v := range values {
		if !allDigests(v.SchemaSHA256, v.CompatibilityReviewSHA256) {
			return nil, 0, 0, 0, errors.New("privacy review contract digest is invalid")
		}
		if _, ok := by[v.ID]; ok {
			return nil, 0, 0, 0, errors.New("privacy review contract is duplicated")
		}
		var err error
		p, f, i, err = count(v.Outcome, p, f, i)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		by[v.ID] = v
	}
	ordered := make([]Contract, 0, len(requiredContracts))
	for _, id := range requiredContracts {
		v, ok := by[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("required privacy review contract is missing")
		}
		ordered = append(ordered, v)
	}
	return ordered, p, f, i, nil
}
func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("privacy review checks are incomplete")
	}
	by := map[CheckID]Check{}
	p, f, i := 0, 0, 0
	for _, v := range values {
		if !allDigests(v.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("privacy review check digest is invalid")
		}
		if _, ok := by[v.ID]; ok {
			return nil, 0, 0, 0, errors.New("privacy review check is duplicated")
		}
		var err error
		p, f, i, err = count(v.Outcome, p, f, i)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		by[v.ID] = v
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		v, ok := by[id]
		if !ok {
			return nil, 0, 0, 0, errors.New("required privacy review check is missing")
		}
		ordered = append(ordered, v)
	}
	return ordered, p, f, i, nil
}
func count(o Outcome, p, f, i int) (int, int, int, error) {
	switch o {
	case OutcomePassed:
		p++
	case OutcomeFailed:
		f++
	case OutcomeInconclusive:
		i++
	default:
		return 0, 0, 0, errors.New("privacy review outcome is invalid")
	}
	return p, f, i, nil
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
		return "", errors.New("privacy review path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("privacy review input must be a bounded regular file")
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
		return "", errors.New("privacy review input identity changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, maximumInputBytes+1))
	if err != nil || int64(len(b)) != opened.Size() || len(b) > maximumInputBytes {
		return "", errors.New("read privacy review input")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(target); err != nil {
		return "", err
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("privacy review input contains trailing data")
	}
	openedAfterRead, err := f.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("privacy review input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("privacy review input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("privacy review receipt path is required")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-privacy-review-*")
}
