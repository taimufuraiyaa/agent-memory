// Package retrievalrisk normalizes content-free independent staging evidence
// for the CP5-B two-tenant retrieval-isolation risk review.
package retrievalrisk

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
	InputSchemaV1             = "agent-memory-staging-retrieval-risk-review-v1"
	ReceiptSchemaV1           = "agent-memory-staging-retrieval-risk-receipt-v1"
	maximumInputBytes         = 128 << 10
	maximumReviewSpan         = 14 * 24 * time.Hour
	maximumCollectionAge      = 24 * time.Hour
	maximumCount              = 1_000_000
	maximumTimingMicroseconds = 60_000_000
)

type DomainID string
type Outcome string

const (
	DomainBlindCorpus      DomainID = "blind_corpus_independence_reviewed"
	DomainResultIsolation  DomainID = "result_isolation_reviewed"
	DomainCountConcealment DomainID = "public_count_concealment_reviewed"
	DomainTimingAnalysis   DomainID = "statistical_timing_analysis_reviewed"
	DomainCacheNamespace   DomainID = "cache_key_namespace_reviewed"
	DomainWarmCache        DomainID = "warm_cache_contamination_reviewed"
	DomainRiskTolerance    DomainID = "risk_tolerance_accepted"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	requiredDomains = []DomainID{DomainBlindCorpus, DomainResultIsolation, DomainCountConcealment, DomainTimingAnalysis, DomainCacheNamespace, DomainWarmCache, DomainRiskTolerance}
)

type Domain struct {
	ID             DomainID `json:"id"`
	Outcome        Outcome  `json:"outcome"`
	FindingCount   int      `json:"finding_count"`
	EvidenceSHA256 string   `json:"evidence_sha256"`
}

type Input struct {
	Schema                          string    `json:"schema"`
	Classification                  string    `json:"classification"`
	Environment                     string    `json:"environment"`
	ReviewID                        string    `json:"review_id"`
	CorpusVersion                   string    `json:"corpus_version"`
	TimingMethodVersion             string    `json:"timing_method_version"`
	ToleranceVersion                string    `json:"tolerance_version"`
	InventoryID                     string    `json:"inventory_id"`
	InventoryReceiptSHA256          string    `json:"inventory_receipt_sha256"`
	PlanID                          string    `json:"plan_id"`
	PlanReceiptSHA256               string    `json:"plan_receipt_sha256"`
	ChangeID                        string    `json:"change_id"`
	ChangeReceiptSHA256             string    `json:"change_receipt_sha256"`
	ReleaseID                       string    `json:"release_id"`
	ReleaseReceiptSHA256            string    `json:"release_receipt_sha256"`
	BlindCorpusSHA256               string    `json:"blind_corpus_sha256"`
	TimingReportSHA256              string    `json:"timing_report_sha256"`
	CacheReviewSHA256               string    `json:"cache_review_sha256"`
	RiskToleranceDecisionSHA256     string    `json:"risk_tolerance_decision_sha256"`
	ReviewStartedAt                 time.Time `json:"review_started_at"`
	ReviewCompletedAt               time.Time `json:"review_completed_at"`
	GeneratedAt                     time.Time `json:"generated_at"`
	TenantCount                     int       `json:"tenant_count"`
	CaseCount                       int       `json:"case_count"`
	TimingSampleCountPerClass       int       `json:"timing_sample_count_per_class"`
	ResultLeakCount                 int       `json:"result_leak_count"`
	CountLeakCount                  int       `json:"count_leak_count"`
	CacheLeakCount                  int       `json:"cache_leak_count"`
	MaximumTimingDeltaMicroseconds  int64     `json:"maximum_timing_delta_microseconds"`
	ObservedTimingDeltaMicroseconds int64     `json:"observed_timing_delta_microseconds"`
	Ready                           bool      `json:"ready"`
	Domains                         []Domain  `json:"domains"`
}

type Receipt struct {
	Schema                          string    `json:"schema"`
	Classification                  string    `json:"classification"`
	Environment                     string    `json:"environment"`
	ReviewID                        string    `json:"review_id"`
	CorpusVersion                   string    `json:"corpus_version"`
	TimingMethodVersion             string    `json:"timing_method_version"`
	ToleranceVersion                string    `json:"tolerance_version"`
	InventoryID                     string    `json:"inventory_id"`
	InventoryReceiptSHA256          string    `json:"inventory_receipt_sha256"`
	PlanID                          string    `json:"plan_id"`
	PlanReceiptSHA256               string    `json:"plan_receipt_sha256"`
	ChangeID                        string    `json:"change_id"`
	ChangeReceiptSHA256             string    `json:"change_receipt_sha256"`
	ReleaseID                       string    `json:"release_id"`
	ReleaseReceiptSHA256            string    `json:"release_receipt_sha256"`
	BlindCorpusSHA256               string    `json:"blind_corpus_sha256"`
	TimingReportSHA256              string    `json:"timing_report_sha256"`
	CacheReviewSHA256               string    `json:"cache_review_sha256"`
	RiskToleranceDecisionSHA256     string    `json:"risk_tolerance_decision_sha256"`
	InputSHA256                     string    `json:"input_sha256"`
	ReviewStartedAt                 time.Time `json:"review_started_at"`
	ReviewCompletedAt               time.Time `json:"review_completed_at"`
	GeneratedAt                     time.Time `json:"generated_at"`
	CollectedAt                     time.Time `json:"collected_at"`
	TenantCount                     int       `json:"tenant_count"`
	CaseCount                       int       `json:"case_count"`
	TimingSampleCountPerClass       int       `json:"timing_sample_count_per_class"`
	ResultLeakCount                 int       `json:"result_leak_count"`
	CountLeakCount                  int       `json:"count_leak_count"`
	CacheLeakCount                  int       `json:"cache_leak_count"`
	MaximumTimingDeltaMicroseconds  int64     `json:"maximum_timing_delta_microseconds"`
	ObservedTimingDeltaMicroseconds int64     `json:"observed_timing_delta_microseconds"`
	Ready                           bool      `json:"ready"`
	DomainCount                     int       `json:"domain_count"`
	PassedCount                     int       `json:"passed_count"`
	FailedCount                     int       `json:"failed_count"`
	InconclusiveCount               int       `json:"inconclusive_count"`
	FindingCount                    int       `json:"finding_count"`
	RiskBreachCount                 int       `json:"risk_breach_count"`
	Domains                         []Domain  `json:"domains"`
}

func RequiredDomains() []DomainID { return append([]DomainID(nil), requiredDomains...) }

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
	if inventory.Schema != platforminventory.SchemaV1 || inventory.Environment != platforminventory.Staging || plan.Schema != platformplan.SchemaV1 || plan.Environment != inventory.Environment || plan.InventoryID != inventory.InventoryID || plan.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || !platformplan.Assess(plan).Ready || change.Schema != platformchange.SchemaV1 || change.Environment != inventory.Environment || change.InventoryID != inventory.InventoryID || change.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || change.PlanID != plan.PlanID || change.PlanReceiptSHA256 != plan.ReceiptSHA256 || !platformchange.Assess(change).Ready || !digestPattern.MatchString(inventory.ReceiptSHA256) || !digestPattern.MatchString(plan.ReceiptSHA256) || !digestPattern.MatchString(change.ReceiptSHA256) {
		return Receipt{}, errors.New("retrieval risk platform chain is invalid or unready")
	}
	if !validPassedRelease(release, releaseDigest) {
		return Receipt{}, errors.New("retrieval risk staging release is invalid or unready")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" || !opaquePattern.MatchString(input.ReviewID) || !opaquePattern.MatchString(input.CorpusVersion) || !opaquePattern.MatchString(input.TimingMethodVersion) || !opaquePattern.MatchString(input.ToleranceVersion) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 || input.PlanID != plan.PlanID || input.PlanReceiptSHA256 != plan.ReceiptSHA256 || input.ChangeID != change.ChangeID || input.ChangeReceiptSHA256 != change.ReceiptSHA256 || input.ReleaseID != release.ReleaseID || input.ReleaseReceiptSHA256 != releaseDigest || !digestPattern.MatchString(input.BlindCorpusSHA256) || !digestPattern.MatchString(input.TimingReportSHA256) || !digestPattern.MatchString(input.CacheReviewSHA256) || !digestPattern.MatchString(input.RiskToleranceDecisionSHA256) || !digestPattern.MatchString(inputDigest) {
		return Receipt{}, errors.New("retrieval risk input identity or binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("retrieval risk collection time is invalid")
	}
	now = now.UTC()
	started, completed, generated := input.ReviewStartedAt.UTC(), input.ReviewCompletedAt.UTC(), input.GeneratedAt.UTC()
	earliest := release.CompletedAt.UTC()
	if change.GeneratedAt.UTC().After(earliest) {
		earliest = change.GeneratedAt.UTC()
	}
	if started.IsZero() || completed.IsZero() || generated.IsZero() || started.Before(earliest) || completed.Before(started) || completed.Sub(started) > maximumReviewSpan || generated.Before(completed) || generated.After(now) || generated.Before(now.Add(-maximumCollectionAge)) {
		return Receipt{}, errors.New("retrieval risk review timeline is invalid")
	}
	if input.TenantCount != 2 || input.CaseCount < 1 || input.CaseCount > maximumCount || input.TimingSampleCountPerClass < 1 || input.TimingSampleCountPerClass > maximumCount || !validCount(input.ResultLeakCount) || !validCount(input.CountLeakCount) || !validCount(input.CacheLeakCount) || input.MaximumTimingDeltaMicroseconds <= 0 || input.MaximumTimingDeltaMicroseconds > maximumTimingMicroseconds || input.ObservedTimingDeltaMicroseconds < 0 || input.ObservedTimingDeltaMicroseconds > maximumTimingMicroseconds {
		return Receipt{}, errors.New("retrieval risk aggregate metrics are invalid")
	}
	domains, passed, failed, inconclusive, findings, err := validateDomains(input.Domains)
	if err != nil {
		return Receipt{}, err
	}
	timingBreach := input.ObservedTimingDeltaMicroseconds > input.MaximumTimingDeltaMicroseconds
	if contradictsObservedLeak(domains, DomainResultIsolation, input.ResultLeakCount) || contradictsObservedLeak(domains, DomainCountConcealment, input.CountLeakCount) || contradictsObservedLeak(domains, DomainCacheNamespace, input.CacheLeakCount) || contradictsObservedTiming(domains, timingBreach) {
		return Receipt{}, errors.New("retrieval risk domain contradicts aggregate observation")
	}
	breaches := 0
	if input.ResultLeakCount > 0 {
		breaches++
	}
	if input.CountLeakCount > 0 {
		breaches++
	}
	if input.CacheLeakCount > 0 {
		breaches++
	}
	if timingBreach {
		breaches++
	}
	ready := passed == len(requiredDomains) && failed == 0 && inconclusive == 0 && findings == 0 && breaches == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("retrieval risk readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, ReviewID: input.ReviewID, CorpusVersion: input.CorpusVersion, TimingMethodVersion: input.TimingMethodVersion, ToleranceVersion: input.ToleranceVersion, InventoryID: input.InventoryID, InventoryReceiptSHA256: input.InventoryReceiptSHA256, PlanID: input.PlanID, PlanReceiptSHA256: input.PlanReceiptSHA256, ChangeID: input.ChangeID, ChangeReceiptSHA256: input.ChangeReceiptSHA256, ReleaseID: input.ReleaseID, ReleaseReceiptSHA256: releaseDigest, BlindCorpusSHA256: input.BlindCorpusSHA256, TimingReportSHA256: input.TimingReportSHA256, CacheReviewSHA256: input.CacheReviewSHA256, RiskToleranceDecisionSHA256: input.RiskToleranceDecisionSHA256, InputSHA256: inputDigest, ReviewStartedAt: started, ReviewCompletedAt: completed, GeneratedAt: generated, CollectedAt: now, TenantCount: input.TenantCount, CaseCount: input.CaseCount, TimingSampleCountPerClass: input.TimingSampleCountPerClass, ResultLeakCount: input.ResultLeakCount, CountLeakCount: input.CountLeakCount, CacheLeakCount: input.CacheLeakCount, MaximumTimingDeltaMicroseconds: input.MaximumTimingDeltaMicroseconds, ObservedTimingDeltaMicroseconds: input.ObservedTimingDeltaMicroseconds, Ready: ready, DomainCount: len(domains), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, FindingCount: findings, RiskBreachCount: breaches, Domains: domains}, nil
}

func validPassedRelease(release platformrollback.ReleaseReceipt, digest string) bool {
	return release.Schema == "agent-memory-kubernetes-release-receipt-v1" && release.Environment == "staging" && release.Namespace == "agent-memory-staging" && opaquePattern.MatchString(release.ReleaseID) && release.Outcome == "passed" && release.Migration.Outcome == "complete" && release.Rollouts.Outcome == "healthy" && !release.Rollback.Attempted && !release.Rollback.Succeeded && !release.StartedAt.IsZero() && !release.CompletedAt.Before(release.StartedAt) && digestPattern.MatchString(digest)
}
func validCount(value int) bool { return value >= 0 && value <= maximumCount }

func validateDomains(domains []Domain) ([]Domain, int, int, int, int, error) {
	if len(domains) != len(requiredDomains) {
		return nil, 0, 0, 0, 0, errors.New("retrieval risk domains are incomplete")
	}
	byID := make(map[DomainID]Domain, len(domains))
	passed, failed, inconclusive, findings := 0, 0, 0, 0
	for _, domain := range domains {
		if !digestPattern.MatchString(domain.EvidenceSHA256) || domain.FindingCount < 0 || domain.FindingCount > maximumCount {
			return nil, 0, 0, 0, 0, errors.New("retrieval risk domain is invalid")
		}
		switch domain.Outcome {
		case OutcomePassed:
			if domain.FindingCount != 0 {
				return nil, 0, 0, 0, 0, errors.New("passed retrieval risk domain contains findings")
			}
			passed++
		case OutcomeFailed:
			if domain.FindingCount == 0 {
				return nil, 0, 0, 0, 0, errors.New("failed retrieval risk domain has no finding")
			}
			failed++
		case OutcomeInconclusive:
			if domain.FindingCount != 0 {
				return nil, 0, 0, 0, 0, errors.New("inconclusive retrieval risk domain contains a known finding")
			}
			inconclusive++
		default:
			return nil, 0, 0, 0, 0, errors.New("retrieval risk outcome is invalid")
		}
		if _, duplicate := byID[domain.ID]; duplicate {
			return nil, 0, 0, 0, 0, errors.New("retrieval risk domain is duplicated")
		}
		byID[domain.ID] = domain
		findings += domain.FindingCount
	}
	ordered := make([]Domain, 0, len(requiredDomains))
	for _, id := range requiredDomains {
		domain, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, 0, errors.New("retrieval risk required domain is missing")
		}
		ordered = append(ordered, domain)
	}
	return ordered, passed, failed, inconclusive, findings, nil
}

func domainOutcome(domains []Domain, id DomainID) Outcome {
	for _, domain := range domains {
		if domain.ID == id {
			return domain.Outcome
		}
	}
	return ""
}
func contradictsObservedLeak(domains []Domain, id DomainID, leaks int) bool {
	outcome := domainOutcome(domains, id)
	if leaks > 0 {
		return outcome != OutcomeFailed
	}
	return outcome == ""
}
func contradictsObservedTiming(domains []Domain, breach bool) bool {
	outcome := domainOutcome(domains, DomainTimingAnalysis)
	if breach {
		return outcome != OutcomeFailed
	}
	return outcome == ""
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("retrieval risk input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("retrieval risk input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open retrieval risk input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("retrieval risk input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read retrieval risk input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("retrieval risk input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("retrieval risk input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retrieval risk input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("retrieval risk input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("retrieval risk receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("retrieval risk receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect retrieval risk receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-retrieval-risk-*")
}
