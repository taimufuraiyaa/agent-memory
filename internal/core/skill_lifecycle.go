package core

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	MaxSkillNameBytes              = 128
	MaxSkillDescriptionBytes       = 2048
	MaxSkillSummaryBytes           = 4096
	MaxSkillReasonBytes            = 2048
	MaxSkillListItems              = 100
	MaxSkillBundleFiles            = 256
	MaxSkillBundleFileBytes  int64 = 16 * 1024 * 1024
)

var (
	skillNamePattern   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	skillDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

type SkillRiskTier string

const (
	SkillRiskLow    SkillRiskTier = "low"
	SkillRiskMedium SkillRiskTier = "medium"
	SkillRiskHigh   SkillRiskTier = "high"
)

func (r SkillRiskTier) Valid() bool {
	return r == SkillRiskLow || r == SkillRiskMedium || r == SkillRiskHigh
}

type SkillStatus string

const (
	SkillStatusActive   SkillStatus = "active"
	SkillStatusArchived SkillStatus = "archived"
)

func (s SkillStatus) Valid() bool { return s == SkillStatusActive || s == SkillStatusArchived }

type LogicalSkill struct {
	ID                string        `json:"id"`
	Workspace         string        `json:"workspace"`
	Name              string        `json:"name"`
	Aliases           []string      `json:"aliases,omitempty"`
	Description       string        `json:"description"`
	TriggerConditions []string      `json:"trigger_conditions,omitempty"`
	Capabilities      []string      `json:"capabilities,omitempty"`
	RiskTier          SkillRiskTier `json:"risk_tier"`
	OwnerGroup        string        `json:"owner_group"`
	Status            SkillStatus   `json:"status"`
	Generation        int64         `json:"generation"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

func (s LogicalSkill) Validate() error {
	for field, value := range map[string]string{"id": s.ID, "workspace": s.Workspace, "description": s.Description, "owner_group": s.OwnerGroup} {
		limit := MaxSkillDescriptionBytes
		if field != "description" {
			limit = 256
		}
		if err := requireSkillText(field, value, limit); err != nil {
			return err
		}
	}
	if !skillNamePattern.MatchString(s.Name) || len(s.Name) > MaxSkillNameBytes {
		return errors.New("skill name must be lowercase kebab-case and within bound")
	}
	if !s.RiskTier.Valid() {
		return errors.New("invalid skill risk_tier")
	}
	if !s.Status.Valid() {
		return errors.New("invalid skill status")
	}
	if s.Generation < 1 {
		return errors.New("skill generation must be at least 1")
	}
	if err := validateSkillTextList("aliases", s.Aliases, MaxSkillListItems, MaxSkillNameBytes); err != nil {
		return err
	}
	for _, alias := range s.Aliases {
		if !skillNamePattern.MatchString(alias) {
			return errors.New("skill aliases must be lowercase kebab-case")
		}
	}
	if err := validateSkillTextList("trigger_conditions", s.TriggerConditions, MaxSkillListItems, MaxSkillSummaryBytes); err != nil {
		return err
	}
	if err := validateSkillTextList("capabilities", s.Capabilities, MaxSkillListItems, 256); err != nil {
		return err
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.UpdatedAt.Before(s.CreatedAt) {
		return errors.New("skill created_at and ordered updated_at are required")
	}
	return nil
}

type SkillRevisionState string

const (
	SkillRevisionDraft    SkillRevisionState = "draft"
	SkillRevisionTesting  SkillRevisionState = "testing"
	SkillRevisionCanary   SkillRevisionState = "canary"
	SkillRevisionActive   SkillRevisionState = "active"
	SkillRevisionPrevious SkillRevisionState = "previous"
	SkillRevisionDisabled SkillRevisionState = "disabled"
	SkillRevisionRejected SkillRevisionState = "rejected"
)

func (s SkillRevisionState) Valid() bool {
	switch s {
	case SkillRevisionDraft, SkillRevisionTesting, SkillRevisionCanary, SkillRevisionActive, SkillRevisionPrevious, SkillRevisionDisabled, SkillRevisionRejected:
		return true
	default:
		return false
	}
}

func CanTransitionSkillRevision(from, to SkillRevisionState) bool {
	if !from.Valid() || !to.Valid() || from == to {
		return false
	}
	allowed := map[SkillRevisionState]map[SkillRevisionState]bool{
		SkillRevisionDraft:    {SkillRevisionTesting: true, SkillRevisionRejected: true, SkillRevisionDisabled: true},
		SkillRevisionTesting:  {SkillRevisionCanary: true, SkillRevisionRejected: true, SkillRevisionDisabled: true},
		SkillRevisionCanary:   {SkillRevisionActive: true, SkillRevisionRejected: true, SkillRevisionDisabled: true},
		SkillRevisionActive:   {SkillRevisionPrevious: true, SkillRevisionDisabled: true},
		SkillRevisionPrevious: {SkillRevisionActive: true, SkillRevisionDisabled: true},
	}
	return allowed[from][to]
}

type SkillBundleFile struct {
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
}

func (f SkillBundleFile) Validate() error {
	if !validSkillRelativePath(f.Path) {
		return errors.New("skill bundle file path is unsafe")
	}
	if !validSkillDigest(f.Digest) {
		return errors.New("skill bundle file digest is invalid")
	}
	if f.SizeBytes < 0 || f.SizeBytes > MaxSkillBundleFileBytes {
		return errors.New("skill bundle file size_bytes exceeds bound")
	}
	return nil
}

type SkillCompatibility struct {
	Platforms            []string `json:"platforms,omitempty"`
	Architectures        []string `json:"architectures,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	MinimumRuntime       string   `json:"minimum_runtime,omitempty"`
}

func (c SkillCompatibility) Validate() error {
	if err := validateSkillTextList("platforms", c.Platforms, MaxSkillListItems, 64); err != nil {
		return err
	}
	if err := validateSkillTextList("architectures", c.Architectures, MaxSkillListItems, 64); err != nil {
		return err
	}
	if err := validateSkillTextList("required_capabilities", c.RequiredCapabilities, MaxSkillListItems, 256); err != nil {
		return err
	}
	if len(c.MinimumRuntime) > 128 {
		return errors.New("minimum_runtime exceeds bound")
	}
	return nil
}

type SkillRevision struct {
	ID                  string             `json:"id"`
	Workspace           string             `json:"workspace"`
	SkillID             string             `json:"skill_id"`
	Number              int64              `json:"number"`
	State               SkillRevisionState `json:"state"`
	BundleDigest        string             `json:"bundle_digest"`
	ManifestVersion     int64              `json:"manifest_version"`
	Files               []SkillBundleFile  `json:"files"`
	ParentRevisionIDs   []string           `json:"parent_revision_ids,omitempty"`
	CandidateID         string             `json:"candidate_id,omitempty"`
	Compatibility       SkillCompatibility `json:"compatibility"`
	RiskTier            SkillRiskTier      `json:"risk_tier"`
	ProtectedSections   []string           `json:"protected_sections,omitempty"`
	SourceMemoryIDs     []string           `json:"source_memory_ids,omitempty"`
	SourceToolLessonIDs []string           `json:"source_tool_lesson_ids,omitempty"`
	SourceEpisodeIDs    []string           `json:"source_episode_ids,omitempty"`
	CreatedBy           string             `json:"created_by"`
	CreatedAt           time.Time          `json:"created_at"`
}

type SkillLegalHold struct {
	ID         string    `json:"id"`
	Workspace  string    `json:"workspace"`
	TargetKind string    `json:"target_kind"`
	TargetID   string    `json:"target_id"`
	Reason     string    `json:"reason"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	ReleasedAt time.Time `json:"released_at,omitempty"`
}

type SkillOrchestratorLegalHold struct {
	ID         string                 `json:"id"`
	Scope      SkillOrchestratorScope `json:"scope"`
	TargetKind string                 `json:"target_kind"`
	TargetID   string                 `json:"target_id"`
	Reason     string                 `json:"reason"`
	State      string                 `json:"state"`
	CreatedAt  time.Time              `json:"created_at"`
	ReleasedAt time.Time              `json:"released_at,omitempty"`
}

type SkillEvidenceDeletionResult struct {
	Workspace           string `json:"workspace"`
	EvidenceKind        string `json:"evidence_kind"`
	EvidenceID          string `json:"evidence_id"`
	CandidateReferences int64  `json:"candidate_references"`
	RevisionReferences  int64  `json:"revision_references"`
	ExecutionsDeleted   int64  `json:"executions_deleted"`
	Replayed            bool   `json:"replayed"`
}

type SkillOrchestratorDeletionResult struct {
	Scope           SkillOrchestratorScope `json:"scope"`
	RecordKind      string                 `json:"record_kind"`
	RecordID        string                 `json:"record_id"`
	JobsCancelled   int64                  `json:"jobs_cancelled"`
	WorkflowsClosed int64                  `json:"workflows_closed"`
	RecordsDeleted  int64                  `json:"records_deleted"`
	Replayed        bool                   `json:"replayed"`
}

func (r SkillRevision) Validate() error {
	for field, value := range map[string]string{"id": r.ID, "workspace": r.Workspace, "skill_id": r.SkillID, "created_by": r.CreatedBy} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if r.Number < 1 {
		return errors.New("skill revision number must be at least 1")
	}
	if r.Number > 1 && len(r.ParentRevisionIDs) == 0 {
		return errors.New("skill revision parent is required after revision 1")
	}
	if !r.State.Valid() {
		return errors.New("invalid skill revision state")
	}
	if !validSkillDigest(r.BundleDigest) {
		return errors.New("skill revision bundle_digest is invalid")
	}
	if r.ManifestVersion < 1 {
		return errors.New("skill revision manifest_version must be at least 1")
	}
	if len(r.Files) == 0 || len(r.Files) > MaxSkillBundleFiles {
		return errors.New("skill revision files are required and bounded")
	}
	seen, hasSkill := map[string]struct{}{}, false
	for _, file := range r.Files {
		if err := file.Validate(); err != nil {
			return err
		}
		if _, exists := seen[file.Path]; exists {
			return errors.New("skill revision file paths must be unique")
		}
		seen[file.Path] = struct{}{}
		if file.Path == "SKILL.md" {
			hasSkill = true
		}
	}
	if !hasSkill {
		return errors.New("skill revision must contain SKILL.md")
	}
	if err := validateSkillIDList("parent_revision_ids", r.ParentRevisionIDs); err != nil {
		return err
	}
	if err := r.Compatibility.Validate(); err != nil {
		return err
	}
	if !r.RiskTier.Valid() {
		return errors.New("invalid skill revision risk_tier")
	}
	if err := validateSkillTextList("protected_sections", r.ProtectedSections, MaxSkillListItems, 256); err != nil {
		return err
	}
	for field, values := range map[string][]string{"source_memory_ids": r.SourceMemoryIDs, "source_tool_lesson_ids": r.SourceToolLessonIDs, "source_episode_ids": r.SourceEpisodeIDs} {
		if err := validateSkillIDList(field, values); err != nil {
			return err
		}
	}
	if r.CreatedAt.IsZero() {
		return errors.New("skill revision created_at is required")
	}
	return nil
}

type SkillCandidateKind string

const (
	SkillCandidateCreate SkillCandidateKind = "create"
	SkillCandidateRevise SkillCandidateKind = "revise"
	SkillCandidateMerge  SkillCandidateKind = "merge"
	SkillCandidateSplit  SkillCandidateKind = "split"
)

func (k SkillCandidateKind) Valid() bool {
	return k == SkillCandidateCreate || k == SkillCandidateRevise || k == SkillCandidateMerge || k == SkillCandidateSplit
}

type SkillCandidateState string

const (
	SkillCandidateProposed   SkillCandidateState = "proposed"
	SkillCandidateAccepted   SkillCandidateState = "accepted"
	SkillCandidateRejected   SkillCandidateState = "rejected"
	SkillCandidateSuperseded SkillCandidateState = "superseded"
)

func (s SkillCandidateState) Valid() bool {
	return s == SkillCandidateProposed || s == SkillCandidateAccepted || s == SkillCandidateRejected || s == SkillCandidateSuperseded
}

type SkillCandidate struct {
	ID                  string              `json:"id"`
	Workspace           string              `json:"workspace"`
	Kind                SkillCandidateKind  `json:"kind"`
	TargetSkillIDs      []string            `json:"target_skill_ids,omitempty"`
	Summary             string              `json:"summary"`
	ExpectedBenefit     string              `json:"expected_benefit"`
	Risks               []string            `json:"risks,omitempty"`
	RiskTier            SkillRiskTier       `json:"risk_tier"`
	Confidence          float64             `json:"confidence"`
	State               SkillCandidateState `json:"state"`
	SourceMemoryIDs     []string            `json:"source_memory_ids,omitempty"`
	SourceEpisodeIDs    []string            `json:"source_episode_ids,omitempty"`
	SourceToolLessonIDs []string            `json:"source_tool_lesson_ids,omitempty"`
	SourceExecutionIDs  []string            `json:"source_execution_ids,omitempty"`
	DeduplicationHash   string              `json:"deduplication_hash"`
	CreatedBy           string              `json:"created_by"`
	CreatedAt           time.Time           `json:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

func (c SkillCandidate) Validate() error {
	for field, value := range map[string]string{"id": c.ID, "workspace": c.Workspace, "summary": c.Summary, "expected_benefit": c.ExpectedBenefit, "created_by": c.CreatedBy} {
		limit := MaxSkillSummaryBytes
		if field == "id" || field == "workspace" || field == "created_by" {
			limit = 256
		}
		if err := requireSkillText(field, value, limit); err != nil {
			return err
		}
	}
	if !c.Kind.Valid() {
		return errors.New("invalid skill candidate kind")
	}
	if !c.State.Valid() {
		return errors.New("invalid skill candidate state")
	}
	if !c.RiskTier.Valid() {
		return errors.New("invalid skill candidate risk_tier")
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return errors.New("skill candidate confidence must be between 0 and 1")
	}
	if !validSkillDigest(c.DeduplicationHash) {
		return errors.New("skill candidate deduplication_hash is invalid")
	}
	if err := validateSkillIDList("target_skill_ids", c.TargetSkillIDs); err != nil {
		return err
	}
	switch c.Kind {
	case SkillCandidateCreate:
		if len(c.TargetSkillIDs) != 0 {
			return errors.New("create candidate must not have target_skill_id")
		}
	case SkillCandidateRevise, SkillCandidateSplit:
		if len(c.TargetSkillIDs) != 1 {
			return errors.New("revise or split candidate requires exactly one target_skill_id")
		}
	case SkillCandidateMerge:
		if len(c.TargetSkillIDs) < 2 {
			return errors.New("merge candidate requires at least two target_skill_ids")
		}
	}
	if len(c.SourceMemoryIDs)+len(c.SourceEpisodeIDs)+len(c.SourceToolLessonIDs)+len(c.SourceExecutionIDs) == 0 {
		return errors.New("skill candidate requires source evidence")
	}
	for field, values := range map[string][]string{"source_memory_ids": c.SourceMemoryIDs, "source_episode_ids": c.SourceEpisodeIDs, "source_tool_lesson_ids": c.SourceToolLessonIDs, "source_execution_ids": c.SourceExecutionIDs} {
		if err := validateSkillIDList(field, values); err != nil {
			return err
		}
	}
	if err := validateSkillTextList("risks", c.Risks, MaxSkillListItems, MaxSkillReasonBytes); err != nil {
		return err
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() || c.UpdatedAt.Before(c.CreatedAt) {
		return errors.New("skill candidate created_at and ordered updated_at are required")
	}
	return nil
}

type SkillEvaluationCaseKind string

const (
	SkillCasePositive      SkillEvaluationCaseKind = "positive"
	SkillCaseNegative      SkillEvaluationCaseKind = "negative_trigger"
	SkillCaseRegression    SkillEvaluationCaseKind = "regression"
	SkillCaseSafety        SkillEvaluationCaseKind = "safety"
	SkillCaseCompatibility SkillEvaluationCaseKind = "compatibility"
	SkillCaseArtifact      SkillEvaluationCaseKind = "artifact"
)

func (k SkillEvaluationCaseKind) Valid() bool {
	switch k {
	case SkillCasePositive, SkillCaseNegative, SkillCaseRegression, SkillCaseSafety, SkillCaseCompatibility, SkillCaseArtifact:
		return true
	default:
		return false
	}
}

type SkillEvaluationCase struct {
	ID        string                  `json:"id"`
	Kind      SkillEvaluationCaseKind `json:"kind"`
	Summary   string                  `json:"summary"`
	Reference string                  `json:"reference"`
	Required  bool                    `json:"required"`
}

func (c SkillEvaluationCase) Validate() error {
	for field, value := range map[string]string{"id": c.ID, "summary": c.Summary, "reference": c.Reference} {
		if err := requireSkillText(field, value, map[bool]int{true: 256, false: MaxSkillSummaryBytes}[field == "id"]); err != nil {
			return err
		}
	}
	if !c.Kind.Valid() {
		return errors.New("invalid skill evaluation case kind")
	}
	return nil
}

type SkillEvaluationSuite struct {
	ID        string                `json:"id"`
	SkillID   string                `json:"skill_id"`
	Workspace string                `json:"workspace"`
	Version   int64                 `json:"version"`
	Digest    string                `json:"digest"`
	Cases     []SkillEvaluationCase `json:"cases"`
	CreatedBy string                `json:"created_by"`
	CreatedAt time.Time             `json:"created_at"`
}

func (s SkillEvaluationSuite) Validate() error {
	for field, value := range map[string]string{"id": s.ID, "skill_id": s.SkillID, "workspace": s.Workspace, "created_by": s.CreatedBy} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if s.Version < 1 {
		return errors.New("skill evaluation suite version must be at least 1")
	}
	if !validSkillDigest(s.Digest) {
		return errors.New("skill evaluation suite digest is invalid")
	}
	if len(s.Cases) == 0 || len(s.Cases) > MaxSkillListItems {
		return errors.New("skill evaluation suite cases are required and bounded")
	}
	seen := map[string]struct{}{}
	for _, item := range s.Cases {
		if err := item.Validate(); err != nil {
			return err
		}
		if _, ok := seen[item.ID]; ok {
			return errors.New("skill evaluation case ids must be unique")
		}
		seen[item.ID] = struct{}{}
	}
	if s.CreatedAt.IsZero() {
		return errors.New("skill evaluation suite created_at is required")
	}
	return nil
}

type SkillEvaluationVerdict string

const (
	SkillEvaluationPass         SkillEvaluationVerdict = "pass"
	SkillEvaluationFail         SkillEvaluationVerdict = "fail"
	SkillEvaluationInconclusive SkillEvaluationVerdict = "inconclusive"
)

func (v SkillEvaluationVerdict) Valid() bool {
	return v == SkillEvaluationPass || v == SkillEvaluationFail || v == SkillEvaluationInconclusive
}

type SkillEvaluationCaseResult struct {
	CaseID                string `json:"case_id"`
	Passed                bool   `json:"passed"`
	IndependentlyVerified bool   `json:"independently_verified"`
	FailureClass          string `json:"failure_class,omitempty"`
	DurationMS            int64  `json:"duration_ms,omitempty"`
}

type SkillEvaluationRun struct {
	ID                     string                      `json:"id"`
	Workspace              string                      `json:"workspace"`
	SkillID                string                      `json:"skill_id"`
	RevisionID             string                      `json:"revision_id"`
	RevisionDigest         string                      `json:"revision_digest"`
	BaselineRevisionID     string                      `json:"baseline_revision_id,omitempty"`
	BaselineDigest         string                      `json:"baseline_digest,omitempty"`
	SuiteID                string                      `json:"suite_id"`
	SuiteVersion           int64                       `json:"suite_version"`
	SuiteDigest            string                      `json:"suite_digest"`
	Evaluator              string                      `json:"evaluator"`
	EvaluatorVersion       string                      `json:"evaluator_version"`
	EnvironmentFingerprint string                      `json:"environment_fingerprint"`
	Verdict                SkillEvaluationVerdict      `json:"verdict"`
	CaseResults            []SkillEvaluationCaseResult `json:"case_results"`
	StartedAt              time.Time                   `json:"started_at"`
	CompletedAt            time.Time                   `json:"completed_at"`
}

func (r SkillEvaluationRun) Validate() error {
	for field, value := range map[string]string{"id": r.ID, "workspace": r.Workspace, "skill_id": r.SkillID, "revision_id": r.RevisionID, "suite_id": r.SuiteID, "evaluator": r.Evaluator, "evaluator_version": r.EvaluatorVersion} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	for field, digest := range map[string]string{"revision_digest": r.RevisionDigest, "suite_digest": r.SuiteDigest, "environment_fingerprint": r.EnvironmentFingerprint} {
		if !validSkillDigest(digest) {
			return fmt.Errorf("skill evaluation %s is invalid", field)
		}
	}
	if r.BaselineRevisionID != "" && !validSkillDigest(r.BaselineDigest) {
		return errors.New("skill evaluation baseline_digest is invalid")
	}
	if r.SuiteVersion < 1 || !r.Verdict.Valid() {
		return errors.New("skill evaluation suite version and verdict are invalid")
	}
	if len(r.CaseResults) == 0 || len(r.CaseResults) > MaxSkillListItems {
		return errors.New("skill evaluation case_results are required and bounded")
	}
	seen := map[string]struct{}{}
	for _, result := range r.CaseResults {
		if err := requireSkillText("case_id", result.CaseID, 256); err != nil {
			return err
		}
		if result.DurationMS < 0 {
			return errors.New("skill evaluation case duration_ms cannot be negative")
		}
		if _, ok := seen[result.CaseID]; ok {
			return errors.New("skill evaluation case results must be unique")
		}
		seen[result.CaseID] = struct{}{}
	}
	if r.StartedAt.IsZero() || r.CompletedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) {
		return errors.New("skill evaluation completed_at must not precede started_at")
	}
	return nil
}

type SkillPromotionPolicy struct {
	ID                         string        `json:"id"`
	Workspace                  string        `json:"workspace"`
	Version                    int64         `json:"version"`
	RiskTier                   SkillRiskTier `json:"risk_tier"`
	MinimumCanarySamples       int           `json:"minimum_canary_samples"`
	MinimumVerifiedSuccessRate float64       `json:"minimum_verified_success_rate"`
	MaximumFailureRate         float64       `json:"maximum_failure_rate"`
	AllowAutomaticActivation   bool          `json:"allow_automatic_activation"`
	CreatedBy                  string        `json:"created_by"`
	CreatedAt                  time.Time     `json:"created_at"`
}

func (p SkillPromotionPolicy) Validate() error {
	for field, value := range map[string]string{"id": p.ID, "workspace": p.Workspace, "created_by": p.CreatedBy} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if p.Version < 1 || !p.RiskTier.Valid() {
		return errors.New("skill promotion policy version and risk_tier are invalid")
	}
	if p.MinimumCanarySamples < 1 || p.MinimumCanarySamples > 1_000_000 {
		return errors.New("skill promotion minimum_canary_samples is invalid")
	}
	if p.MinimumVerifiedSuccessRate < 0 || p.MinimumVerifiedSuccessRate > 1 || p.MaximumFailureRate < 0 || p.MaximumFailureRate > 1 {
		return errors.New("skill promotion policy rates must be between 0 and 1")
	}
	if p.RiskTier == SkillRiskHigh && p.AllowAutomaticActivation {
		return errors.New("high-risk skill policy cannot allow automatic activation")
	}
	if p.CreatedAt.IsZero() {
		return errors.New("skill promotion policy created_at is required")
	}
	return nil
}

type SkillPromotionDecision string

const (
	SkillDecisionPromote          SkillPromotionDecision = "promote"
	SkillDecisionCanary           SkillPromotionDecision = "canary"
	SkillDecisionApprovalRequired SkillPromotionDecision = "approval_required"
	SkillDecisionPause            SkillPromotionDecision = "pause"
	SkillDecisionReject           SkillPromotionDecision = "reject"
)

func (d SkillPromotionDecision) Valid() bool {
	switch d {
	case SkillDecisionPromote, SkillDecisionCanary, SkillDecisionApprovalRequired, SkillDecisionPause, SkillDecisionReject:
		return true
	default:
		return false
	}
}

type SkillPolicyDecision struct {
	ID               string                 `json:"id"`
	Workspace        string                 `json:"workspace"`
	SkillID          string                 `json:"skill_id"`
	RevisionID       string                 `json:"revision_id"`
	PolicyID         string                 `json:"policy_id"`
	PolicyVersion    int64                  `json:"policy_version"`
	EvaluationRunIDs []string               `json:"evaluation_run_ids"`
	RiskTier         SkillRiskTier          `json:"risk_tier"`
	Decision         SkillPromotionDecision `json:"decision"`
	ReasonCodes      []string               `json:"reason_codes"`
	DecidedAt        time.Time              `json:"decided_at"`
}

func (d SkillPolicyDecision) Validate() error {
	for field, value := range map[string]string{"id": d.ID, "workspace": d.Workspace, "skill_id": d.SkillID, "revision_id": d.RevisionID, "policy_id": d.PolicyID} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if d.PolicyVersion < 1 || !d.RiskTier.Valid() || !d.Decision.Valid() {
		return errors.New("skill policy decision classification is invalid")
	}
	if len(d.EvaluationRunIDs) == 0 {
		return errors.New("skill policy decision evaluation_run_ids are required")
	}
	if err := validateSkillIDList("evaluation_run_ids", d.EvaluationRunIDs); err != nil {
		return err
	}
	if err := validateSkillTextList("reason_codes", d.ReasonCodes, MaxSkillListItems, 256); err != nil {
		return err
	}
	if len(d.ReasonCodes) == 0 || d.DecidedAt.IsZero() {
		return errors.New("skill policy decision reasons and decided_at are required")
	}
	return nil
}

type SkillApproval struct {
	ID               string    `json:"id"`
	Workspace        string    `json:"workspace"`
	RevisionID       string    `json:"revision_id"`
	PolicyDecisionID string    `json:"policy_decision_id"`
	ApproverID       string    `json:"approver_id"`
	Approved         bool      `json:"approved"`
	Reason           string    `json:"reason"`
	CreatedAt        time.Time `json:"created_at"`
	RevokedAt        time.Time `json:"revoked_at,omitempty"`
}

func (a SkillApproval) Validate() error {
	for field, value := range map[string]string{"id": a.ID, "workspace": a.Workspace, "revision_id": a.RevisionID, "policy_decision_id": a.PolicyDecisionID, "approver_id": a.ApproverID, "reason": a.Reason} {
		limit := 256
		if field == "reason" {
			limit = MaxSkillReasonBytes
		}
		if err := requireSkillText(field, value, limit); err != nil {
			return err
		}
	}
	if a.CreatedAt.IsZero() || (!a.RevokedAt.IsZero() && a.RevokedAt.Before(a.CreatedAt)) {
		return errors.New("skill approval timestamps are invalid")
	}
	return nil
}

type SkillMaterializationState string

const (
	SkillMaterializationPending SkillMaterializationState = "pending"
	SkillMaterializationReady   SkillMaterializationState = "ready"
	SkillMaterializationFailed  SkillMaterializationState = "failed"
)

func (s SkillMaterializationState) Valid() bool {
	return s == SkillMaterializationPending || s == SkillMaterializationReady || s == SkillMaterializationFailed
}

type SkillActivation struct {
	ID                      string                    `json:"id"`
	Workspace               string                    `json:"workspace"`
	Environment             string                    `json:"environment"`
	SkillID                 string                    `json:"skill_id"`
	ActiveRevisionID        string                    `json:"active_revision_id"`
	ActiveDigest            string                    `json:"active_digest"`
	LastKnownGoodRevisionID string                    `json:"last_known_good_revision_id,omitempty"`
	LastKnownGoodDigest     string                    `json:"last_known_good_digest,omitempty"`
	CanaryRevisionID        string                    `json:"canary_revision_id,omitempty"`
	CanaryDigest            string                    `json:"canary_digest,omitempty"`
	Generation              int64                     `json:"generation"`
	PolicyDecisionID        string                    `json:"policy_decision_id"`
	Materialization         SkillMaterializationState `json:"materialization"`
	ActivatedBy             string                    `json:"activated_by"`
	ActivatedAt             time.Time                 `json:"activated_at"`
	UpdatedAt               time.Time                 `json:"updated_at"`
}

type SkillActivationOperationState string

const (
	SkillActivationOperationReserved      SkillActivationOperationState = "reserved"
	SkillActivationOperationMaterializing SkillActivationOperationState = "materializing"
	SkillActivationOperationCompleted     SkillActivationOperationState = "completed"
	SkillActivationOperationFailed        SkillActivationOperationState = "failed"
)

func (s SkillActivationOperationState) Valid() bool {
	switch s {
	case SkillActivationOperationReserved, SkillActivationOperationMaterializing, SkillActivationOperationCompleted, SkillActivationOperationFailed:
		return true
	default:
		return false
	}
}

func CanTransitionSkillActivationOperation(from, to SkillActivationOperationState) bool {
	if !from.Valid() || !to.Valid() || from == to {
		return false
	}
	allowed := map[SkillActivationOperationState]map[SkillActivationOperationState]bool{
		SkillActivationOperationReserved:      {SkillActivationOperationMaterializing: true, SkillActivationOperationFailed: true},
		SkillActivationOperationMaterializing: {SkillActivationOperationCompleted: true, SkillActivationOperationFailed: true},
		SkillActivationOperationFailed:        {SkillActivationOperationMaterializing: true},
	}
	return allowed[from][to]
}

type SkillActivationOperation struct {
	ID                 string                        `json:"id"`
	Workspace          string                        `json:"workspace"`
	Environment        string                        `json:"environment"`
	SkillID            string                        `json:"skill_id"`
	FromRevisionID     string                        `json:"from_revision_id,omitempty"`
	ToRevisionID       string                        `json:"to_revision_id"`
	ExpectedGeneration int64                         `json:"expected_generation"`
	State              SkillActivationOperationState `json:"state"`
	Error              string                        `json:"error,omitempty"`
	IdempotencyKey     string                        `json:"idempotency_key"`
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

type SkillMaterializationRequest struct {
	OperationID string        `json:"operation_id"`
	Skill       LogicalSkill  `json:"skill"`
	Revision    SkillRevision `json:"revision"`
}

type SkillMaterializationResult struct {
	OperationID string `json:"operation_id"`
	SkillID     string `json:"skill_id"`
	RevisionID  string `json:"revision_id"`
	Digest      string `json:"digest"`
	Recovered   bool   `json:"recovered"`
}

func (o SkillActivationOperation) Validate() error {
	for field, value := range map[string]string{
		"id": o.ID, "workspace": o.Workspace, "environment": o.Environment, "skill_id": o.SkillID,
		"to_revision_id": o.ToRevisionID, "idempotency_key": o.IdempotencyKey,
	} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if len(o.FromRevisionID) > 256 {
		return errors.New("skill activation operation from_revision_id exceeds bound")
	}
	if o.ExpectedGeneration < 0 || !o.State.Valid() {
		return errors.New("skill activation operation generation or state is invalid")
	}
	if len(o.Error) > MaxSkillReasonBytes {
		return errors.New("skill activation operation error exceeds bound")
	}
	if o.State == SkillActivationOperationFailed && strings.TrimSpace(o.Error) == "" {
		return errors.New("failed skill activation operation requires error")
	}
	if o.State != SkillActivationOperationFailed && o.Error != "" {
		return errors.New("non-failed skill activation operation cannot contain error")
	}
	if o.CreatedAt.IsZero() || o.UpdatedAt.IsZero() || o.UpdatedAt.Before(o.CreatedAt) {
		return errors.New("skill activation operation timestamps are invalid")
	}
	return nil
}

func (a SkillActivation) Validate() error {
	for field, value := range map[string]string{"id": a.ID, "workspace": a.Workspace, "environment": a.Environment, "skill_id": a.SkillID, "active_revision_id": a.ActiveRevisionID, "policy_decision_id": a.PolicyDecisionID, "activated_by": a.ActivatedBy} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if !validSkillDigest(a.ActiveDigest) {
		return errors.New("skill activation active_digest is invalid")
	}
	if a.LastKnownGoodRevisionID != "" && !validSkillDigest(a.LastKnownGoodDigest) {
		return errors.New("skill activation last_known_good_digest is invalid")
	}
	if a.CanaryRevisionID != "" && !validSkillDigest(a.CanaryDigest) {
		return errors.New("skill activation canary_digest is invalid")
	}
	if a.Generation < 1 || !a.Materialization.Valid() {
		return errors.New("skill activation generation or materialization is invalid")
	}
	if a.ActivatedAt.IsZero() || a.UpdatedAt.IsZero() || a.UpdatedAt.Before(a.ActivatedAt) {
		return errors.New("skill activation timestamps are invalid")
	}
	return nil
}

type SkillResolutionReason string

const (
	SkillResolutionExplicitPin SkillResolutionReason = "explicit_pin"
	SkillResolutionEnvironment SkillResolutionReason = "environment_pin"
	SkillResolutionCanary      SkillResolutionReason = "canary"
	SkillResolutionActive      SkillResolutionReason = "active"
	SkillResolutionFallback    SkillResolutionReason = "fallback"
)

func (r SkillResolutionReason) Valid() bool {
	switch r {
	case SkillResolutionExplicitPin, SkillResolutionEnvironment, SkillResolutionCanary, SkillResolutionActive, SkillResolutionFallback:
		return true
	default:
		return false
	}
}

type SkillResolution struct {
	ID                       string                `json:"id"`
	Workspace                string                `json:"workspace"`
	Environment              string                `json:"environment"`
	PrincipalID              string                `json:"principal_id"`
	TaskID                   string                `json:"task_id"`
	SkillID                  string                `json:"skill_id"`
	RevisionID               string                `json:"revision_id"`
	RevisionNumber           int64                 `json:"revision_number"`
	Digest                   string                `json:"digest"`
	Reason                   SkillResolutionReason `json:"reason"`
	PolicyVersion            int64                 `json:"policy_version"`
	FallbackRevisionID       string                `json:"fallback_revision_id,omitempty"`
	FallbackDigest           string                `json:"fallback_digest,omitempty"`
	AcknowledgementTokenHash string                `json:"acknowledgement_token_hash"`
	ExpiresAt                time.Time             `json:"expires_at"`
	ResolvedAt               time.Time             `json:"resolved_at"`
}

type SkillResolutionAcknowledgement struct {
	Workspace      string    `json:"workspace"`
	ResolutionID   string    `json:"resolution_id"`
	PrincipalID    string    `json:"principal_id"`
	TaskID         string    `json:"task_id"`
	RevisionID     string    `json:"revision_id"`
	RevisionDigest string    `json:"revision_digest"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

func (a SkillResolutionAcknowledgement) Validate() error {
	for field, value := range map[string]string{"workspace": a.Workspace, "resolution_id": a.ResolutionID, "principal_id": a.PrincipalID, "task_id": a.TaskID, "revision_id": a.RevisionID} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if !validSkillDigest(a.RevisionDigest) || a.AcknowledgedAt.IsZero() {
		return errors.New("skill acknowledgement digest and acknowledged_at are required")
	}
	return nil
}

func (r SkillResolution) Validate() error {
	for field, value := range map[string]string{"id": r.ID, "workspace": r.Workspace, "environment": r.Environment, "principal_id": r.PrincipalID, "task_id": r.TaskID, "skill_id": r.SkillID, "revision_id": r.RevisionID} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if r.RevisionNumber < 1 || !validSkillDigest(r.Digest) || !r.Reason.Valid() || r.PolicyVersion < 1 {
		return errors.New("skill resolution revision, digest, reason, or policy version is invalid")
	}
	if r.FallbackRevisionID != "" && !validSkillDigest(r.FallbackDigest) {
		return errors.New("skill resolution fallback_digest is invalid")
	}
	if !validSkillDigest(r.AcknowledgementTokenHash) {
		return errors.New("skill resolution acknowledgement_token_hash is invalid")
	}
	if r.ResolvedAt.IsZero() || !r.ExpiresAt.After(r.ResolvedAt) {
		return errors.New("skill resolution expires_at must follow resolved_at")
	}
	return nil
}

type SkillExecutionOutcome string

const (
	SkillExecutionSuccess   SkillExecutionOutcome = "success"
	SkillExecutionFailure   SkillExecutionOutcome = "failure"
	SkillExecutionPartial   SkillExecutionOutcome = "partial"
	SkillExecutionCancelled SkillExecutionOutcome = "cancelled"
)

func (o SkillExecutionOutcome) Valid() bool {
	return o == SkillExecutionSuccess || o == SkillExecutionFailure || o == SkillExecutionPartial || o == SkillExecutionCancelled
}

type SkillExecution struct {
	ID                    string                `json:"id"`
	Workspace             string                `json:"workspace"`
	Environment           string                `json:"environment"`
	EpisodeID             string                `json:"episode_id"`
	SkillID               string                `json:"skill_id"`
	RevisionID            string                `json:"revision_id"`
	RevisionDigest        string                `json:"revision_digest"`
	ResolutionID          string                `json:"resolution_id"`
	Acknowledged          bool                  `json:"acknowledged"`
	AcknowledgedAt        time.Time             `json:"acknowledged_at,omitempty"`
	Outcome               SkillExecutionOutcome `json:"outcome"`
	IndependentlyVerified bool                  `json:"independently_verified"`
	FailureClass          string                `json:"failure_class,omitempty"`
	StartedAt             time.Time             `json:"started_at"`
	CompletedAt           time.Time             `json:"completed_at"`
	DurationMS            int64                 `json:"duration_ms,omitempty"`
	InputTokens           int64                 `json:"input_tokens,omitempty"`
	OutputTokens          int64                 `json:"output_tokens,omitempty"`
	ToolCalls             int64                 `json:"tool_calls,omitempty"`
	FeedbackClass         string                `json:"feedback_class,omitempty"`
}

type SkillExecutionAggregate struct {
	Workspace         string  `json:"workspace"`
	Environment       string  `json:"environment"`
	SkillID           string  `json:"skill_id"`
	RevisionID        string  `json:"revision_id"`
	VerifiedSamples   int64   `json:"verified_samples"`
	VerifiedSuccesses int64   `json:"verified_successes"`
	Failures          int64   `json:"failures"`
	HarmfulFeedback   int64   `json:"harmful_feedback"`
	AverageDurationMS float64 `json:"average_duration_ms"`
}

func (e SkillExecution) Validate() error {
	for field, value := range map[string]string{"id": e.ID, "workspace": e.Workspace, "environment": e.Environment, "episode_id": e.EpisodeID, "skill_id": e.SkillID, "revision_id": e.RevisionID, "resolution_id": e.ResolutionID} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if !validSkillDigest(e.RevisionDigest) {
		return errors.New("skill execution revision_digest is invalid")
	}
	if !e.Outcome.Valid() {
		return errors.New("skill execution outcome is invalid")
	}
	if !e.Acknowledged || e.AcknowledgedAt.IsZero() {
		return errors.New("completed skill execution must be acknowledged")
	}
	if e.StartedAt.IsZero() || e.CompletedAt.IsZero() || e.CompletedAt.Before(e.StartedAt) || e.AcknowledgedAt.After(e.CompletedAt) {
		return errors.New("skill execution timestamps are invalid")
	}
	if e.DurationMS < 0 || e.InputTokens < 0 || e.OutputTokens < 0 || e.ToolCalls < 0 {
		return errors.New("skill execution metrics cannot be negative")
	}
	if len(e.FailureClass) > 256 {
		return errors.New("skill execution failure_class exceeds bound")
	}
	if len(e.FeedbackClass) > 64 {
		return errors.New("skill execution feedback_class exceeds bound")
	}
	return nil
}

type SkillRollbackEvent struct {
	ID             string    `json:"id"`
	Workspace      string    `json:"workspace"`
	Environment    string    `json:"environment"`
	SkillID        string    `json:"skill_id"`
	FromRevisionID string    `json:"from_revision_id"`
	ToRevisionID   string    `json:"to_revision_id"`
	ReasonCode     string    `json:"reason_code"`
	Automatic      bool      `json:"automatic"`
	OperationID    string    `json:"operation_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type SkillSafetySignalKind string

const (
	SkillSafetyViolation SkillSafetySignalKind = "safety_violation"
	SkillHarmfulFeedback SkillSafetySignalKind = "harmful_feedback"
	SkillDigestMismatch  SkillSafetySignalKind = "digest_mismatch"
	SkillSoftRegression  SkillSafetySignalKind = "soft_regression"
)

func (k SkillSafetySignalKind) Hard() bool {
	return k == SkillSafetyViolation || k == SkillHarmfulFeedback || k == SkillDigestMismatch
}

func (k SkillSafetySignalKind) Valid() bool { return k.Hard() || k == SkillSoftRegression }

type SkillSafetySignalState string

const (
	SkillSafetyCooldown        SkillSafetySignalState = "cooldown"
	SkillSafetyRollbackPending SkillSafetySignalState = "rollback_pending"
	SkillSafetyRollbackFailed  SkillSafetySignalState = "rollback_failed"
	SkillSafetyResolved        SkillSafetySignalState = "resolved"
)

type SkillSafetySignal struct {
	ID            string                 `json:"id"`
	Workspace     string                 `json:"workspace"`
	Environment   string                 `json:"environment"`
	SkillID       string                 `json:"skill_id"`
	RevisionID    string                 `json:"revision_id"`
	Kind          SkillSafetySignalKind  `json:"kind"`
	State         SkillSafetySignalState `json:"state"`
	Verified      bool                   `json:"verified"`
	SourceType    string                 `json:"source_type,omitempty"`
	VerifierID    string                 `json:"verifier_id,omitempty"`
	EvidenceRef   string                 `json:"evidence_reference,omitempty"`
	DedupDigest   string                 `json:"deduplication_digest,omitempty"`
	PolicyVersion int64                  `json:"policy_version,omitempty"`
	Occurrences   int64                  `json:"occurrences"`
	CooldownUntil time.Time              `json:"cooldown_until,omitempty"`
	LastError     string                 `json:"last_error,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

func (s SkillSafetySignal) Validate() error {
	for field, value := range map[string]string{"id": s.ID, "workspace": s.Workspace, "environment": s.Environment, "skill_id": s.SkillID, "revision_id": s.RevisionID} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if !s.Kind.Valid() || s.Occurrences < 1 || !s.Verified || s.CreatedAt.IsZero() || s.UpdatedAt.Before(s.CreatedAt) {
		return errors.New("skill safety signal classification, verification, occurrences, or timestamps are invalid")
	}
	if s.SourceType != "" || s.VerifierID != "" || s.EvidenceRef != "" || s.DedupDigest != "" || s.PolicyVersion != 0 {
		for field, value := range map[string]string{"source_type": s.SourceType, "verifier_id": s.VerifierID, "evidence_reference": s.EvidenceRef} {
			if err := requireSkillText(field, value, 256); err != nil {
				return err
			}
		}
		if !validSkillDigest(s.DedupDigest) || s.PolicyVersion < 1 {
			return errors.New("skill safety signal deduplication digest and policy version are invalid")
		}
	}
	if s.State != SkillSafetyCooldown && s.State != SkillSafetyRollbackPending && s.State != SkillSafetyRollbackFailed && s.State != SkillSafetyResolved {
		return errors.New("skill safety signal state is invalid")
	}
	if s.State == SkillSafetyCooldown && !s.CooldownUntil.After(s.UpdatedAt) {
		return errors.New("soft safety signal requires future cooldown")
	}
	if len(s.LastError) > MaxSkillReasonBytes {
		return errors.New("skill safety signal last_error exceeds bound")
	}
	return nil
}

func (e SkillRollbackEvent) Validate() error {
	for field, value := range map[string]string{"id": e.ID, "workspace": e.Workspace, "environment": e.Environment, "skill_id": e.SkillID, "from_revision_id": e.FromRevisionID, "to_revision_id": e.ToRevisionID, "reason_code": e.ReasonCode, "operation_id": e.OperationID} {
		if err := requireSkillText(field, value, 256); err != nil {
			return err
		}
	}
	if e.FromRevisionID == e.ToRevisionID {
		return errors.New("skill rollback must change revision")
	}
	if e.CreatedAt.IsZero() {
		return errors.New("skill rollback created_at is required")
	}
	return nil
}

func requireSkillText(field, value string, limit int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("skill %s is required", field)
	}
	if len(value) > limit {
		return fmt.Errorf("skill %s exceeds bound", field)
	}
	return nil
}

func validateSkillTextList(field string, values []string, limit, itemLimit int) error {
	if len(values) > limit {
		return fmt.Errorf("skill %s exceeds item bound", field)
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if err := requireSkillText(field, value, itemLimit); err != nil {
			return err
		}
		key := strings.TrimSpace(value)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("skill %s contains duplicate values", field)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateSkillIDList(field string, values []string) error {
	return validateSkillTextList(field, values, MaxSkillListItems, 256)
}

func validSkillDigest(value string) bool {
	return skillDigestPattern.MatchString(strings.TrimSpace(value))
}

func validSkillRelativePath(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}
