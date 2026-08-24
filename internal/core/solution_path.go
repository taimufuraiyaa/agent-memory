package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MaxSolutionGoalSummaryBytes      = 2048
	MaxSolutionStepSummaryBytes      = 4096
	MaxSolutionRationaleSummaryBytes = 2048
	MaxSolutionStateItemBytes        = 1024
	MaxSolutionReferenceTargetBytes  = 2048
	MaxSolutionReferencesPerStep     = 64
	MaxSolutionStateItems            = 100
	MaxSolutionSummaryBytes          = 16 * 1024
	MaxSolutionSummaryStepIDs        = 100
)

type SolutionEpisodeStatus string

const (
	SolutionEpisodeActive    SolutionEpisodeStatus = "active"
	SolutionEpisodePaused    SolutionEpisodeStatus = "paused"
	SolutionEpisodeCompleted SolutionEpisodeStatus = "completed"
	SolutionEpisodePartial   SolutionEpisodeStatus = "partial"
	SolutionEpisodeAbandoned SolutionEpisodeStatus = "abandoned"
	SolutionEpisodeCancelled SolutionEpisodeStatus = "cancelled"
)

func (s SolutionEpisodeStatus) Valid() bool {
	switch s {
	case SolutionEpisodeActive, SolutionEpisodePaused, SolutionEpisodeCompleted,
		SolutionEpisodePartial, SolutionEpisodeAbandoned, SolutionEpisodeCancelled:
		return true
	default:
		return false
	}
}

func (s SolutionEpisodeStatus) Terminal() bool {
	switch s {
	case SolutionEpisodeCompleted, SolutionEpisodePartial, SolutionEpisodeAbandoned, SolutionEpisodeCancelled:
		return true
	default:
		return false
	}
}

type SolutionCapturePolicy string

const (
	SolutionCaptureSummaryOnly SolutionCapturePolicy = "summary_only"
	SolutionCaptureStructured  SolutionCapturePolicy = "structured"
)

func (p SolutionCapturePolicy) Valid() bool {
	return p == SolutionCaptureSummaryOnly || p == SolutionCaptureStructured
}

type SolutionRetentionClass string

const (
	SolutionRetentionTransient SolutionRetentionClass = "transient"
	SolutionRetentionStandard  SolutionRetentionClass = "standard"
	SolutionRetentionPinned    SolutionRetentionClass = "pinned"
)

func (r SolutionRetentionClass) Valid() bool {
	return r == SolutionRetentionTransient || r == SolutionRetentionStandard || r == SolutionRetentionPinned
}

type SolutionSensitivity string

const (
	SolutionSensitivityPublic     SolutionSensitivity = "public"
	SolutionSensitivityInternal   SolutionSensitivity = "internal"
	SolutionSensitivitySensitive  SolutionSensitivity = "sensitive"
	SolutionSensitivityRestricted SolutionSensitivity = "restricted"
)

func (s SolutionSensitivity) Valid() bool {
	switch s {
	case SolutionSensitivityPublic, SolutionSensitivityInternal,
		SolutionSensitivitySensitive, SolutionSensitivityRestricted:
		return true
	default:
		return false
	}
}

type SolutionEpisode struct {
	ID             string                 `json:"id"`
	Workspace      string                 `json:"workspace"`
	SessionID      string                 `json:"session_id"`
	PrincipalID    string                 `json:"principal_id"`
	ClientID       string                 `json:"client_id"`
	GoalSummary    string                 `json:"goal_summary"`
	Status         SolutionEpisodeStatus  `json:"status"`
	CapturePolicy  SolutionCapturePolicy  `json:"capture_policy"`
	RetentionClass SolutionRetentionClass `json:"retention_class"`
	Version        int64                  `json:"version"`
	SupersededBy   string                 `json:"superseded_by,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

func (e SolutionEpisode) Validate() error {
	if err := requireSolutionText("id", e.ID, 256); err != nil {
		return err
	}
	if err := requireSolutionText("workspace", e.Workspace, 256); err != nil {
		return err
	}
	if err := requireSolutionText("session_id", e.SessionID, 256); err != nil {
		return err
	}
	if err := requireSolutionText("principal_id", e.PrincipalID, 256); err != nil {
		return err
	}
	if err := requireSolutionText("client_id", e.ClientID, 256); err != nil {
		return err
	}
	if err := requireSolutionText("goal_summary", e.GoalSummary, MaxSolutionGoalSummaryBytes); err != nil {
		return err
	}
	if !e.Status.Valid() {
		return fmt.Errorf("invalid solution episode status %q", e.Status)
	}
	if !e.CapturePolicy.Valid() {
		return fmt.Errorf("invalid solution capture_policy %q", e.CapturePolicy)
	}
	if !e.RetentionClass.Valid() {
		return fmt.Errorf("invalid solution retention_class %q", e.RetentionClass)
	}
	if e.Version < 1 {
		return errors.New("solution episode version must be at least 1")
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		return errors.New("solution episode timestamps are required")
	}
	if e.UpdatedAt.Before(e.CreatedAt) {
		return errors.New("solution episode updated_at cannot precede created_at")
	}
	return nil
}

type SolutionStepKind string

const (
	SolutionStepHypothesis  SolutionStepKind = "hypothesis"
	SolutionStepAction      SolutionStepKind = "action"
	SolutionStepObservation SolutionStepKind = "observation"
	SolutionStepDecision    SolutionStepKind = "decision"
	SolutionStepCheckpoint  SolutionStepKind = "checkpoint"
	SolutionStepResult      SolutionStepKind = "result"
	SolutionStepHandoff     SolutionStepKind = "handoff"
)

func (k SolutionStepKind) Valid() bool {
	switch k {
	case SolutionStepHypothesis, SolutionStepAction, SolutionStepObservation,
		SolutionStepDecision, SolutionStepCheckpoint, SolutionStepResult, SolutionStepHandoff:
		return true
	default:
		return false
	}
}

type SolutionStepStatus string

const (
	SolutionStepProposed  SolutionStepStatus = "proposed"
	SolutionStepRunning   SolutionStepStatus = "running"
	SolutionStepCompleted SolutionStepStatus = "completed"
	SolutionStepFailed    SolutionStepStatus = "failed"
	SolutionStepSkipped   SolutionStepStatus = "skipped"
)

func (s SolutionStepStatus) Valid() bool {
	switch s {
	case SolutionStepProposed, SolutionStepRunning, SolutionStepCompleted, SolutionStepFailed, SolutionStepSkipped:
		return true
	default:
		return false
	}
}

type SolutionReferenceKind string

const (
	SolutionReferenceObservation SolutionReferenceKind = "observation"
	SolutionReferenceMemory      SolutionReferenceKind = "memory"
	SolutionReferenceFile        SolutionReferenceKind = "file"
	SolutionReferenceTest        SolutionReferenceKind = "test"
	SolutionReferenceTool        SolutionReferenceKind = "tool"
	SolutionReferenceSkill       SolutionReferenceKind = "skill"
	SolutionReferenceArtifact    SolutionReferenceKind = "artifact"
	SolutionReferenceStep        SolutionReferenceKind = "step"
)

func (k SolutionReferenceKind) Valid() bool {
	switch k {
	case SolutionReferenceObservation, SolutionReferenceMemory, SolutionReferenceFile,
		SolutionReferenceTest, SolutionReferenceTool, SolutionReferenceSkill,
		SolutionReferenceArtifact, SolutionReferenceStep:
		return true
	default:
		return false
	}
}

type SolutionReference struct {
	Kind       SolutionReferenceKind       `json:"kind"`
	TargetID   string                      `json:"target_id"`
	Locator    string                      `json:"locator,omitempty"`
	Workspace  string                      `json:"workspace,omitempty"`
	SessionID  string                      `json:"session_id,omitempty"`
	Resolution SolutionReferenceResolution `json:"resolution,omitempty"`
}

type SolutionReferenceResolution string

const (
	SolutionReferenceUnverified SolutionReferenceResolution = "unverified"
	SolutionReferenceVerified   SolutionReferenceResolution = "verified"
	SolutionReferenceScoped     SolutionReferenceResolution = "scoped"
	SolutionReferenceTombstoned SolutionReferenceResolution = "tombstoned"
)

func (r SolutionReferenceResolution) Valid() bool {
	return r == SolutionReferenceUnverified || r == SolutionReferenceVerified || r == SolutionReferenceScoped || r == SolutionReferenceTombstoned
}

func (r SolutionReference) Validate() error {
	if !r.Kind.Valid() {
		return fmt.Errorf("invalid solution reference kind %q", r.Kind)
	}
	if err := requireSolutionText("target_id", r.TargetID, MaxSolutionReferenceTargetBytes); err != nil {
		return err
	}
	if len(r.Locator) > MaxSolutionReferenceTargetBytes {
		return fmt.Errorf("solution reference locator exceeds %d bytes", MaxSolutionReferenceTargetBytes)
	}
	if r.Resolution != "" && !r.Resolution.Valid() {
		return fmt.Errorf("invalid solution reference resolution %q", r.Resolution)
	}
	if (strings.TrimSpace(r.Workspace) == "") != (strings.TrimSpace(r.SessionID) == "") {
		return errors.New("solution reference workspace and session_id must be set together")
	}
	return nil
}

type SolutionCorrelationProposal struct {
	Reference  SolutionReference `json:"reference"`
	Basis      string            `json:"basis"`
	Confidence float64           `json:"confidence"`
}

type SolutionCorrelationResult struct {
	Proposals []SolutionCorrelationProposal `json:"proposals"`
	Ambiguous bool                          `json:"ambiguous"`
	Examined  int                           `json:"examined"`
}

type SolutionStep struct {
	ID               string              `json:"id"`
	EpisodeID        string              `json:"episode_id"`
	Ordinal          int64               `json:"ordinal"`
	Kind             SolutionStepKind    `json:"kind"`
	Status           SolutionStepStatus  `json:"status"`
	Summary          string              `json:"summary"`
	RationaleSummary string              `json:"rationale_summary,omitempty"`
	Source           string              `json:"source"`
	ParentStepIDs    []string            `json:"parent_step_ids,omitempty"`
	References       []SolutionReference `json:"references,omitempty"`
	Confidence       float64             `json:"confidence"`
	Sensitivity      SolutionSensitivity `json:"sensitivity"`
	CreatedAt        time.Time           `json:"created_at"`
}

func (s SolutionStep) Validate() error {
	if err := requireSolutionText("id", s.ID, 256); err != nil {
		return err
	}
	if err := requireSolutionText("episode_id", s.EpisodeID, 256); err != nil {
		return err
	}
	if s.Ordinal < 1 {
		return errors.New("solution step ordinal must be at least 1")
	}
	if !s.Kind.Valid() {
		return fmt.Errorf("invalid solution step kind %q", s.Kind)
	}
	if !s.Status.Valid() {
		return fmt.Errorf("invalid solution step status %q", s.Status)
	}
	if err := requireSolutionText("summary", s.Summary, MaxSolutionStepSummaryBytes); err != nil {
		return err
	}
	if len(s.RationaleSummary) > MaxSolutionRationaleSummaryBytes {
		return fmt.Errorf("rationale_summary exceeds %d bytes", MaxSolutionRationaleSummaryBytes)
	}
	if err := requireSolutionText("source", s.Source, 256); err != nil {
		return err
	}
	if s.Confidence < 0 || s.Confidence > 1 {
		return errors.New("solution step confidence must be between 0 and 1")
	}
	if !s.Sensitivity.Valid() {
		return fmt.Errorf("invalid solution step sensitivity %q", s.Sensitivity)
	}
	if s.CreatedAt.IsZero() {
		return errors.New("solution step created_at is required")
	}
	if len(s.References) > MaxSolutionReferencesPerStep {
		return fmt.Errorf("solution step references exceed %d entries", MaxSolutionReferencesPerStep)
	}
	for i, reference := range s.References {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("solution step reference %d: %w", i, err)
		}
	}
	return nil
}

type SolutionPlanStatus string

const (
	SolutionPlanPending    SolutionPlanStatus = "pending"
	SolutionPlanInProgress SolutionPlanStatus = "in_progress"
	SolutionPlanCompleted  SolutionPlanStatus = "completed"
)

func (s SolutionPlanStatus) Valid() bool {
	return s == SolutionPlanPending || s == SolutionPlanInProgress || s == SolutionPlanCompleted
}

type SolutionPlanItem struct {
	ID      string             `json:"id"`
	Summary string             `json:"summary"`
	Status  SolutionPlanStatus `json:"status"`
}

type SolutionWorkingState struct {
	EpisodeID      string              `json:"episode_id"`
	Workspace      string              `json:"workspace"`
	SessionID      string              `json:"session_id"`
	PrincipalID    string              `json:"principal_id"`
	GoalSummary    string              `json:"goal_summary"`
	Constraints    []string            `json:"constraints,omitempty"`
	PlanItems      []SolutionPlanItem  `json:"plan_items,omitempty"`
	CompletedItems []string            `json:"completed_items,omitempty"`
	OpenQuestions  []string            `json:"open_questions,omitempty"`
	NextAction     string              `json:"next_action,omitempty"`
	Artifacts      []SolutionReference `json:"artifacts,omitempty"`
	Generation     int64               `json:"generation"`
	Sensitivity    SolutionSensitivity `json:"sensitivity"`
	UpdatedAt      time.Time           `json:"updated_at"`
	ExpiresAt      time.Time           `json:"expires_at"`
}

func (s SolutionWorkingState) Validate() error {
	for name, value := range map[string]string{
		"episode_id": s.EpisodeID, "workspace": s.Workspace, "session_id": s.SessionID,
		"principal_id": s.PrincipalID, "goal_summary": s.GoalSummary,
	} {
		limit := 256
		if name == "goal_summary" {
			limit = MaxSolutionGoalSummaryBytes
		}
		if err := requireSolutionText(name, value, limit); err != nil {
			return err
		}
	}
	if s.Generation < 1 {
		return errors.New("solution working state generation must be at least 1")
	}
	if !s.Sensitivity.Valid() {
		return fmt.Errorf("invalid solution working state sensitivity %q", s.Sensitivity)
	}
	if s.UpdatedAt.IsZero() || s.ExpiresAt.IsZero() || !s.ExpiresAt.After(s.UpdatedAt) {
		return errors.New("solution working state expires_at must be after updated_at")
	}
	if err := validateSolutionStringList("constraints", s.Constraints); err != nil {
		return err
	}
	if err := validateSolutionStringList("completed_items", s.CompletedItems); err != nil {
		return err
	}
	if err := validateSolutionStringList("open_questions", s.OpenQuestions); err != nil {
		return err
	}
	if len(s.NextAction) > MaxSolutionStateItemBytes {
		return fmt.Errorf("solution working state next_action exceeds %d bytes", MaxSolutionStateItemBytes)
	}
	if len(s.PlanItems) > MaxSolutionStateItems {
		return fmt.Errorf("solution working state plan_items exceed %d entries", MaxSolutionStateItems)
	}
	for i, item := range s.PlanItems {
		if err := requireSolutionText("plan item id", item.ID, 256); err != nil {
			return fmt.Errorf("plan_items[%d]: %w", i, err)
		}
		if err := requireSolutionText("plan item summary", item.Summary, MaxSolutionStateItemBytes); err != nil {
			return fmt.Errorf("plan_items[%d]: %w", i, err)
		}
		if !item.Status.Valid() {
			return fmt.Errorf("plan_items[%d]: invalid status %q", i, item.Status)
		}
	}
	if len(s.Artifacts) > MaxSolutionReferencesPerStep {
		return fmt.Errorf("solution working state artifacts exceed %d entries", MaxSolutionReferencesPerStep)
	}
	for i, artifact := range s.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifacts[%d]: %w", i, err)
		}
	}
	return nil
}

type SolutionToolEventKind string

const (
	SolutionToolDiscovery  SolutionToolEventKind = "discovery"
	SolutionToolSelection  SolutionToolEventKind = "selection"
	SolutionToolInvocation SolutionToolEventKind = "invocation"
	SolutionToolResult     SolutionToolEventKind = "result"
)

type SolutionToolResultClass string

const (
	SolutionToolResultSuccess   SolutionToolResultClass = "success"
	SolutionToolResultFailure   SolutionToolResultClass = "failure"
	SolutionToolResultPartial   SolutionToolResultClass = "partial"
	SolutionToolResultCancelled SolutionToolResultClass = "cancelled"
	SolutionToolResultUnknown   SolutionToolResultClass = "unknown"
)

type SolutionToolInvocationRecord struct {
	ID           string                  `json:"id"`
	StepID       string                  `json:"step_id"`
	Kind         SolutionToolEventKind   `json:"kind"`
	ToolName     string                  `json:"tool_name"`
	Operation    string                  `json:"operation"`
	Capability   string                  `json:"capability"`
	InputSummary string                  `json:"input_summary,omitempty"`
	ResultClass  SolutionToolResultClass `json:"result_class"`
	DurationMS   int64                   `json:"duration_ms,omitempty"`
	Evidence     []SolutionReference     `json:"evidence,omitempty"`
	OccurredAt   time.Time               `json:"occurred_at"`
}

type SolutionValidationState string

const (
	SolutionValidationProposed SolutionValidationState = "proposed"
	SolutionValidationVerified SolutionValidationState = "verified"
	SolutionValidationRejected SolutionValidationState = "rejected"
)

type SolutionToolLesson struct {
	ID            string                  `json:"id"`
	Workspace     string                  `json:"workspace"`
	ToolName      string                  `json:"tool_name"`
	Capability    string                  `json:"capability"`
	Preconditions []string                `json:"preconditions,omitempty"`
	Limitations   []string                `json:"limitations,omitempty"`
	FailureModes  []string                `json:"failure_modes,omitempty"`
	Fallback      string                  `json:"fallback,omitempty"`
	Confidence    float64                 `json:"confidence"`
	Validation    SolutionValidationState `json:"validation"`
	SourceStepIDs []string                `json:"source_step_ids"`
	Version       int64                   `json:"version"`
	CreatedAt     time.Time               `json:"created_at"`
}

type SolutionSummary struct {
	ID                   string                  `json:"id"`
	EpisodeID            string                  `json:"episode_id"`
	Version              int64                   `json:"version"`
	Outcome              OutcomeResult           `json:"outcome"`
	Summary              string                  `json:"summary"`
	DecisiveStepIDs      []string                `json:"decisive_step_ids,omitempty"`
	UsefulFailureStepIDs []string                `json:"useful_failure_step_ids,omitempty"`
	Evidence             []SolutionReference     `json:"evidence,omitempty"`
	Risks                []string                `json:"risks,omitempty"`
	NextGuidance         string                  `json:"next_guidance,omitempty"`
	Validation           SolutionValidationState `json:"validation"`
	SupersededBy         string                  `json:"superseded_by,omitempty"`
	CreatedAt            time.Time               `json:"created_at"`
}

func (s SolutionSummary) Validate() error {
	if err := requireSolutionText("summary id", s.ID, 256); err != nil {
		return err
	}
	if err := requireSolutionText("summary episode_id", s.EpisodeID, 256); err != nil {
		return err
	}
	if s.Version < 1 {
		return errors.New("solution summary version must be at least 1")
	}
	if s.Outcome != OutcomeSuccess && s.Outcome != OutcomeFailure && s.Outcome != OutcomePartial {
		return fmt.Errorf("invalid solution summary outcome %q", s.Outcome)
	}
	if err := requireSolutionText("summary", s.Summary, MaxSolutionSummaryBytes); err != nil {
		return err
	}
	if s.Validation != SolutionValidationProposed && s.Validation != SolutionValidationVerified && s.Validation != SolutionValidationRejected {
		return fmt.Errorf("invalid solution summary validation %q", s.Validation)
	}
	if len(s.DecisiveStepIDs) > MaxSolutionSummaryStepIDs || len(s.UsefulFailureStepIDs) > MaxSolutionSummaryStepIDs {
		return errors.New("solution summary step identifiers exceed bound")
	}
	if len(s.Evidence) > MaxSolutionReferencesPerStep {
		return errors.New("solution summary evidence exceeds bound")
	}
	for i, evidence := range s.Evidence {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("summary evidence[%d]: %w", i, err)
		}
	}
	if len(s.Risks) > MaxSolutionStateItems {
		return errors.New("solution summary risks exceed bound")
	}
	for _, risk := range s.Risks {
		if len(risk) > MaxSolutionStateItemBytes {
			return errors.New("solution summary risk exceeds bound")
		}
	}
	if len(s.NextGuidance) > MaxSolutionStateItemBytes {
		return errors.New("solution summary next guidance exceeds bound")
	}
	if s.CreatedAt.IsZero() {
		return errors.New("solution summary created_at is required")
	}
	return nil
}

type SolutionPromotionKind string

const (
	SolutionPromotionMemory SolutionPromotionKind = "memory"
	SolutionPromotionSkill  SolutionPromotionKind = "skill"
)

type SolutionPromotionState string

const (
	SolutionPromotionPending   SolutionPromotionState = "pending"
	SolutionPromotionPublished SolutionPromotionState = "published"
	SolutionPromotionFailed    SolutionPromotionState = "failed"
)

type SolutionPromotion struct {
	ID             string                 `json:"id"`
	EpisodeID      string                 `json:"episode_id"`
	SummaryID      string                 `json:"summary_id"`
	Kind           SolutionPromotionKind  `json:"kind"`
	MemoryType     MemoryType             `json:"memory_type,omitempty"`
	TargetID       string                 `json:"target_id"`
	SourceStepIDs  []string               `json:"source_step_ids,omitempty"`
	ObservationIDs []string               `json:"observation_ids,omitempty"`
	State          SolutionPromotionState `json:"state"`
	Error          string                 `json:"error,omitempty"`
	PolicyIdentity string                 `json:"policy_identity"`
	CreatedAt      time.Time              `json:"created_at"`
}

func requireSolutionText(field, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("solution %s is required", field)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("solution %s exceeds %d bytes", field, maxBytes)
	}
	return nil
}

func validateSolutionStringList(field string, values []string) error {
	if len(values) > MaxSolutionStateItems {
		return fmt.Errorf("solution working state %s exceed %d entries", field, MaxSolutionStateItems)
	}
	for i, value := range values {
		if err := requireSolutionText(field, value, MaxSolutionStateItemBytes); err != nil {
			return fmt.Errorf("%s[%d]: %w", field, i, err)
		}
	}
	return nil
}
