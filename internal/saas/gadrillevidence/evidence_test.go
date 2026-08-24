package gadrillevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/gascorecardevidence"
)

func TestEvaluateRequiresRepeatedUniqueDatedDrillsPerScenario(t *testing.T) {
	start := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	drills := passingDrills(start)
	results, passed, failed, inconclusive, err := evaluate(drills, start, start.Add(90*24*time.Hour))
	if err != nil || len(results) != 4 || passed != 8 || failed != 0 || inconclusive != 0 {
		t.Fatalf("results=%+v counts=%d/%d/%d err=%v", results, passed, failed, inconclusive, err)
	}
	for _, result := range results {
		if result.DrillCount != 2 || result.DistinctDateCount != 2 || !result.Ready {
			t.Fatalf("scenario not repeated: %+v", result)
		}
	}
}

func TestEvaluateRejectsReplayMissingAndOutsideWindow(t *testing.T) {
	start := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func([]Drill) []Drill{
		"missing repetition": func(values []Drill) []Drill { return values[1:] },
		"duplicate id":       func(values []Drill) []Drill { values[1].DrillID = values[0].DrillID; return values },
		"replayed digest":    func(values []Drill) []Drill { values[1].EvidenceSHA256 = values[0].EvidenceSHA256; return values },
		"same date": func(values []Drill) []Drill {
			values[1].StartedAt = values[0].StartedAt.Add(time.Hour)
			values[1].CompletedAt = values[1].StartedAt.Add(time.Hour)
			return values
		},
		"outside window": func(values []Drill) []Drill { values[0].StartedAt = start.Add(-time.Hour); return values },
	} {
		t.Run(name, func(t *testing.T) {
			values := append([]Drill(nil), passingDrills(start)...)
			values = mutate(values)
			if _, _, _, _, err := evaluate(values, start, start.Add(90*24*time.Hour)); err == nil {
				t.Fatal("invalid repeated drill set accepted")
			}
		})
	}
}

func TestEvaluatePreservesCompleteFailedSetAsUnready(t *testing.T) {
	start := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	drills := passingDrills(start)
	drills[0].Outcome = OutcomeFailed
	results, passed, failed, inconclusive, err := evaluate(drills, start, start.Add(90*24*time.Hour))
	if err != nil || passed != 7 || failed != 1 || inconclusive != 0 || results[0].Ready {
		t.Fatalf("failed drill set not preserved: %+v %d/%d/%d %v", results, passed, failed, inconclusive, err)
	}
}

func TestCollectBindsReadyScorecardAndPublishesReloadableReceipt(t *testing.T) {
	start := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	end := start.Add(90 * 24 * time.Hour)
	scorecardPath := readyScorecard(t, start, end)
	scorecardDigest := fileDigest(t, scorecardPath)
	digest := fmt.Sprintf("%064x", 100)
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest})
	}
	input := Input{
		Schema: InputSchemaV1, Classification: "production_external", Environment: "production",
		ReviewID: "ga-drills-2026", PolicyVersion: "ga-drill-policy-v1", ScorecardID: "scorecard-2026", ScorecardReceiptSHA256: scorecardDigest,
		InventoryID: "inventory", InventoryReceiptSHA256: digest, PlanID: "plan", PlanReceiptSHA256: digest,
		ChangeID: "change", ChangeReceiptSHA256: digest, ReleaseID: "release", ReleaseReceiptSHA256: digest,
		DrillManifestSHA256: digest, RepetitionPolicySHA256: digest, AccountableReviewSHA256: digest,
		GeneratedAt: end.Add(time.Hour), Ready: true, Drills: passingDrills(start), Checks: checks,
	}
	inputPath := writeJSON(t, "input.json", input)
	receipt, err := Collect(scorecardPath, inputPath, end.Add(2*time.Hour))
	if err != nil || !receipt.Ready || receipt.DrillCount != 8 || receipt.ScenarioCount != 4 {
		t.Fatalf("collect repeated drills: %+v err=%v", receipt, err)
	}
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadReady(receiptPath)
	if err != nil || loaded.ReviewID != input.ReviewID {
		t.Fatalf("load ready repeated drills: %+v err=%v", loaded, err)
	}
	input.ScorecardReceiptSHA256 = fmt.Sprintf("%064x", 101)
	if _, err := Collect(scorecardPath, writeJSON(t, "substitution.json", input), end.Add(2*time.Hour)); err == nil {
		t.Fatal("scorecard substitution accepted")
	}
}

func passingDrills(start time.Time) []Drill {
	drills := make([]Drill, 0, 8)
	sequence := 0
	for _, scenario := range requiredScenarios {
		for repetition := 0; repetition < 2; repetition++ {
			sequence++
			started := start.Add(time.Duration(sequence*48) * time.Hour)
			drills = append(drills, Drill{
				Scenario: scenario, DrillID: string(scenario) + "-" + string(rune('a'+repetition)),
				StartedAt: started, CompletedAt: started.Add(time.Hour), Outcome: OutcomePassed,
				EvidenceSHA256: fmt.Sprintf("%064x", sequence),
			})
		}
	}
	return drills
}

func readyScorecard(t *testing.T, start, end time.Time) string {
	t.Helper()
	digest := fmt.Sprintf("%064x", 100)
	values := map[gascorecardevidence.MetricID]int64{
		gascorecardevidence.MetricAPIAvailability: 999500, gascorecardevidence.MetricSearchP95: 700000,
		gascorecardevidence.MetricMemoryWriteP95: 250000, gascorecardevidence.MetricCriticalFindings: 0,
		gascorecardevidence.MetricTenantIsolation: 1, gascorecardevidence.MetricDeletionCompliance: 1000000,
		gascorecardevidence.MetricAuditIntegrity: 1000000, gascorecardevidence.MetricBillingReconciliation: 1000000,
		gascorecardevidence.MetricRestoreRPO: 4, gascorecardevidence.MetricRestoreRTO: 50,
		gascorecardevidence.MetricCostPerActiveTenant: 3000000, gascorecardevidence.MetricSupportResponse: 1000000,
		gascorecardevidence.MetricRetentionCompliance: 1000000,
	}
	targets := map[gascorecardevidence.MetricID]int64{
		gascorecardevidence.MetricAPIAvailability: 999000, gascorecardevidence.MetricSearchP95: 800000,
		gascorecardevidence.MetricMemoryWriteP95: 300000, gascorecardevidence.MetricCriticalFindings: 0,
		gascorecardevidence.MetricTenantIsolation: 1, gascorecardevidence.MetricDeletionCompliance: 1000000,
		gascorecardevidence.MetricAuditIntegrity: 1000000, gascorecardevidence.MetricBillingReconciliation: 1000000,
		gascorecardevidence.MetricRestoreRPO: 5, gascorecardevidence.MetricRestoreRTO: 60,
		gascorecardevidence.MetricCostPerActiveTenant: 4000000, gascorecardevidence.MetricSupportResponse: 1000000,
		gascorecardevidence.MetricRetentionCompliance: 1000000,
	}
	metrics := make([]gascorecardevidence.MetricObservation, 0, 13)
	results := make([]gascorecardevidence.MetricResult, 0, 13)
	for _, id := range gascorecardevidence.RequiredMetrics() {
		metric := gascorecardevidence.MetricObservation{ID: id, ObservedValue: values[id], ExpectedSampleCount: 10, ObservedSampleCount: 10, EvidenceSHA256: digest}
		comparator := "gte"
		if id == gascorecardevidence.MetricSearchP95 || id == gascorecardevidence.MetricMemoryWriteP95 || id == gascorecardevidence.MetricCriticalFindings || id == gascorecardevidence.MetricRestoreRPO || id == gascorecardevidence.MetricRestoreRTO || id == gascorecardevidence.MetricCostPerActiveTenant {
			comparator = "lte"
		}
		metrics = append(metrics, metric)
		results = append(results, gascorecardevidence.MetricResult{MetricObservation: metric, Comparator: comparator, TargetValue: targets[id], CoverageComplete: true, Passed: true})
	}
	checks := make([]gascorecardevidence.Check, 0, 7)
	for _, id := range gascorecardevidence.RequiredChecks() {
		checks = append(checks, gascorecardevidence.Check{ID: id, Outcome: gascorecardevidence.OutcomePassed, EvidenceSHA256: digest})
	}
	receipt := gascorecardevidence.Receipt{
		Input:  gascorecardevidence.Input{Classification: "production_external", Environment: "production", ScorecardID: "scorecard-2026", MetricSourceVersion: "metrics", QueryManifestVersion: "queries", TargetVersion: "targets", WindowDecisionVersion: "window", InventoryID: "inventory", InventoryReceiptSHA256: digest, PlanID: "plan", PlanReceiptSHA256: digest, ChangeID: "change", ChangeReceiptSHA256: digest, ReleaseID: "release", ReleaseReceiptSHA256: digest, ScorecardExportSHA256: digest, QueryManifestSHA256: digest, WindowDecisionSHA256: digest, TargetDecisionSHA256: digest, ProductDomainReviewSHA256: digest, WindowApprovedAt: start.Add(-time.Hour), WindowStart: start, WindowEnd: end, EvaluatedAt: end, GeneratedAt: end.Add(time.Hour), ApprovedCostPerActiveTenantMicroUSD: 4000000, Ready: true, Metrics: metrics, Checks: checks},
		Schema: gascorecardevidence.ReceiptSchemaV1, InputSHA256: digest, CollectedAt: end.Add(2 * time.Hour), ObservationDurationSeconds: int64(end.Sub(start) / time.Second), CoverageComplete: true, RetentionPassed: true, MetricResults: results, CheckCount: 7, PassedCount: 7,
	}
	return writeJSON(t, "scorecard.json", receipt)
}

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
