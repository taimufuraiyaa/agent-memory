package application

import (
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestStandaloneNaturalFlowReportRequiresCompleteContentFreeJourney(t *testing.T) {
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	input := SkillStandaloneNaturalFlowReportInput{
		ReleaseID: "release-31", BuildDigest: "sha256:" + strings.Repeat("a", 64),
		MigrationDigest: "sha256:" + strings.Repeat("b", 64), StartedAt: now, CompletedAt: now.Add(time.Second),
		CompletedStages: []core.SkillOrchestratorStage{
			core.SkillStageDetect, core.SkillStageBuild, core.SkillStageEvaluate, core.SkillStageDecide,
			core.SkillStageStartCanary, core.SkillStageAnalyzeCanary, core.SkillStageActivate, core.SkillStageRollback,
		},
		ControlledRestarts: 1, ExactUses: 5, VerifiedHardSignals: 1,
		AutomaticActivation: true, LastKnownGoodRestored: true, RollbackDurationMS: 20,
	}
	report, err := BuildSkillStandaloneNaturalFlowReport(input)
	if err != nil || report.Schema != SkillStandaloneNaturalFlowReportSchemaV1 || !validSHA256Digest(report.ReportDigest) {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if err := VerifySkillStandaloneNaturalFlowReport(report); err != nil {
		t.Fatal(err)
	}

	report.ExactUses = 0
	if err := VerifySkillStandaloneNaturalFlowReport(report); err == nil {
		t.Fatal("incomplete report verified")
	}
}

func TestStandaloneNaturalFlowReportRejectsDuplicateOrMissingStages(t *testing.T) {
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	_, err := BuildSkillStandaloneNaturalFlowReport(SkillStandaloneNaturalFlowReportInput{
		ReleaseID: "release-31", BuildDigest: "sha256:" + strings.Repeat("a", 64),
		MigrationDigest: "sha256:" + strings.Repeat("b", 64), StartedAt: now, CompletedAt: now.Add(time.Second),
		CompletedStages:    []core.SkillOrchestratorStage{core.SkillStageDetect, core.SkillStageDetect},
		ControlledRestarts: 1, ExactUses: 1, VerifiedHardSignals: 1,
		AutomaticActivation: true, LastKnownGoodRestored: true, RollbackDurationMS: 1,
	})
	if err == nil {
		t.Fatal("incomplete duplicate stage report built")
	}
}
