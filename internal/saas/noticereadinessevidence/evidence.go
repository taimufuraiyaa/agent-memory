// Package noticereadinessevidence normalizes content-free P6.5-A and CP6-A
// legal, routing, staffing, and tabletop evidence without approving either
// external control.
package noticereadinessevidence

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
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchscopeevidence"
)

const (
	maximumInputBytes = 256 << 10
	maximumAge        = 24 * time.Hour
	maximumCount      = 1_000_000_000
)

var (
	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
)

func Collect(launchScopeReceiptPath, inputPath string, now time.Time) (Receipt, error) {
	scope, scopeDigest, err := launchscopeevidence.LoadReady(launchScopeReceiptPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load ready launch-scope receipt: %w", err)
	}
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(scope, scopeDigest, input, inputDigest, now)
}

type routeSummary struct {
	covered, copyCovered, deadlinesReady, escalationsReady int
}

type scenarioSummary struct {
	passed, failed, inconclusive int
}

func build(scope launchscopeevidence.Receipt, scopeDigest string, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if input.Schema != InputSchemaV1 || input.Classification != "external_business" || input.Environment != "external" || !scope.Ready ||
		!allOpaque(input.ReviewID, input.WorkflowPolicyVersion, input.CopyManifestVersion, input.RoutingPolicyVersion, input.DeadlinePolicyVersion, input.EscalationPolicyVersion, input.RepeatAbusePolicyVersion, input.TabletopVersion, input.StaffingPlanVersion) ||
		input.ScopeDecisionID != scope.ScopeDecisionID || input.LaunchScopeReceiptSHA256 != scopeDigest ||
		scope.NoticeJurisdictionCount < 1 || scope.SupportLanguageCount < 1 ||
		!allDigests(scopeDigest, inputDigest) {
		return Receipt{}, errors.New("notice-readiness identity or launch-scope binding is invalid")
	}
	digests := map[string]string{
		"workflow_policy": input.WorkflowPolicySHA256, "copy_manifest": input.CopyManifestSHA256,
		"routing_policy": input.RoutingPolicySHA256, "deadline_policy": input.DeadlinePolicySHA256,
		"escalation_policy": input.EscalationPolicySHA256, "repeat_abuse_policy": input.RepeatAbusePolicySHA256,
		"tabletop_report": input.TabletopReportSHA256, "staffing_plan": input.StaffingPlanSHA256,
		"counsel_review": input.CounselReviewSHA256, "legal_operations_review": input.LegalOperationsReviewSHA256,
		"support_review": input.SupportReviewSHA256,
	}
	for _, digest := range digests {
		if !digestPattern.MatchString(digest) {
			return Receipt{}, errors.New("notice-readiness evidence digest is invalid")
		}
	}
	if now.IsZero() {
		return Receipt{}, errors.New("notice-readiness collection time is invalid")
	}
	now, reviewed, generated := now.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if reviewed.IsZero() || generated.IsZero() || reviewed.Before(scope.CollectedAt.UTC()) || generated.Before(reviewed) || generated.After(now) || generated.Before(now.Add(-maximumAge)) {
		return Receipt{}, errors.New("notice-readiness evidence timeline is invalid")
	}
	routes, routeTotals, err := validateRoutes(input.Routes, scope.NoticeJurisdictionCount, scope.SupportLanguageCount)
	if err != nil {
		return Receipt{}, err
	}
	staffing, coveredStaffing, err := validateStaffing(input.Staffing)
	if err != nil {
		return Receipt{}, err
	}
	scenarios, scenarioTotals, err := validateScenarios(input.Scenarios)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	expected := map[CheckID]Outcome{
		CheckLaunchScopeReady:     OutcomePassed,
		CheckJurisdictionRouting:  OutcomePassed,
		CheckCopyLanguageCoverage: outcome(routeTotals.copyCovered == len(routes)),
		CheckDeadlinePolicy:       outcome(routeTotals.deadlinesReady == len(routes)),
		CheckEscalationPaths:      outcome(routeTotals.escalationsReady == len(routes)),
		CheckStaffingCoverage:     outcome(coveredStaffing == len(requiredStaffing)),
		CheckTabletopScenarios:    aggregateOutcome(scenarioTotals.passed, scenarioTotals.failed, scenarioTotals.inconclusive, len(requiredScenarios)),
	}
	for id, want := range expected {
		if outcomeFor(checks, id) != want {
			return Receipt{}, errors.New("notice-readiness check contradicts derived evidence")
		}
	}
	ready := routeTotals.covered == len(routes) && coveredStaffing == len(requiredStaffing) && scenarioTotals.passed == len(requiredScenarios) && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("notice-readiness readiness contradicts evidence")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		ReviewID: input.ReviewID, WorkflowPolicyVersion: input.WorkflowPolicyVersion, CopyManifestVersion: input.CopyManifestVersion,
		RoutingPolicyVersion: input.RoutingPolicyVersion, DeadlinePolicyVersion: input.DeadlinePolicyVersion,
		EscalationPolicyVersion: input.EscalationPolicyVersion, RepeatAbusePolicyVersion: input.RepeatAbusePolicyVersion,
		TabletopVersion: input.TabletopVersion, StaffingPlanVersion: input.StaffingPlanVersion,
		ScopeDecisionID: input.ScopeDecisionID, LaunchScopeReceiptSHA256: scopeDigest, InputSHA256: inputDigest,
		ScopeDecisionVersion: scope.ScopeDecisionVersion, JurisdictionPolicyVersion: scope.JurisdictionPolicyVersion,
		LegalReviewVersion: scope.LegalReviewVersion, SupportLanguageCount: scope.SupportLanguageCount,
		NoticeJurisdictionCount: scope.NoticeJurisdictionCount, ReviewedAt: reviewed, GeneratedAt: generated, CollectedAt: now,
		Ready: ready, RouteCount: len(routes), CoveredRouteCount: routeTotals.covered,
		StaffingDomainCount: len(staffing), CoveredStaffingDomainCount: coveredStaffing,
		ScenarioCount: len(scenarios), PassedScenarioCount: scenarioTotals.passed, FailedScenarioCount: scenarioTotals.failed,
		InconclusiveScenarioCount: scenarioTotals.inconclusive, CheckCount: len(checks), PassedCount: passed,
		FailedCount: failed, InconclusiveCount: inconclusive, EvidenceDigests: digests,
		Routes: routes, Staffing: staffing, Scenarios: scenarios, Checks: checks,
	}, nil
}

func validateRoutes(values []Route, expectedCount, approvedLanguageCount int) ([]Route, routeSummary, error) {
	if len(values) != expectedCount || len(values) > maximumCount {
		return nil, routeSummary{}, errors.New("notice-readiness route set is incomplete")
	}
	seen := make(map[string]struct{}, len(values))
	ordered := append([]Route(nil), values...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].JurisdictionRefSHA256 < ordered[j].JurisdictionRefSHA256
	})
	summary := routeSummary{}
	for _, value := range ordered {
		if !allDigests(value.JurisdictionRefSHA256, value.CopySHA256, value.RoutingSHA256, value.DeadlinePolicySHA256, value.EscalationSHA256) {
			return nil, routeSummary{}, errors.New("notice-readiness route digest is invalid")
		}
		if _, duplicate := seen[value.JurisdictionRefSHA256]; duplicate {
			return nil, routeSummary{}, errors.New("notice-readiness jurisdiction route is duplicated")
		}
		seen[value.JurisdictionRefSHA256] = struct{}{}
		for _, count := range []int{value.RequiredLanguageCount, value.CoveredLanguageCount, value.NormalValidationDeadlineSeconds, value.UrgentValidationDeadlineSeconds, value.PrimaryEscalationPathCount, value.BackupEscalationPathCount} {
			if count < 0 || count > maximumCount {
				return nil, routeSummary{}, errors.New("notice-readiness route aggregate is invalid")
			}
		}
		if value.RequiredLanguageCount == 0 || value.NormalValidationDeadlineSeconds == 0 || value.UrgentValidationDeadlineSeconds == 0 {
			return nil, routeSummary{}, errors.New("notice-readiness route requirement is invalid")
		}
		if value.RequiredLanguageCount > approvedLanguageCount || value.CoveredLanguageCount > approvedLanguageCount {
			return nil, routeSummary{}, errors.New("notice-readiness route exceeds approved language scope")
		}
		copyReady := value.CoveredLanguageCount >= value.RequiredLanguageCount
		deadlineReady := value.UrgentValidationDeadlineSeconds <= value.NormalValidationDeadlineSeconds
		escalationReady := value.PrimaryEscalationPathCount > 0 && value.BackupEscalationPathCount > 0
		derived := outcome(copyReady && deadlineReady && escalationReady)
		if value.Outcome != derived {
			return nil, routeSummary{}, errors.New("notice-readiness route outcome contradicts coverage")
		}
		if copyReady {
			summary.copyCovered++
		}
		if deadlineReady {
			summary.deadlinesReady++
		}
		if escalationReady {
			summary.escalationsReady++
		}
		if derived == OutcomePassed {
			summary.covered++
		}
	}
	return ordered, summary, nil
}

func validateStaffing(values []StaffingDomain) ([]StaffingDomain, int, error) {
	if len(values) != len(requiredStaffing) {
		return nil, 0, errors.New("notice-readiness staffing domains are incomplete")
	}
	byID := make(map[StaffingDomainID]StaffingDomain, len(values))
	covered := 0
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, errors.New("notice-readiness staffing domain is duplicated")
		}
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, errors.New("notice-readiness staffing digest is invalid")
		}
		for _, count := range []int{value.RequiredCoverageMinutes, value.PrimaryCoveredMinutes, value.BackupCoveredMinutes, value.PrimarySlotCount, value.BackupSlotCount} {
			if count < 0 || count > maximumCount {
				return nil, 0, errors.New("notice-readiness staffing aggregate is invalid")
			}
		}
		if value.RequiredCoverageMinutes == 0 {
			return nil, 0, errors.New("notice-readiness staffing requirement is invalid")
		}
		ready := value.PrimaryCoveredMinutes >= value.RequiredCoverageMinutes && value.BackupCoveredMinutes >= value.RequiredCoverageMinutes && value.PrimarySlotCount > 0 && value.BackupSlotCount > 0
		if value.Outcome != outcome(ready) {
			return nil, 0, errors.New("notice-readiness staffing outcome contradicts coverage")
		}
		if ready {
			covered++
		}
		byID[value.ID] = value
	}
	ordered := make([]StaffingDomain, 0, len(requiredStaffing))
	for _, id := range requiredStaffing {
		value, exists := byID[id]
		if !exists {
			return nil, 0, errors.New("required notice-readiness staffing domain is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, covered, nil
}

func validateScenarios(values []TabletopScenario) ([]TabletopScenario, scenarioSummary, error) {
	if len(values) != len(requiredScenarios) {
		return nil, scenarioSummary{}, errors.New("notice-readiness tabletop scenarios are incomplete")
	}
	byID := make(map[ScenarioID]TabletopScenario, len(values))
	summary := scenarioSummary{}
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, scenarioSummary{}, errors.New("notice-readiness tabletop scenario is duplicated")
		}
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, scenarioSummary{}, errors.New("notice-readiness tabletop digest is invalid")
		}
		for _, count := range []int{value.ExecutedCount, value.PassedCount, value.FailedCount, value.InconclusiveCount, value.MaximumTargetSeconds, value.MaximumObservedSeconds} {
			if count < 0 || count > maximumCount {
				return nil, scenarioSummary{}, errors.New("notice-readiness tabletop aggregate is invalid")
			}
		}
		if value.ExecutedCount == 0 || value.MaximumTargetSeconds == 0 || value.PassedCount > value.ExecutedCount || value.FailedCount > value.ExecutedCount || value.InconclusiveCount > value.ExecutedCount || value.ExecutedCount != value.PassedCount+value.FailedCount+value.InconclusiveCount {
			return nil, scenarioSummary{}, errors.New("notice-readiness tabletop reconciliation is invalid")
		}
		derived := OutcomePassed
		if value.FailedCount > 0 || value.MaximumObservedSeconds > value.MaximumTargetSeconds {
			derived = OutcomeFailed
		} else if value.InconclusiveCount > 0 || value.PassedCount != value.ExecutedCount {
			derived = OutcomeInconclusive
		}
		if value.Outcome != derived {
			return nil, scenarioSummary{}, errors.New("notice-readiness tabletop outcome contradicts observations")
		}
		switch derived {
		case OutcomePassed:
			summary.passed++
		case OutcomeFailed:
			summary.failed++
		case OutcomeInconclusive:
			summary.inconclusive++
		}
		byID[value.ID] = value
	}
	ordered := make([]TabletopScenario, 0, len(requiredScenarios))
	for _, id := range requiredScenarios {
		value, exists := byID[id]
		if !exists {
			return nil, scenarioSummary{}, errors.New("required notice-readiness tabletop scenario is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, summary, nil
}

func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("notice-readiness checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(values))
	passed, failed, inconclusive := 0, 0, 0
	for _, value := range values {
		if _, duplicate := byID[value.ID]; duplicate {
			return nil, 0, 0, 0, errors.New("notice-readiness check is duplicated")
		}
		if !digestPattern.MatchString(value.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("notice-readiness check digest is invalid")
		}
		switch value.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("notice-readiness check outcome is invalid")
		}
		byID[value.ID] = value
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		value, exists := byID[id]
		if !exists {
			return nil, 0, 0, 0, errors.New("required notice-readiness check is missing")
		}
		ordered = append(ordered, value)
	}
	return ordered, passed, failed, inconclusive, nil
}

func aggregateOutcome(passed, failed, inconclusive, total int) Outcome {
	if failed > 0 {
		return OutcomeFailed
	}
	if inconclusive > 0 || passed != total {
		return OutcomeInconclusive
	}
	return OutcomePassed
}

func outcome(value bool) Outcome {
	if value {
		return OutcomePassed
	}
	return OutcomeFailed
}

func outcomeFor(values []Check, id CheckID) Outcome {
	for _, value := range values {
		if value.ID == id {
			return value.Outcome
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
		return "", errors.New("notice-readiness input path is required")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximumInputBytes {
		return "", errors.New("notice-readiness input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open notice-readiness input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		return "", errors.New("notice-readiness input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read notice-readiness input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("notice-readiness input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("notice-readiness input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("notice-readiness input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("notice-readiness input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("notice-readiness receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("notice-readiness receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect notice-readiness receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-notice-readiness-*")
}
