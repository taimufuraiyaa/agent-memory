// Package isolationreview normalizes content-free evidence from an independent
// tenant-isolation review of one deployed staging release.
package isolationreview

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
	InputSchemaV1        = "agent-memory-staging-tenant-isolation-review-v1"
	ReceiptSchemaV1      = "agent-memory-staging-tenant-isolation-receipt-v1"
	maximumInputBytes    = 64 << 10
	maximumReviewSpan    = 14 * 24 * time.Hour
	maximumCollectionAge = 24 * time.Hour
	maximumFindings      = 1000
)

type DomainID string
type Outcome string

const (
	DomainControlAPIAuthorization DomainID = "control_api_authorization_reviewed"
	DomainForcedRLS               DomainID = "forced_rls_effectiveness_reviewed"
	DomainIdentifierSubstitution  DomainID = "identifier_substitution_reviewed"
	DomainCacheNamespace          DomainID = "cache_namespace_reviewed"
	DomainConcealmentErrors       DomainID = "concealment_and_error_behavior_reviewed"
	DomainTimingInference         DomainID = "timing_inference_reviewed"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredDomains = []DomainID{
		DomainControlAPIAuthorization,
		DomainForcedRLS,
		DomainIdentifierSubstitution,
		DomainCacheNamespace,
		DomainConcealmentErrors,
		DomainTimingInference,
	}
)

type ReviewWindow struct {
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type Domain struct {
	ID             DomainID `json:"id"`
	Outcome        Outcome  `json:"outcome"`
	FindingCount   int      `json:"finding_count"`
	EvidenceSHA256 string   `json:"evidence_sha256"`
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
	Domains                []Domain     `json:"domains"`
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
	DomainCount            int          `json:"domain_count"`
	PassedCount            int          `json:"passed_count"`
	FailedCount            int          `json:"failed_count"`
	InconclusiveCount      int          `json:"inconclusive_count"`
	FindingCount           int          `json:"finding_count"`
	Domains                []Domain     `json:"domains"`
}

func RequiredDomains() []DomainID { return append([]DomainID(nil), requiredDomains...) }

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
		return Receipt{}, errors.New("tenant isolation review platform chain is invalid or unready")
	}
	if release.Schema != "agent-memory-kubernetes-release-receipt-v1" || release.Environment != "staging" || release.Namespace != "agent-memory-staging" ||
		!opaquePattern.MatchString(release.ReleaseID) || release.Outcome != "passed" || release.Migration.Outcome != "complete" ||
		release.Rollouts.Outcome != "healthy" || release.Rollback.Attempted || release.Rollback.Succeeded ||
		release.StartedAt.IsZero() || release.CompletedAt.Before(release.StartedAt) || !digestPattern.MatchString(releaseDigest) {
		return Receipt{}, errors.New("tenant isolation review staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !opaquePattern.MatchString(input.ReviewID) ||
		input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.ChangeID != change.ChangeID ||
		input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest ||
		!digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("tenant isolation review identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("tenant isolation review collection time is invalid")
	}
	now = now.UTC()
	review := ReviewWindow{StartedAt: input.Review.StartedAt.UTC(), CompletedAt: input.Review.CompletedAt.UTC()}
	generated := input.GeneratedAt.UTC()
	if review.StartedAt.IsZero() || review.CompletedAt.IsZero() || generated.IsZero() ||
		review.StartedAt.Before(change.GeneratedAt.UTC()) || review.StartedAt.Before(release.CompletedAt.UTC()) ||
		review.CompletedAt.Before(review.StartedAt) || review.CompletedAt.Sub(review.StartedAt) > maximumReviewSpan ||
		generated.Before(review.CompletedAt) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("tenant isolation review timeline is invalid")
	}
	domains, passed, failed, inconclusive, findings, err := validateDomains(input.Domains)
	if err != nil {
		return Receipt{}, err
	}
	ready := passed == len(requiredDomains) && findings == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("tenant isolation review readiness contradicts domains")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, ReviewID: input.ReviewID,
		InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, ChangeID: input.ChangeID,
		ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: input.ReleaseReceiptSHA256,
		InputSHA256: inputDigest, Ready: ready, GeneratedAt: generated, CollectedAt: now, Review: review,
		DomainCount: len(domains), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, FindingCount: findings, Domains: domains,
	}, nil
}

func validateDomains(domains []Domain) ([]Domain, int, int, int, int, error) {
	if len(domains) != len(requiredDomains) {
		return nil, 0, 0, 0, 0, errors.New("tenant isolation review domains are incomplete")
	}
	byID := make(map[DomainID]Domain, len(domains))
	passed, failed, inconclusive, findings := 0, 0, 0, 0
	for _, domain := range domains {
		if !digestPattern.MatchString(domain.EvidenceSHA256) || domain.FindingCount < 0 || domain.FindingCount > maximumFindings {
			return nil, 0, 0, 0, 0, errors.New("tenant isolation review domain is invalid")
		}
		switch domain.Outcome {
		case OutcomePassed:
			if domain.FindingCount != 0 {
				return nil, 0, 0, 0, 0, errors.New("passed isolation review domain contains findings")
			}
			passed++
		case OutcomeFailed:
			if domain.FindingCount == 0 {
				return nil, 0, 0, 0, 0, errors.New("failed isolation review domain has no finding")
			}
			failed++
		case OutcomeInconclusive:
			if domain.FindingCount != 0 {
				return nil, 0, 0, 0, 0, errors.New("inconclusive isolation review domain contains a known finding")
			}
			inconclusive++
		default:
			return nil, 0, 0, 0, 0, errors.New("tenant isolation review outcome is invalid")
		}
		if _, duplicate := byID[domain.ID]; duplicate {
			return nil, 0, 0, 0, 0, errors.New("tenant isolation review domain is duplicated")
		}
		byID[domain.ID] = domain
		findings += domain.FindingCount
	}
	ordered := make([]Domain, 0, len(requiredDomains))
	for _, id := range requiredDomains {
		domain, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, 0, errors.New("tenant isolation review required domain is missing")
		}
		ordered = append(ordered, domain)
	}
	return ordered, passed, failed, inconclusive, findings, nil
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("tenant isolation review path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("tenant isolation review must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open tenant isolation review")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("tenant isolation review changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read tenant isolation review")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("tenant isolation review JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("tenant isolation review contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("tenant isolation review changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("tenant isolation review changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("tenant isolation receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("tenant isolation receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect tenant isolation receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-isolation-review-*")
}
