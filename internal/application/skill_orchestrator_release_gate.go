package application

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	SkillProductionReleaseEvidenceSchemaV1 = "agent-memory/skill-orchestrator-production-release-evidence/v1"
	SkillProductApprovalSchemaV1           = "agent-memory/skill-orchestrator-product-approval/v1"
	maxSkillReleaseReferenceBytes          = 256
)

type SkillReleaseDrillOperation string

const (
	SkillReleaseDrillPause    SkillReleaseDrillOperation = "pause"
	SkillReleaseDrillDrain    SkillReleaseDrillOperation = "drain"
	SkillReleaseDrillRestore  SkillReleaseDrillOperation = "restore"
	SkillReleaseDrillShutdown SkillReleaseDrillOperation = "shutdown"
)

var requiredSkillReleaseDrills = []SkillReleaseDrillOperation{
	SkillReleaseDrillPause, SkillReleaseDrillDrain, SkillReleaseDrillRestore, SkillReleaseDrillShutdown,
}

var requiredSkillReleaseModes = []core.SkillOrchestratorMode{
	core.SkillOrchestratorDisabled, core.SkillOrchestratorShadow, core.SkillOrchestratorManual,
	core.SkillOrchestratorCanary, core.SkillOrchestratorAutomaticLowRisk,
}

type SkillRolloutObservation struct {
	Sequence                    int                        `json:"sequence"`
	Mode                        core.SkillOrchestratorMode `json:"mode"`
	ConfigurationDigest         string                     `json:"configuration_digest"`
	ConfigurationSignatureValid bool                       `json:"configuration_signature_valid"`
	Passed                      bool                       `json:"passed"`
}

type SkillOperationalDrill struct {
	Iteration               int                        `json:"iteration"`
	Operation               SkillReleaseDrillOperation `json:"operation"`
	Passed                  bool                       `json:"passed"`
	ActiveSkillDigestBefore string                     `json:"active_skill_digest_before"`
	ActiveSkillDigestAfter  string                     `json:"active_skill_digest_after"`
	AuditRecordsBefore      int64                      `json:"audit_records_before"`
	AuditRecordsAfter       int64                      `json:"audit_records_after"`
	RollbackMillis          int64                      `json:"rollback_millis"`
	AlertsRouted            bool                       `json:"alerts_routed"`
	RunbookID               string                     `json:"runbook_id"`
	RunbookDigest           string                     `json:"runbook_digest"`
}

type SkillProductionReleaseEvidence struct {
	Schema                 string                    `json:"schema"`
	ReleaseID              string                    `json:"release_id"`
	BuildDigest            string                    `json:"build_digest"`
	MigrationDigest        string                    `json:"migration_digest"`
	PolicyDigest           string                    `json:"policy_digest"`
	Rollout                []SkillRolloutObservation `json:"rollout"`
	Drills                 []SkillOperationalDrill   `json:"drills"`
	RollbackSLOMillis      int64                     `json:"rollback_slo_millis"`
	StandaloneReportDigest string                    `json:"standalone_report_digest"`
	HostedReportDigest     string                    `json:"hosted_report_digest"`
	ChaosCertificateDigest string                    `json:"chaos_certificate_digest"`
	SecurityReportDigest   string                    `json:"security_report_digest"`
	CapacityReportDigest   string                    `json:"capacity_report_digest"`
	MigrationReportDigest  string                    `json:"migration_report_digest"`
	AlertRoutingDigest     string                    `json:"alert_routing_digest"`
	GeneratedAt            time.Time                 `json:"generated_at"`
	SigningKeyID           string                    `json:"signing_key_id"`
	Signature              string                    `json:"signature"`
}

type skillProductionReleaseEvidencePayload struct {
	Schema                 string                    `json:"schema"`
	ReleaseID              string                    `json:"release_id"`
	BuildDigest            string                    `json:"build_digest"`
	MigrationDigest        string                    `json:"migration_digest"`
	PolicyDigest           string                    `json:"policy_digest"`
	Rollout                []SkillRolloutObservation `json:"rollout"`
	Drills                 []SkillOperationalDrill   `json:"drills"`
	RollbackSLOMillis      int64                     `json:"rollback_slo_millis"`
	StandaloneReportDigest string                    `json:"standalone_report_digest"`
	HostedReportDigest     string                    `json:"hosted_report_digest"`
	ChaosCertificateDigest string                    `json:"chaos_certificate_digest"`
	SecurityReportDigest   string                    `json:"security_report_digest"`
	CapacityReportDigest   string                    `json:"capacity_report_digest"`
	MigrationReportDigest  string                    `json:"migration_report_digest"`
	AlertRoutingDigest     string                    `json:"alert_routing_digest"`
	GeneratedAt            time.Time                 `json:"generated_at"`
	SigningKeyID           string                    `json:"signing_key_id"`
}

type SkillProductApproval struct {
	Schema                   string    `json:"schema"`
	ApprovalID               string    `json:"approval_id"`
	ReleaseID                string    `json:"release_id"`
	BuildDigest              string    `json:"build_digest"`
	MigrationDigest          string    `json:"migration_digest"`
	PolicyDigest             string    `json:"policy_digest"`
	ApproverID               string    `json:"approver_id"`
	ApproverRole             string    `json:"approver_role"`
	RiskClassesApproved      bool      `json:"risk_classes_approved"`
	ThresholdsApproved       bool      `json:"thresholds_approved"`
	CanaryPolicyApproved     bool      `json:"canary_policy_approved"`
	RetryDeadLetterApproved  bool      `json:"retry_dead_letter_approved"`
	BudgetsApproved          bool      `json:"budgets_approved"`
	RetentionApproved        bool      `json:"retention_approved"`
	SLOsApproved             bool      `json:"slos_approved"`
	AutomaticLowRiskApproved bool      `json:"automatic_low_risk_approved"`
	ApprovedAt               time.Time `json:"approved_at"`
	ExpiresAt                time.Time `json:"expires_at"`
	SigningKeyID             string    `json:"signing_key_id"`
	Signature                string    `json:"signature"`
}

type skillProductApprovalPayload struct {
	Schema                   string    `json:"schema"`
	ApprovalID               string    `json:"approval_id"`
	ReleaseID                string    `json:"release_id"`
	BuildDigest              string    `json:"build_digest"`
	MigrationDigest          string    `json:"migration_digest"`
	PolicyDigest             string    `json:"policy_digest"`
	ApproverID               string    `json:"approver_id"`
	ApproverRole             string    `json:"approver_role"`
	RiskClassesApproved      bool      `json:"risk_classes_approved"`
	ThresholdsApproved       bool      `json:"thresholds_approved"`
	CanaryPolicyApproved     bool      `json:"canary_policy_approved"`
	RetryDeadLetterApproved  bool      `json:"retry_dead_letter_approved"`
	BudgetsApproved          bool      `json:"budgets_approved"`
	RetentionApproved        bool      `json:"retention_approved"`
	SLOsApproved             bool      `json:"slos_approved"`
	AutomaticLowRiskApproved bool      `json:"automatic_low_risk_approved"`
	ApprovedAt               time.Time `json:"approved_at"`
	ExpiresAt                time.Time `json:"expires_at"`
	SigningKeyID             string    `json:"signing_key_id"`
}

type SkillReleaseGateConfig struct {
	ReleaseID          string
	BuildDigest        string
	MigrationDigest    string
	PolicyDigest       string
	ReleaseSignerID    string
	TrustedReleaseKeys map[string]ed25519.PublicKey
	TrustedProductKeys map[string]ed25519.PublicKey
	MaximumApprovalAge time.Duration
}

type SkillReleaseGateReport struct {
	Ready           bool      `json:"ready"`
	ReleaseID       string    `json:"release_id"`
	BuildDigest     string    `json:"build_digest"`
	MigrationDigest string    `json:"migration_digest"`
	PolicyDigest    string    `json:"policy_digest"`
	EvidenceDigest  string    `json:"evidence_digest"`
	ApprovalDigest  string    `json:"approval_digest"`
	DrillIterations int       `json:"drill_iterations"`
	Blockers        []string  `json:"blockers"`
	VerifiedAt      time.Time `json:"verified_at"`
}

func RequiredSkillReleaseDrillOperations() []SkillReleaseDrillOperation {
	return append([]SkillReleaseDrillOperation(nil), requiredSkillReleaseDrills...)
}

func EvaluateSkillOrchestratorReleaseGate(config SkillReleaseGateConfig, evidence SkillProductionReleaseEvidence, approval SkillProductApproval, now time.Time) (SkillReleaseGateReport, error) {
	if err := validateSkillReleaseGateConfig(config, now); err != nil {
		return SkillReleaseGateReport{}, err
	}
	if err := validateSkillReleaseEvidenceBounds(evidence); err != nil {
		return SkillReleaseGateReport{}, err
	}
	report := SkillReleaseGateReport{
		ReleaseID: config.ReleaseID, BuildDigest: config.BuildDigest, MigrationDigest: config.MigrationDigest,
		PolicyDigest: config.PolicyDigest, Blockers: []string{}, VerifiedAt: now.UTC(),
	}
	evidencePayload, err := json.Marshal(skillProductionReleaseEvidenceUnsigned(evidence))
	if err != nil {
		return SkillReleaseGateReport{}, err
	}
	report.EvidenceDigest = releasePayloadDigest(evidencePayload)
	if evidence.Schema != SkillProductionReleaseEvidenceSchemaV1 || evidence.ReleaseID != config.ReleaseID || evidence.BuildDigest != config.BuildDigest || evidence.MigrationDigest != config.MigrationDigest || evidence.PolicyDigest != config.PolicyDigest {
		report.Blockers = append(report.Blockers, "release_evidence_binding_mismatch")
	}
	if !verifyReleaseSignature(config.TrustedReleaseKeys, evidence.SigningKeyID, evidence.Signature, evidencePayload) {
		report.Blockers = append(report.Blockers, "release_evidence_signature_invalid")
	}
	if !validSkillRollout(evidence.Rollout) {
		report.Blockers = append(report.Blockers, "rollout_sequence_invalid")
	}
	report.DrillIterations = completeSkillDrillIterations(evidence.Drills, evidence.RollbackSLOMillis)
	if report.DrillIterations < 2 {
		report.Blockers = append(report.Blockers, "staging_drills_incomplete")
	}
	if !validSkillEvidenceDigests(evidence) {
		report.Blockers = append(report.Blockers, "required_release_evidence_incomplete")
	}
	approvalPayload, err := json.Marshal(skillProductApprovalUnsigned(approval))
	if err != nil {
		return SkillReleaseGateReport{}, err
	}
	report.ApprovalDigest = releasePayloadDigest(approvalPayload)
	if approval.Schema != SkillProductApprovalSchemaV1 || approval.ApproverRole != "accountable_product" || !boundedReleaseReference(approval.ApprovalID) || !boundedReleaseReference(approval.ApproverID) {
		report.Blockers = append(report.Blockers, "product_approval_identity_invalid")
	}
	if approval.ReleaseID != config.ReleaseID || approval.BuildDigest != config.BuildDigest || approval.MigrationDigest != config.MigrationDigest || approval.PolicyDigest != config.PolicyDigest {
		report.Blockers = append(report.Blockers, "product_approval_binding_mismatch")
	}
	if !allSkillProductControlsApproved(approval) {
		report.Blockers = append(report.Blockers, "product_controls_unapproved")
	}
	if approval.ApprovedAt.IsZero() || approval.ExpiresAt.IsZero() || approval.ApprovedAt.After(now) || !now.Before(approval.ExpiresAt) || now.Sub(approval.ApprovedAt) > config.MaximumApprovalAge {
		report.Blockers = append(report.Blockers, "product_approval_stale")
	}
	if !verifyReleaseSignature(config.TrustedProductKeys, approval.SigningKeyID, approval.Signature, approvalPayload) {
		report.Blockers = append(report.Blockers, "product_approval_signature_invalid")
	}
	if approval.ApproverID == config.ReleaseSignerID || approval.SigningKeyID == evidence.SigningKeyID {
		report.Blockers = append(report.Blockers, "separation_of_duty_invalid")
	}
	report.Blockers = sortedUniqueStrings(report.Blockers)
	report.Ready = len(report.Blockers) == 0
	return report, nil
}

func skillProductionReleaseEvidenceUnsigned(evidence SkillProductionReleaseEvidence) skillProductionReleaseEvidencePayload {
	return skillProductionReleaseEvidencePayload{
		Schema: evidence.Schema, ReleaseID: evidence.ReleaseID, BuildDigest: evidence.BuildDigest,
		MigrationDigest: evidence.MigrationDigest, PolicyDigest: evidence.PolicyDigest,
		Rollout: evidence.Rollout, Drills: evidence.Drills, RollbackSLOMillis: evidence.RollbackSLOMillis,
		StandaloneReportDigest: evidence.StandaloneReportDigest, HostedReportDigest: evidence.HostedReportDigest,
		ChaosCertificateDigest: evidence.ChaosCertificateDigest, SecurityReportDigest: evidence.SecurityReportDigest,
		CapacityReportDigest: evidence.CapacityReportDigest, MigrationReportDigest: evidence.MigrationReportDigest,
		AlertRoutingDigest: evidence.AlertRoutingDigest, GeneratedAt: evidence.GeneratedAt.UTC(), SigningKeyID: evidence.SigningKeyID,
	}
}

func skillProductApprovalUnsigned(approval SkillProductApproval) skillProductApprovalPayload {
	return skillProductApprovalPayload{
		Schema: approval.Schema, ApprovalID: approval.ApprovalID, ReleaseID: approval.ReleaseID,
		BuildDigest: approval.BuildDigest, MigrationDigest: approval.MigrationDigest, PolicyDigest: approval.PolicyDigest,
		ApproverID: approval.ApproverID, ApproverRole: approval.ApproverRole,
		RiskClassesApproved: approval.RiskClassesApproved, ThresholdsApproved: approval.ThresholdsApproved,
		CanaryPolicyApproved: approval.CanaryPolicyApproved, RetryDeadLetterApproved: approval.RetryDeadLetterApproved,
		BudgetsApproved: approval.BudgetsApproved, RetentionApproved: approval.RetentionApproved,
		SLOsApproved: approval.SLOsApproved, AutomaticLowRiskApproved: approval.AutomaticLowRiskApproved,
		ApprovedAt: approval.ApprovedAt.UTC(), ExpiresAt: approval.ExpiresAt.UTC(), SigningKeyID: approval.SigningKeyID,
	}
}

func validateSkillReleaseGateConfig(config SkillReleaseGateConfig, now time.Time) error {
	if !boundedReleaseReference(config.ReleaseID) || !boundedReleaseReference(config.ReleaseSignerID) || now.IsZero() || config.MaximumApprovalAge <= 0 || config.MaximumApprovalAge > 90*24*time.Hour {
		return errors.New("skill release gate identity, clock, or approval age is invalid")
	}
	for _, digest := range []string{config.BuildDigest, config.MigrationDigest, config.PolicyDigest} {
		if !validSHA256Digest(digest) {
			return errors.New("skill release gate digest is invalid")
		}
	}
	if len(config.TrustedReleaseKeys) == 0 || len(config.TrustedProductKeys) == 0 {
		return errors.New("skill release gate trust material is required")
	}
	return nil
}

func validateSkillReleaseEvidenceBounds(evidence SkillProductionReleaseEvidence) error {
	if !boundedReleaseReference(evidence.ReleaseID) || !boundedReleaseReference(evidence.SigningKeyID) || evidence.GeneratedAt.IsZero() || len(evidence.Rollout) > 16 || len(evidence.Drills) > 64 || evidence.RollbackSLOMillis < 1 || evidence.RollbackSLOMillis > int64((10*time.Minute)/time.Millisecond) {
		return errors.New("skill release evidence identity or bounds are invalid")
	}
	for _, drill := range evidence.Drills {
		if !boundedReleaseReference(drill.RunbookID) {
			return errors.New("skill release drill reference is invalid")
		}
	}
	return nil
}

func validSkillRollout(rollout []SkillRolloutObservation) bool {
	if len(rollout) != len(requiredSkillReleaseModes) {
		return false
	}
	for index, observation := range rollout {
		if observation.Sequence != index+1 || observation.Mode != requiredSkillReleaseModes[index] || !validSHA256Digest(observation.ConfigurationDigest) || !observation.ConfigurationSignatureValid || !observation.Passed {
			return false
		}
	}
	return true
}

func completeSkillDrillIterations(drills []SkillOperationalDrill, rollbackSLOMillis int64) int {
	if rollbackSLOMillis < 1 {
		return 0
	}
	required := make(map[string]struct{})
	for iteration := 1; iteration <= 2; iteration++ {
		for _, operation := range requiredSkillReleaseDrills {
			required[drillKey(iteration, operation)] = struct{}{}
		}
	}
	for _, drill := range drills {
		key := drillKey(drill.Iteration, drill.Operation)
		if _, ok := required[key]; !ok || !drill.Passed || !validSHA256Digest(drill.ActiveSkillDigestBefore) || drill.ActiveSkillDigestBefore != drill.ActiveSkillDigestAfter || drill.AuditRecordsBefore < 0 || drill.AuditRecordsAfter < drill.AuditRecordsBefore || drill.RollbackMillis < 0 || drill.RollbackMillis > rollbackSLOMillis || !drill.AlertsRouted || !validSHA256Digest(drill.RunbookDigest) {
			return 0
		}
		delete(required, key)
	}
	if len(required) != 0 {
		return 0
	}
	return 2
}

func validSkillEvidenceDigests(evidence SkillProductionReleaseEvidence) bool {
	for _, digest := range []string{
		evidence.StandaloneReportDigest, evidence.HostedReportDigest, evidence.ChaosCertificateDigest,
		evidence.SecurityReportDigest, evidence.CapacityReportDigest, evidence.MigrationReportDigest, evidence.AlertRoutingDigest,
	} {
		if !validSHA256Digest(digest) {
			return false
		}
	}
	return true
}

func allSkillProductControlsApproved(approval SkillProductApproval) bool {
	return approval.RiskClassesApproved && approval.ThresholdsApproved && approval.CanaryPolicyApproved &&
		approval.RetryDeadLetterApproved && approval.BudgetsApproved && approval.RetentionApproved &&
		approval.SLOsApproved && approval.AutomaticLowRiskApproved
}

func verifyReleaseSignature(keys map[string]ed25519.PublicKey, keyID, encoded string, payload []byte) bool {
	key, ok := keys[keyID]
	signature, err := base64.StdEncoding.DecodeString(encoded)
	return ok && len(key) == ed25519.PublicKeySize && err == nil && len(signature) == ed25519.SignatureSize && ed25519.Verify(key, payload, signature)
}

func releasePayloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func boundedReleaseReference(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maxSkillReleaseReferenceBytes && !strings.ContainsAny(value, "\r\n\t")
}

func drillKey(iteration int, operation SkillReleaseDrillOperation) string {
	return string(rune(iteration)) + "\x00" + string(operation)
}

func sortedUniqueStrings(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		unique[value] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
