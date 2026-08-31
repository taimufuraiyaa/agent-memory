package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const SkillStandaloneNaturalFlowReportSchemaV1 = "agent-memory/skill-standalone-natural-flow-report/v1"

type SkillStandaloneNaturalFlowReportInput struct {
	ReleaseID             string
	BuildDigest           string
	MigrationDigest       string
	StartedAt             time.Time
	CompletedAt           time.Time
	CompletedStages       []core.SkillOrchestratorStage
	ControlledRestarts    int
	ExactUses             int
	VerifiedHardSignals   int
	AutomaticActivation   bool
	LastKnownGoodRestored bool
	RollbackDurationMS    int64
}

type SkillStandaloneNaturalFlowReport struct {
	Schema                string                        `json:"schema"`
	ReleaseID             string                        `json:"release_id"`
	BuildDigest           string                        `json:"build_digest"`
	MigrationDigest       string                        `json:"migration_digest"`
	StartedAt             time.Time                     `json:"started_at"`
	CompletedAt           time.Time                     `json:"completed_at"`
	CompletedStages       []core.SkillOrchestratorStage `json:"completed_stages"`
	ControlledRestarts    int                           `json:"controlled_restarts"`
	ExactUses             int                           `json:"exact_uses"`
	VerifiedHardSignals   int                           `json:"verified_hard_signals"`
	AutomaticActivation   bool                          `json:"automatic_activation"`
	LastKnownGoodRestored bool                          `json:"last_known_good_restored"`
	RollbackDurationMS    int64                         `json:"rollback_duration_ms"`
	ReportDigest          string                        `json:"report_digest"`
}

type skillStandaloneNaturalFlowUnsignedReport struct {
	Schema                string                        `json:"schema"`
	ReleaseID             string                        `json:"release_id"`
	BuildDigest           string                        `json:"build_digest"`
	MigrationDigest       string                        `json:"migration_digest"`
	StartedAt             time.Time                     `json:"started_at"`
	CompletedAt           time.Time                     `json:"completed_at"`
	CompletedStages       []core.SkillOrchestratorStage `json:"completed_stages"`
	ControlledRestarts    int                           `json:"controlled_restarts"`
	ExactUses             int                           `json:"exact_uses"`
	VerifiedHardSignals   int                           `json:"verified_hard_signals"`
	AutomaticActivation   bool                          `json:"automatic_activation"`
	LastKnownGoodRestored bool                          `json:"last_known_good_restored"`
	RollbackDurationMS    int64                         `json:"rollback_duration_ms"`
}

func BuildSkillStandaloneNaturalFlowReport(input SkillStandaloneNaturalFlowReportInput) (SkillStandaloneNaturalFlowReport, error) {
	unsigned := skillStandaloneNaturalFlowUnsignedReport{
		Schema: SkillStandaloneNaturalFlowReportSchemaV1, ReleaseID: input.ReleaseID,
		BuildDigest: input.BuildDigest, MigrationDigest: input.MigrationDigest,
		StartedAt: input.StartedAt.UTC(), CompletedAt: input.CompletedAt.UTC(),
		CompletedStages:    append([]core.SkillOrchestratorStage(nil), input.CompletedStages...),
		ControlledRestarts: input.ControlledRestarts, ExactUses: input.ExactUses,
		VerifiedHardSignals: input.VerifiedHardSignals, AutomaticActivation: input.AutomaticActivation,
		LastKnownGoodRestored: input.LastKnownGoodRestored, RollbackDurationMS: input.RollbackDurationMS,
	}
	sort.Slice(unsigned.CompletedStages, func(i, j int) bool { return unsigned.CompletedStages[i] < unsigned.CompletedStages[j] })
	if err := validateSkillStandaloneNaturalFlowUnsigned(unsigned); err != nil {
		return SkillStandaloneNaturalFlowReport{}, err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return SkillStandaloneNaturalFlowReport{}, err
	}
	digest := sha256.Sum256(payload)
	return SkillStandaloneNaturalFlowReport{
		Schema: unsigned.Schema, ReleaseID: unsigned.ReleaseID, BuildDigest: unsigned.BuildDigest,
		MigrationDigest: unsigned.MigrationDigest, StartedAt: unsigned.StartedAt, CompletedAt: unsigned.CompletedAt,
		CompletedStages: unsigned.CompletedStages, ControlledRestarts: unsigned.ControlledRestarts,
		ExactUses: unsigned.ExactUses, VerifiedHardSignals: unsigned.VerifiedHardSignals,
		AutomaticActivation: unsigned.AutomaticActivation, LastKnownGoodRestored: unsigned.LastKnownGoodRestored,
		RollbackDurationMS: unsigned.RollbackDurationMS, ReportDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func VerifySkillStandaloneNaturalFlowReport(report SkillStandaloneNaturalFlowReport) error {
	unsigned := skillStandaloneNaturalFlowUnsignedReport{
		Schema: report.Schema, ReleaseID: report.ReleaseID, BuildDigest: report.BuildDigest,
		MigrationDigest: report.MigrationDigest, StartedAt: report.StartedAt.UTC(), CompletedAt: report.CompletedAt.UTC(),
		CompletedStages:    append([]core.SkillOrchestratorStage(nil), report.CompletedStages...),
		ControlledRestarts: report.ControlledRestarts, ExactUses: report.ExactUses,
		VerifiedHardSignals: report.VerifiedHardSignals, AutomaticActivation: report.AutomaticActivation,
		LastKnownGoodRestored: report.LastKnownGoodRestored, RollbackDurationMS: report.RollbackDurationMS,
	}
	if err := validateSkillStandaloneNaturalFlowUnsigned(unsigned); err != nil {
		return err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if report.ReportDigest != "sha256:"+hex.EncodeToString(digest[:]) {
		return errors.New("standalone natural-flow report digest mismatch")
	}
	return nil
}

func validateSkillStandaloneNaturalFlowUnsigned(report skillStandaloneNaturalFlowUnsignedReport) error {
	if report.Schema != SkillStandaloneNaturalFlowReportSchemaV1 || strings.TrimSpace(report.ReleaseID) == "" || len(report.ReleaseID) > 128 || !validSHA256Digest(report.BuildDigest) || !validSHA256Digest(report.MigrationDigest) {
		return errors.New("standalone natural-flow report provenance is invalid")
	}
	if report.StartedAt.IsZero() || report.CompletedAt.Before(report.StartedAt) || report.CompletedAt.Sub(report.StartedAt) > 30*time.Minute || report.ControlledRestarts < 1 || report.ControlledRestarts > 100 || report.ExactUses < 1 || report.ExactUses > 1_000_000 || report.VerifiedHardSignals < 1 || report.VerifiedHardSignals > 1_000 || report.RollbackDurationMS < 0 || report.RollbackDurationMS > int64((10*time.Minute)/time.Millisecond) || !report.AutomaticActivation || !report.LastKnownGoodRestored {
		return errors.New("standalone natural-flow report outcome is incomplete or outside bounds")
	}
	required := map[core.SkillOrchestratorStage]struct{}{
		core.SkillStageDetect: {}, core.SkillStageBuild: {}, core.SkillStageEvaluate: {}, core.SkillStageDecide: {},
		core.SkillStageStartCanary: {}, core.SkillStageAnalyzeCanary: {}, core.SkillStageActivate: {}, core.SkillStageRollback: {},
	}
	if len(report.CompletedStages) != len(required) {
		return errors.New("standalone natural-flow report stage evidence is incomplete")
	}
	for _, stage := range report.CompletedStages {
		if _, ok := required[stage]; !ok {
			return errors.New("standalone natural-flow report stage evidence is unexpected or duplicated")
		}
		delete(required, stage)
	}
	if len(required) != 0 {
		return errors.New("standalone natural-flow report stage evidence is incomplete")
	}
	return nil
}
