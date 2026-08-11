package migrationacceptanceevidence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/migrationcohortevidence"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/parityevidence"
)

func TestCollectBindsReadyCohortParityAndTabletop(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	cohort := readyCohort(now)
	parity := readyParity(now)
	cohortPath := writeJSON(t, "cohort.json", cohort)
	parityPath := writeJSON(t, "parity.json", parity)
	input := readyAcceptance(now, cohort, digestFile(t, cohortPath), parity, digestFile(t, parityPath))
	inputPath := writeJSON(t, "acceptance.json", input)
	receipt, err := Collect(cohortPath, parityPath, inputPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.CheckCount != 8 || receipt.PassedCount != 8 || receipt.InputSHA256 != digestFile(t, inputPath) || receipt.EvidenceBundleSHA256 == "" {
		t.Fatalf("receipt=%+v", receipt)
	}
}

func TestCollectPreservesFailedTabletopAsValidUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	cohort := readyCohort(now)
	parity := readyParity(now)
	cohortPath := writeJSON(t, "cohort.json", cohort)
	parityPath := writeJSON(t, "parity.json", parity)
	input := readyAcceptance(now, cohort, digestFile(t, cohortPath), parity, digestFile(t, parityPath))
	input.Ready = false
	input.Checks[2].Outcome = OutcomeFailed
	receipt, err := Collect(cohortPath, parityPath, writeJSON(t, "unready.json", input), now)
	if err != nil || receipt.Ready || receipt.FailedCount != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestCollectRejectsSubstitutionUnsafeAndContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	cohort := readyCohort(now)
	parity := readyParity(now)
	cohortPath := writeJSON(t, "cohort.json", cohort)
	parityPath := writeJSON(t, "parity.json", parity)
	base := readyAcceptance(now, cohort, digestFile(t, cohortPath), parity, digestFile(t, parityPath))
	for name, mutate := range map[string]func(*Input){
		"dataset substitution":      func(value *Input) { value.DatasetVersion = "other-dataset" },
		"receipt substitution":      func(value *Input) { value.CohortReceiptSHA256 = digest(90) },
		"pre-prerequisite tabletop": func(value *Input) { value.StartedAt = now.Add(-3 * time.Hour) },
		"overlong tabletop":         func(value *Input) { value.CompletedAt = value.StartedAt.Add(5 * time.Hour) },
		"stale input":               func(value *Input) { value.GeneratedAt = now.Add(-25 * time.Hour) },
		"missing check":             func(value *Input) { value.Checks = value.Checks[:7] },
		"duplicate check":           func(value *Input) { value.Checks[7].ID = value.Checks[0].ID },
		"contradictory readiness":   func(value *Input) { value.Ready = false },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Checks = append([]Check(nil), base.Checks...)
			mutate(&input)
			if _, err := Collect(cohortPath, parityPath, writeJSON(t, name+".json", input), now); err == nil {
				t.Fatal("unsafe acceptance evidence accepted")
			}
		})
	}
	linked := filepath.Join(t.TempDir(), "input-link.json")
	inputPath := writeJSON(t, "safe.json", base)
	if err := os.Symlink(inputPath, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(cohortPath, parityPath, linked, now); err == nil {
		t.Fatal("symlink input accepted")
	}
}

func TestCollectRejectsPrerequisitesFromDifferentDatasetOrRelease(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*parityevidence.Receipt){
		"dataset": func(value *parityevidence.Receipt) { value.DatasetVersion = "other-dataset" },
		"release": func(value *parityevidence.Receipt) {
			value.ReleaseID = "other-release"
			value.ReleaseReceiptSHA256 = digest(89)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cohort := readyCohort(now)
			parity := readyParity(now)
			mutate(&parity)
			cohortPath := writeJSON(t, "cohort.json", cohort)
			parityPath := writeJSON(t, "parity.json", parity)
			input := readyAcceptance(now, cohort, digestFile(t, cohortPath), parity, digestFile(t, parityPath))
			if _, err := Collect(cohortPath, parityPath, writeJSON(t, "acceptance.json", input), now); err == nil {
				t.Fatal("mismatched prerequisite receipts accepted")
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

func readyAcceptance(now time.Time, cohort migrationcohortevidence.Receipt, cohortDigest string, parity parityevidence.Receipt, parityDigest string) Input {
	checks := make([]Check, 0, len(RequiredChecks()))
	for index, id := range RequiredChecks() {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: digest(index + 20)})
	}
	return Input{Schema: InputSchemaV1, Classification: "staging_external", Environment: "staging", AcceptanceID: "migration-acceptance-1", RollbackPlanVersion: "rollback-v1",
		CohortID: cohort.CohortID, CohortReceiptSHA256: cohortDigest, ParityEvaluationID: parity.EvaluationID, ParityReceiptSHA256: parityDigest,
		InventoryID: cohort.InventoryID, InventoryReceiptSHA256: cohort.InventoryReceiptSHA256, PlanID: cohort.PlanID, PlanReceiptSHA256: cohort.PlanReceiptSHA256,
		ChangeID: cohort.ChangeID, ChangeReceiptSHA256: cohort.ChangeReceiptSHA256, ReleaseID: cohort.ReleaseID, ReleaseReceiptSHA256: cohort.ReleaseReceiptSHA256, DatasetVersion: cohort.DatasetVersion,
		RollbackPlanSHA256: digest(10), TabletopReportSHA256: digest(11), AcceptanceDecisionSHA256: digest(12),
		StartedAt: now.Add(-45 * time.Minute), CompletedAt: now.Add(-30 * time.Minute), GeneratedAt: now.Add(-15 * time.Minute), Ready: true, Checks: checks}
}

func readyCohort(now time.Time) migrationcohortevidence.Receipt {
	checks := make([]migrationcohortevidence.Check, 0, len(migrationcohortevidence.RequiredChecks()))
	for _, id := range migrationcohortevidence.RequiredChecks() {
		checks = append(checks, migrationcohortevidence.Check{ID: id, Outcome: migrationcohortevidence.OutcomePassed, EvidenceSHA256: digest(1)})
	}
	return migrationcohortevidence.Receipt{Schema: migrationcohortevidence.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", CohortID: "internal-cohort-1", DatasetVersion: "representative-v1", ConsentVersion: "consent-v1", ImporterVersion: "ampb2-v2",
		InventoryID: "staging-inventory", InventoryReceiptSHA256: digest(2), PlanID: "staging-plan", PlanReceiptSHA256: digest(3), ChangeID: "staging-change", ChangeReceiptSHA256: digest(4), ReleaseID: "staging-release", ReleaseReceiptSHA256: digest(5), CohortDecisionSHA256: digest(6), CohortReportSHA256: digest(7), InputSHA256: digest(8),
		ConsentApprovedAt: now.Add(-24 * time.Hour), StartedAt: now.Add(-4 * time.Hour), CompletedAt: now.Add(-3 * time.Hour), GeneratedAt: now.Add(-150 * time.Minute), CollectedAt: now.Add(-2 * time.Hour),
		AccountCount: 3, LibraryCount: 4, SourceCount: 12, MemoryCount: 20, NoteCount: 8, ExpectedItemCount: 40, ImportedItemCount: 32, MergedItemCount: 5, SkippedItemCount: 3,
		FormatCoverageComplete: true, SizeCoverageComplete: true, ReconciliationComplete: true, Ready: true, CheckCount: 9, PassedCount: 9,
		Formats:     []migrationcohortevidence.FormatCoverage{{Format: migrationcohortevidence.FormatPDF, SourceCount: 4}, {Format: migrationcohortevidence.FormatEPUB, SourceCount: 3}, {Format: migrationcohortevidence.FormatMarkdown, SourceCount: 3}, {Format: migrationcohortevidence.FormatText, SourceCount: 2}},
		SizeBuckets: []migrationcohortevidence.SizeCoverage{{Bucket: migrationcohortevidence.SizeSmall, SourceCount: 5}, {Bucket: migrationcohortevidence.SizeMedium, SourceCount: 4}, {Bucket: migrationcohortevidence.SizeLarge, SourceCount: 3}}, Checks: checks}
}

func readyParity(now time.Time) parityevidence.Receipt {
	checks := make([]parityevidence.Check, 0, len(parityevidence.RequiredChecks()))
	for _, id := range parityevidence.RequiredChecks() {
		checks = append(checks, parityevidence.Check{ID: id, Outcome: parityevidence.OutcomePassed, EvidenceSHA256: digest(1)})
	}
	return parityevidence.Receipt{Schema: parityevidence.ReceiptSchemaV1, Classification: "staging_external", Environment: "staging", EvaluationID: "parity-evaluation-1", ThresholdVersion: "threshold-v1", DatasetVersion: "representative-v1",
		InventoryID: "staging-inventory", InventoryReceiptSHA256: digest(2), PlanID: "staging-plan", PlanReceiptSHA256: digest(3), ChangeID: "staging-change", ChangeReceiptSHA256: digest(4), ReleaseID: "staging-release", ReleaseReceiptSHA256: digest(5), ThresholdDecisionSHA256: digest(9), ParityReportSHA256: digest(10), InputSHA256: digest(11),
		ThresholdApprovedAt: now.Add(-5 * time.Hour), EvaluationStartedAt: now.Add(-4 * time.Hour), EvaluationCompletedAt: now.Add(-3 * time.Hour), GeneratedAt: now.Add(-150 * time.Minute), CollectedAt: now.Add(-2 * time.Hour), CaseCount: 250,
		MinimumTopKOverlap: .95, MaximumNormalizedScoreDelta: .2, ObservedTopKOverlap: .98, ObservedMaximumNormalizedScoreDelta: .1, Ready: true, CheckCount: 8, PassedCount: 8, Checks: checks}
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

func digestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func digest(value int) string { return fmt.Sprintf("%064x", value) }
