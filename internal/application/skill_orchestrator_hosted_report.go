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

const SkillHostedNaturalFlowReportSchemaV1 = "agent-memory/skill-hosted-natural-flow-report/v1"

type SkillHostedNaturalFlowReportInput struct {
	ReleaseID               string
	BuildDigest             string
	MigrationDigest         string
	StandaloneOutcomeDigest string
	APIStatusDigest         string
	DashboardStatusDigest   string
	StartedAt               time.Time
	CompletedAt             time.Time
	CompletedStages         []core.SkillOrchestratorStage
	WorkerReplicas          int
	TenantJourneys          int
	ControlledTakeovers     int
	ExactUses               int
	AutomaticActivations    int
	LastKnownGoodRestores   int
	FairTenantClaims        bool
	RLSIsolation            bool
	PolicyEnablement        bool
	RollbackPriority        bool
}

type SkillHostedNaturalFlowReport struct {
	Schema                  string                        `json:"schema"`
	ReleaseID               string                        `json:"release_id"`
	BuildDigest             string                        `json:"build_digest"`
	MigrationDigest         string                        `json:"migration_digest"`
	StandaloneOutcomeDigest string                        `json:"standalone_outcome_digest"`
	APIStatusDigest         string                        `json:"api_status_digest"`
	DashboardStatusDigest   string                        `json:"dashboard_status_digest"`
	StartedAt               time.Time                     `json:"started_at"`
	CompletedAt             time.Time                     `json:"completed_at"`
	CompletedStages         []core.SkillOrchestratorStage `json:"completed_stages"`
	WorkerReplicas          int                           `json:"worker_replicas"`
	TenantJourneys          int                           `json:"tenant_journeys"`
	ControlledTakeovers     int                           `json:"controlled_takeovers"`
	ExactUses               int                           `json:"exact_uses"`
	AutomaticActivations    int                           `json:"automatic_activations"`
	LastKnownGoodRestores   int                           `json:"last_known_good_restores"`
	FairTenantClaims        bool                          `json:"fair_tenant_claims"`
	RLSIsolation            bool                          `json:"rls_isolation"`
	PolicyEnablement        bool                          `json:"policy_enablement"`
	RollbackPriority        bool                          `json:"rollback_priority"`
	ReportDigest            string                        `json:"report_digest"`
}

type skillHostedNaturalFlowUnsignedReport struct {
	Schema                  string                        `json:"schema"`
	ReleaseID               string                        `json:"release_id"`
	BuildDigest             string                        `json:"build_digest"`
	MigrationDigest         string                        `json:"migration_digest"`
	StandaloneOutcomeDigest string                        `json:"standalone_outcome_digest"`
	APIStatusDigest         string                        `json:"api_status_digest"`
	DashboardStatusDigest   string                        `json:"dashboard_status_digest"`
	StartedAt               time.Time                     `json:"started_at"`
	CompletedAt             time.Time                     `json:"completed_at"`
	CompletedStages         []core.SkillOrchestratorStage `json:"completed_stages"`
	WorkerReplicas          int                           `json:"worker_replicas"`
	TenantJourneys          int                           `json:"tenant_journeys"`
	ControlledTakeovers     int                           `json:"controlled_takeovers"`
	ExactUses               int                           `json:"exact_uses"`
	AutomaticActivations    int                           `json:"automatic_activations"`
	LastKnownGoodRestores   int                           `json:"last_known_good_restores"`
	FairTenantClaims        bool                          `json:"fair_tenant_claims"`
	RLSIsolation            bool                          `json:"rls_isolation"`
	PolicyEnablement        bool                          `json:"policy_enablement"`
	RollbackPriority        bool                          `json:"rollback_priority"`
}

func BuildSkillHostedNaturalFlowReport(input SkillHostedNaturalFlowReportInput) (SkillHostedNaturalFlowReport, error) {
	unsigned := skillHostedNaturalFlowUnsignedReport{
		Schema: SkillHostedNaturalFlowReportSchemaV1, ReleaseID: input.ReleaseID,
		BuildDigest: input.BuildDigest, MigrationDigest: input.MigrationDigest,
		StandaloneOutcomeDigest: input.StandaloneOutcomeDigest, APIStatusDigest: input.APIStatusDigest,
		DashboardStatusDigest: input.DashboardStatusDigest, StartedAt: input.StartedAt.UTC(), CompletedAt: input.CompletedAt.UTC(),
		CompletedStages: append([]core.SkillOrchestratorStage(nil), input.CompletedStages...),
		WorkerReplicas:  input.WorkerReplicas, TenantJourneys: input.TenantJourneys,
		ControlledTakeovers: input.ControlledTakeovers, ExactUses: input.ExactUses,
		AutomaticActivations: input.AutomaticActivations, LastKnownGoodRestores: input.LastKnownGoodRestores,
		FairTenantClaims: input.FairTenantClaims, RLSIsolation: input.RLSIsolation,
		PolicyEnablement: input.PolicyEnablement, RollbackPriority: input.RollbackPriority,
	}
	sort.Slice(unsigned.CompletedStages, func(i, j int) bool { return unsigned.CompletedStages[i] < unsigned.CompletedStages[j] })
	if err := validateSkillHostedNaturalFlowUnsigned(unsigned); err != nil {
		return SkillHostedNaturalFlowReport{}, err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return SkillHostedNaturalFlowReport{}, err
	}
	digest := sha256.Sum256(payload)
	return hostedNaturalFlowReport(unsigned, "sha256:"+hex.EncodeToString(digest[:])), nil
}

func VerifySkillHostedNaturalFlowReport(report SkillHostedNaturalFlowReport) error {
	unsigned := skillHostedNaturalFlowUnsignedReport{
		Schema: report.Schema, ReleaseID: report.ReleaseID, BuildDigest: report.BuildDigest,
		MigrationDigest: report.MigrationDigest, StandaloneOutcomeDigest: report.StandaloneOutcomeDigest,
		APIStatusDigest: report.APIStatusDigest, DashboardStatusDigest: report.DashboardStatusDigest,
		StartedAt: report.StartedAt.UTC(), CompletedAt: report.CompletedAt.UTC(),
		CompletedStages: append([]core.SkillOrchestratorStage(nil), report.CompletedStages...),
		WorkerReplicas:  report.WorkerReplicas, TenantJourneys: report.TenantJourneys,
		ControlledTakeovers: report.ControlledTakeovers, ExactUses: report.ExactUses,
		AutomaticActivations: report.AutomaticActivations, LastKnownGoodRestores: report.LastKnownGoodRestores,
		FairTenantClaims: report.FairTenantClaims, RLSIsolation: report.RLSIsolation,
		PolicyEnablement: report.PolicyEnablement, RollbackPriority: report.RollbackPriority,
	}
	if err := validateSkillHostedNaturalFlowUnsigned(unsigned); err != nil {
		return err
	}
	payload, err := json.Marshal(unsigned)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if report.ReportDigest != "sha256:"+hex.EncodeToString(digest[:]) {
		return errors.New("hosted natural-flow report digest mismatch")
	}
	return nil
}

func hostedNaturalFlowReport(unsigned skillHostedNaturalFlowUnsignedReport, digest string) SkillHostedNaturalFlowReport {
	return SkillHostedNaturalFlowReport{
		Schema: unsigned.Schema, ReleaseID: unsigned.ReleaseID, BuildDigest: unsigned.BuildDigest,
		MigrationDigest: unsigned.MigrationDigest, StandaloneOutcomeDigest: unsigned.StandaloneOutcomeDigest,
		APIStatusDigest: unsigned.APIStatusDigest, DashboardStatusDigest: unsigned.DashboardStatusDigest,
		StartedAt: unsigned.StartedAt, CompletedAt: unsigned.CompletedAt, CompletedStages: unsigned.CompletedStages,
		WorkerReplicas: unsigned.WorkerReplicas, TenantJourneys: unsigned.TenantJourneys,
		ControlledTakeovers: unsigned.ControlledTakeovers, ExactUses: unsigned.ExactUses,
		AutomaticActivations: unsigned.AutomaticActivations, LastKnownGoodRestores: unsigned.LastKnownGoodRestores,
		FairTenantClaims: unsigned.FairTenantClaims, RLSIsolation: unsigned.RLSIsolation,
		PolicyEnablement: unsigned.PolicyEnablement, RollbackPriority: unsigned.RollbackPriority, ReportDigest: digest,
	}
}

func validateSkillHostedNaturalFlowUnsigned(report skillHostedNaturalFlowUnsignedReport) error {
	if report.Schema != SkillHostedNaturalFlowReportSchemaV1 || strings.TrimSpace(report.ReleaseID) == "" || len(report.ReleaseID) > 128 {
		return errors.New("hosted natural-flow report identity is invalid")
	}
	for _, digest := range []string{report.BuildDigest, report.MigrationDigest, report.StandaloneOutcomeDigest, report.APIStatusDigest, report.DashboardStatusDigest} {
		if !validSHA256Digest(digest) {
			return errors.New("hosted natural-flow report provenance digest is invalid")
		}
	}
	if report.APIStatusDigest != report.DashboardStatusDigest {
		return errors.New("hosted API and dashboard status contracts diverge")
	}
	if report.StartedAt.IsZero() || report.CompletedAt.Before(report.StartedAt) || report.CompletedAt.Sub(report.StartedAt) > 30*time.Minute || report.WorkerReplicas < 2 || report.WorkerReplicas > 100 || report.TenantJourneys < 2 || report.TenantJourneys > 1_000 || report.ControlledTakeovers < 1 || report.ControlledTakeovers > 100 || report.ExactUses < report.TenantJourneys || report.ExactUses > 1_000_000 || report.AutomaticActivations != report.TenantJourneys || report.LastKnownGoodRestores != report.TenantJourneys || !report.FairTenantClaims || !report.RLSIsolation || !report.PolicyEnablement || !report.RollbackPriority {
		return errors.New("hosted natural-flow report outcome is incomplete or outside bounds")
	}
	if report.ExactUses%report.TenantJourneys != 0 {
		return errors.New("hosted exact-use evidence cannot be compared per journey")
	}
	parityDigest, err := ComputeSkillNaturalFlowOutcomeDigest(SkillNaturalFlowOutcome{CompletedStages: report.CompletedStages, ExactUsesPerJourney: report.ExactUses / report.TenantJourneys, AutomaticActivation: true, LastKnownGoodRestored: true})
	if err != nil || parityDigest != report.StandaloneOutcomeDigest {
		return errors.New("hosted outcome diverges from standalone outcome")
	}
	required := map[core.SkillOrchestratorStage]struct{}{
		core.SkillStageDetect: {}, core.SkillStageBuild: {}, core.SkillStageEvaluate: {}, core.SkillStageDecide: {},
		core.SkillStageStartCanary: {}, core.SkillStageAnalyzeCanary: {}, core.SkillStageActivate: {}, core.SkillStageRollback: {},
	}
	if len(report.CompletedStages) != len(required) {
		return errors.New("hosted natural-flow report stage evidence is incomplete")
	}
	for _, stage := range report.CompletedStages {
		if _, exists := required[stage]; !exists {
			return errors.New("hosted natural-flow report stage evidence is unexpected or duplicated")
		}
		delete(required, stage)
	}
	return nil
}
