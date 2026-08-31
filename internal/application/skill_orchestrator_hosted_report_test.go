package application

import (
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestHostedNaturalFlowReportRequiresHorizontalStandaloneParity(t *testing.T) {
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	standaloneDigest, err := ComputeSkillNaturalFlowOutcomeDigest(SkillNaturalFlowOutcome{CompletedStages: hostedNaturalFlowStages(), ExactUsesPerJourney: 5, AutomaticActivation: true, LastKnownGoodRestored: true})
	if err != nil {
		t.Fatal(err)
	}
	input := SkillHostedNaturalFlowReportInput{
		ReleaseID: "release-32", BuildDigest: "sha256:" + strings.Repeat("a", 64),
		MigrationDigest: "sha256:" + strings.Repeat("b", 64), StandaloneOutcomeDigest: standaloneDigest,
		APIStatusDigest: "sha256:" + strings.Repeat("d", 64), DashboardStatusDigest: "sha256:" + strings.Repeat("d", 64),
		StartedAt: now, CompletedAt: now.Add(time.Second), CompletedStages: hostedNaturalFlowStages(),
		WorkerReplicas: 2, TenantJourneys: 2, ControlledTakeovers: 1, ExactUses: 10,
		AutomaticActivations: 2, LastKnownGoodRestores: 2, FairTenantClaims: true,
		RLSIsolation: true, PolicyEnablement: true, RollbackPriority: true,
	}
	report, err := BuildSkillHostedNaturalFlowReport(input)
	if err != nil || !validSHA256Digest(report.ReportDigest) {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if err := VerifySkillHostedNaturalFlowReport(report); err != nil {
		t.Fatal(err)
	}
	report.DashboardStatusDigest = "sha256:" + strings.Repeat("e", 64)
	if err := VerifySkillHostedNaturalFlowReport(report); err == nil {
		t.Fatal("API/dashboard drift verified")
	}
}

func hostedNaturalFlowStages() []core.SkillOrchestratorStage {
	return []core.SkillOrchestratorStage{
		core.SkillStageDetect, core.SkillStageBuild, core.SkillStageEvaluate, core.SkillStageDecide,
		core.SkillStageStartCanary, core.SkillStageAnalyzeCanary, core.SkillStageActivate, core.SkillStageRollback,
	}
}
