package noticereadinessevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/launchscopeevidence"
)

func TestSchemasCoverExactContentFreeInputAndReceiptShapes(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	scope, input := readyEvidence(now)
	receipt, err := build(scope, digest(90), input, digest(91), now)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range [][]byte{[]byte(`"name"`), []byte(`"claimant"`), []byte(`"tenant"`), []byte(`"account"`), []byte(`"source"`), []byte(`"case"`), []byte(`"contact"`), []byte(`"endpoint"`), []byte(`"path"`), []byte(`"signature"`), []byte(`"key"`), []byte(`"evidence_ref"`)} {
		if bytes.Contains(contents, prohibited) {
			t.Fatalf("receipt contains prohibited field %s", prohibited)
		}
	}
	assertSchemaCoversObject(t, input, "launch-notice-readiness-input.schema.json")
	assertSchemaCoversObject(t, receipt, "launch-notice-readiness-receipt.schema.json")
}

func TestCollectRevalidatesAndBindsExactPublishedLaunchScopeReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	scopeInput := launchscopeevidence.Input{Schema: launchscopeevidence.InputSchemaV1, Classification: "external_business", Environment: "external", ScopeDecisionID: "scope-1", ScopeDecisionVersion: "scope-v1", JurisdictionPolicyVersion: "jurisdiction-v1", LegalReviewVersion: "legal-v1", RiskRegisterVersion: "risk-v1", DecisionRegisterSHA256: digest(1), LaunchScopeDecisionSHA256: digest(2), JurisdictionMemoSHA256: digest(3), PolicyManifestSHA256: digest(4), LegalReviewSHA256: digest(5), RiskRegisterSHA256: digest(6), ScopeApprovedAt: now.Add(-6 * time.Hour), LegalReviewCompletedAt: now.Add(-6 * time.Hour), GeneratedAt: now.Add(-5 * time.Hour), LaunchCountryCount: 2, MinimumAgeYears: 18, SupportLanguageCount: 1, NoticeJurisdictionCount: 2, Ready: true}
	for index, id := range launchscopeevidence.RequiredLegalPositions() {
		scopeInput.LegalPositions = append(scopeInput.LegalPositions, launchscopeevidence.LegalPosition{ID: id, PolicyCopySHA256: digest(100 + index), ReviewEvidenceSHA256: digest(110 + index), Outcome: launchscopeevidence.OutcomePassed})
	}
	for index, id := range launchscopeevidence.RequiredChecks() {
		scopeInput.Checks = append(scopeInput.Checks, launchscopeevidence.Check{ID: id, Outcome: launchscopeevidence.OutcomePassed, EvidenceSHA256: digest(120 + index)})
	}
	scopeReceipt, err := launchscopeevidence.Collect(writeJSON(t, "scope-input.json", scopeInput), now.Add(-4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	scopePath := filepath.Join(t.TempDir(), "scope-receipt.json")
	if err := launchscopeevidence.Publish(scopePath, scopeReceipt); err != nil {
		t.Fatal(err)
	}

	_, input := readyEvidence(now)
	input.LaunchScopeReceiptSHA256 = fileDigest(t, scopePath)
	input.ReviewedAt = now.Add(-3 * time.Hour)
	receipt, err := Collect(scopePath, writeJSON(t, "notice-input.json", input), now)
	if err != nil || !receipt.Ready || receipt.LaunchScopeReceiptSHA256 != fileDigest(t, scopePath) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}

	tampered := scopeReceipt
	tampered.PassedCount--
	if _, err := Collect(writeJSON(t, "tampered-scope.json", tampered), writeJSON(t, "unused-input.json", input), now); err == nil {
		t.Fatal("tampered prerequisite receipt accepted")
	}
}

func TestStrictInputReaderRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema":"agent-memory-launch-notice-readiness-input-v1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var input Input
	if _, err := decodeStrictRegular(unknown, &input); err == nil {
		t.Fatal("unknown field accepted")
	}
	safe := filepath.Join(t.TempDir(), "safe.json")
	if err := os.WriteFile(safe, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "input-link.json")
	if err := os.Symlink(safe, link); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStrictRegular(link, &input); err == nil {
		t.Fatal("symlink input accepted")
	}
}

func TestBuildNormalizesReadyNoticeLegalAndStaffingEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	scope, input := readyEvidence(now)
	receipt, err := build(scope, digest(90), input, digest(91), now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.RouteCount != 2 || receipt.CoveredRouteCount != 2 || receipt.StaffingDomainCount != 3 || receipt.CoveredStaffingDomainCount != 3 || receipt.ScenarioCount != 4 || receipt.PassedScenarioCount != 4 || receipt.CheckCount != 10 || receipt.PassedCount != 10 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestBuildOrdersDynamicJurisdictionRoutesByHashedReference(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	scope, input := readyEvidence(now)
	input.Routes[0], input.Routes[1] = input.Routes[1], input.Routes[0]
	receipt, err := build(scope, digest(90), input, digest(91), now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Routes[0].JurisdictionRefSHA256 != digest(10) {
		t.Fatalf("routes are not canonical: %+v", receipt.Routes)
	}
}

func TestBuildAcceptsZeroSecondObservedTabletopWithinPositiveTarget(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	scope, input := readyEvidence(now)
	input.Scenarios[0].MaximumObservedSeconds = 0
	receipt, err := build(scope, digest(90), input, digest(91), now)
	if err != nil || !receipt.Ready {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildPreservesCompleteAdverseEvidenceAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	scope, input := readyEvidence(now)
	input.Routes[0].CoveredLanguageCount = 0
	input.Routes[0].Outcome = OutcomeFailed
	setCheck(&input, CheckCopyLanguageCoverage, OutcomeFailed)
	input.Scenarios[1].PassedCount = 0
	input.Scenarios[1].FailedCount = 1
	input.Scenarios[1].Outcome = OutcomeFailed
	setCheck(&input, CheckTabletopScenarios, OutcomeFailed)
	input.Ready = false
	receipt, err := build(scope, digest(90), input, digest(91), now)
	if err != nil || receipt.Ready || receipt.CoveredRouteCount != 1 || receipt.FailedScenarioCount != 1 || receipt.FailedCount != 2 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestBuildRejectsIncompleteSubstitutedAndContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Input){
		"classification":      func(v *Input) { v.Classification = "local_development" },
		"scope digest":        func(v *Input) { v.LaunchScopeReceiptSHA256 = digest(99) },
		"bad digest":          func(v *Input) { v.CounselReviewSHA256 = "bad" },
		"stale":               func(v *Input) { v.GeneratedAt = now.Add(-25 * time.Hour) },
		"pre-scope review":    func(v *Input) { v.ReviewedAt = now.Add(-4 * time.Hour) },
		"missing route":       func(v *Input) { v.Routes = v.Routes[:1] },
		"duplicate route":     func(v *Input) { v.Routes[1].JurisdictionRefSHA256 = v.Routes[0].JurisdictionRefSHA256 },
		"route contradiction": func(v *Input) { v.Routes[0].CoveredLanguageCount = 0 },
		"scope language substitution": func(v *Input) {
			v.Routes[0].RequiredLanguageCount = 2
			v.Routes[0].CoveredLanguageCount = 2
		},
		"missing staffing":        func(v *Input) { v.Staffing = v.Staffing[:2] },
		"staffing contradiction":  func(v *Input) { v.Staffing[0].BackupSlotCount = 0 },
		"scenario arithmetic":     func(v *Input) { v.Scenarios[0].ExecutedCount++ },
		"missing check":           func(v *Input) { v.Checks = v.Checks[:9] },
		"readiness contradiction": func(v *Input) { v.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			scope, input := readyEvidence(now)
			mutate(&input)
			if _, err := build(scope, digest(90), input, digest(91), now); err == nil {
				t.Fatal("unsafe notice evidence accepted")
			}
		})
	}
}

func TestPublishCreatesPrivateReceiptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, Receipt{Schema: ReceiptSchemaV1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	if err := Publish(path, Receipt{}); err == nil {
		t.Fatal("receipt overwrite accepted")
	}
}

func readyEvidence(now time.Time) (launchscopeevidence.Receipt, Input) {
	scope := launchscopeevidence.Receipt{Ready: true, ScopeDecisionID: "scope-1", ScopeDecisionVersion: "scope-v1", JurisdictionPolicyVersion: "jurisdiction-v1", LegalReviewVersion: "legal-v1", CollectedAt: now.Add(-3 * time.Hour), SupportLanguageCount: 1, NoticeJurisdictionCount: 2}
	routes := []Route{
		{JurisdictionRefSHA256: digest(10), RequiredLanguageCount: 1, CoveredLanguageCount: 1, NormalValidationDeadlineSeconds: 172800, UrgentValidationDeadlineSeconds: 14400, PrimaryEscalationPathCount: 1, BackupEscalationPathCount: 1, CopySHA256: digest(11), RoutingSHA256: digest(12), DeadlinePolicySHA256: digest(13), EscalationSHA256: digest(14), Outcome: OutcomePassed},
		{JurisdictionRefSHA256: digest(15), RequiredLanguageCount: 1, CoveredLanguageCount: 1, NormalValidationDeadlineSeconds: 172800, UrgentValidationDeadlineSeconds: 14400, PrimaryEscalationPathCount: 1, BackupEscalationPathCount: 1, CopySHA256: digest(16), RoutingSHA256: digest(17), DeadlinePolicySHA256: digest(18), EscalationSHA256: digest(19), Outcome: OutcomePassed},
	}
	staffing := make([]StaffingDomain, 0, 3)
	for index, id := range RequiredStaffingDomains() {
		staffing = append(staffing, StaffingDomain{ID: id, RequiredCoverageMinutes: 10080, PrimaryCoveredMinutes: 10080, BackupCoveredMinutes: 10080, PrimarySlotCount: 2, BackupSlotCount: 2, Outcome: OutcomePassed, EvidenceSHA256: digest(20 + index)})
	}
	scenarios := make([]TabletopScenario, 0, 4)
	for index, id := range RequiredScenarios() {
		scenarios = append(scenarios, TabletopScenario{ID: id, ExecutedCount: 1, PassedCount: 1, MaximumTargetSeconds: 3600, MaximumObservedSeconds: 600, Outcome: OutcomePassed, EvidenceSHA256: digest(30 + index)})
	}
	checks := make([]Check, 0, 10)
	for index, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(40 + index)})
	}
	return scope, Input{Schema: InputSchemaV1, Classification: "external_business", Environment: "external", ReviewID: "notice-review-1", WorkflowPolicyVersion: "workflow-v1", CopyManifestVersion: "copy-v1", RoutingPolicyVersion: "routing-v1", DeadlinePolicyVersion: "deadline-v1", EscalationPolicyVersion: "escalation-v1", RepeatAbusePolicyVersion: "repeat-v1", TabletopVersion: "tabletop-v1", StaffingPlanVersion: "staffing-v1", ScopeDecisionID: scope.ScopeDecisionID, LaunchScopeReceiptSHA256: digest(90), WorkflowPolicySHA256: digest(1), CopyManifestSHA256: digest(2), RoutingPolicySHA256: digest(3), DeadlinePolicySHA256: digest(4), EscalationPolicySHA256: digest(5), RepeatAbusePolicySHA256: digest(6), TabletopReportSHA256: digest(7), StaffingPlanSHA256: digest(8), CounselReviewSHA256: digest(9), LegalOperationsReviewSHA256: digest(50), SupportReviewSHA256: digest(51), ReviewedAt: now.Add(-2 * time.Hour), GeneratedAt: now.Add(-time.Hour), Ready: true, Routes: routes, Staffing: staffing, Scenarios: scenarios, Checks: checks}
}

func setCheck(input *Input, id CheckID, result Outcome) {
	for index := range input.Checks {
		if input.Checks[index].ID == id {
			input.Checks[index].Outcome = result
		}
	}
}
func digest(value int) string { return fmt.Sprintf("%064x", value) }

func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func assertSchemaCoversObject(t *testing.T, value any, schemaName string) {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(contents, &object); err != nil {
		t.Fatal(err)
	}
	schemaContents, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "evidence", "v1", schemaName))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(schemaContents, &schema); err != nil {
		t.Fatal(err)
	}
	if len(object) != len(schema.Required) || len(schema.Properties) != len(schema.Required) {
		t.Fatalf("%s object=%d required=%d properties=%d", schemaName, len(object), len(schema.Required), len(schema.Properties))
	}
	for _, key := range schema.Required {
		if _, ok := object[key]; !ok {
			t.Fatalf("%s missing property %q", schemaName, key)
		}
	}
}
