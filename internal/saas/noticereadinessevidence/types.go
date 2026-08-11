package noticereadinessevidence

import "time"

const (
	InputSchemaV1   = "agent-memory-launch-notice-readiness-input-v1"
	ReceiptSchemaV1 = "agent-memory-launch-notice-readiness-receipt-v1"
)

type Outcome string
type StaffingDomainID string
type ScenarioID string
type CheckID string

const (
	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"

	StaffingNoticeIntake StaffingDomainID = "notice_intake"
	StaffingLegalReview  StaffingDomainID = "legal_review"
	StaffingUserResponse StaffingDomainID = "user_response"

	ScenarioValid       ScenarioID = "valid_notice"
	ScenarioInvalid     ScenarioID = "invalid_notice"
	ScenarioConflicting ScenarioID = "conflicting_notice"
	ScenarioUrgent      ScenarioID = "urgent_notice"

	CheckLaunchScopeReady       CheckID = "launch_scope_ready"
	CheckWorkflowPolicyComplete CheckID = "workflow_policy_complete"
	CheckJurisdictionRouting    CheckID = "jurisdiction_routing_complete"
	CheckCopyLanguageCoverage   CheckID = "copy_language_coverage_complete"
	CheckDeadlinePolicy         CheckID = "deadline_policy_complete"
	CheckEscalationPaths        CheckID = "escalation_paths_complete"
	CheckStaffingCoverage       CheckID = "staffing_coverage_complete"
	CheckTabletopScenarios      CheckID = "tabletop_scenarios_complete"
	CheckRepeatAbuseReview      CheckID = "repeat_abuse_review_complete"
	CheckAccountableReview      CheckID = "accountable_reviews_complete"
)

var (
	requiredStaffing  = []StaffingDomainID{StaffingNoticeIntake, StaffingLegalReview, StaffingUserResponse}
	requiredScenarios = []ScenarioID{ScenarioValid, ScenarioInvalid, ScenarioConflicting, ScenarioUrgent}
	requiredChecks    = []CheckID{
		CheckLaunchScopeReady, CheckWorkflowPolicyComplete, CheckJurisdictionRouting,
		CheckCopyLanguageCoverage, CheckDeadlinePolicy, CheckEscalationPaths,
		CheckStaffingCoverage, CheckTabletopScenarios, CheckRepeatAbuseReview,
		CheckAccountableReview,
	}
)

type Route struct {
	JurisdictionRefSHA256           string  `json:"jurisdiction_ref_sha256"`
	RequiredLanguageCount           int     `json:"required_language_count"`
	CoveredLanguageCount            int     `json:"covered_language_count"`
	NormalValidationDeadlineSeconds int     `json:"normal_validation_deadline_seconds"`
	UrgentValidationDeadlineSeconds int     `json:"urgent_validation_deadline_seconds"`
	PrimaryEscalationPathCount      int     `json:"primary_escalation_path_count"`
	BackupEscalationPathCount       int     `json:"backup_escalation_path_count"`
	CopySHA256                      string  `json:"copy_sha256"`
	RoutingSHA256                   string  `json:"routing_sha256"`
	DeadlinePolicySHA256            string  `json:"deadline_policy_sha256"`
	EscalationSHA256                string  `json:"escalation_sha256"`
	Outcome                         Outcome `json:"outcome"`
}

type StaffingDomain struct {
	ID                      StaffingDomainID `json:"id"`
	RequiredCoverageMinutes int              `json:"required_coverage_minutes"`
	PrimaryCoveredMinutes   int              `json:"primary_covered_minutes"`
	BackupCoveredMinutes    int              `json:"backup_covered_minutes"`
	PrimarySlotCount        int              `json:"primary_slot_count"`
	BackupSlotCount         int              `json:"backup_slot_count"`
	Outcome                 Outcome          `json:"outcome"`
	EvidenceSHA256          string           `json:"evidence_sha256"`
}

type TabletopScenario struct {
	ID                     ScenarioID `json:"id"`
	ExecutedCount          int        `json:"executed_count"`
	PassedCount            int        `json:"passed_count"`
	FailedCount            int        `json:"failed_count"`
	InconclusiveCount      int        `json:"inconclusive_count"`
	MaximumTargetSeconds   int        `json:"maximum_target_seconds"`
	MaximumObservedSeconds int        `json:"maximum_observed_seconds"`
	Outcome                Outcome    `json:"outcome"`
	EvidenceSHA256         string     `json:"evidence_sha256"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                      string             `json:"schema"`
	Classification              string             `json:"classification"`
	Environment                 string             `json:"environment"`
	ReviewID                    string             `json:"review_id"`
	WorkflowPolicyVersion       string             `json:"workflow_policy_version"`
	CopyManifestVersion         string             `json:"copy_manifest_version"`
	RoutingPolicyVersion        string             `json:"routing_policy_version"`
	DeadlinePolicyVersion       string             `json:"deadline_policy_version"`
	EscalationPolicyVersion     string             `json:"escalation_policy_version"`
	RepeatAbusePolicyVersion    string             `json:"repeat_abuse_policy_version"`
	TabletopVersion             string             `json:"tabletop_version"`
	StaffingPlanVersion         string             `json:"staffing_plan_version"`
	ScopeDecisionID             string             `json:"scope_decision_id"`
	LaunchScopeReceiptSHA256    string             `json:"launch_scope_receipt_sha256"`
	WorkflowPolicySHA256        string             `json:"workflow_policy_sha256"`
	CopyManifestSHA256          string             `json:"copy_manifest_sha256"`
	RoutingPolicySHA256         string             `json:"routing_policy_sha256"`
	DeadlinePolicySHA256        string             `json:"deadline_policy_sha256"`
	EscalationPolicySHA256      string             `json:"escalation_policy_sha256"`
	RepeatAbusePolicySHA256     string             `json:"repeat_abuse_policy_sha256"`
	TabletopReportSHA256        string             `json:"tabletop_report_sha256"`
	StaffingPlanSHA256          string             `json:"staffing_plan_sha256"`
	CounselReviewSHA256         string             `json:"counsel_review_sha256"`
	LegalOperationsReviewSHA256 string             `json:"legal_operations_review_sha256"`
	SupportReviewSHA256         string             `json:"support_review_sha256"`
	ReviewedAt                  time.Time          `json:"reviewed_at"`
	GeneratedAt                 time.Time          `json:"generated_at"`
	Ready                       bool               `json:"ready"`
	Routes                      []Route            `json:"routes"`
	Staffing                    []StaffingDomain   `json:"staffing"`
	Scenarios                   []TabletopScenario `json:"scenarios"`
	Checks                      []Check            `json:"checks"`
}

type Receipt struct {
	Schema                     string             `json:"schema"`
	Classification             string             `json:"classification"`
	Environment                string             `json:"environment"`
	ReviewID                   string             `json:"review_id"`
	WorkflowPolicyVersion      string             `json:"workflow_policy_version"`
	CopyManifestVersion        string             `json:"copy_manifest_version"`
	RoutingPolicyVersion       string             `json:"routing_policy_version"`
	DeadlinePolicyVersion      string             `json:"deadline_policy_version"`
	EscalationPolicyVersion    string             `json:"escalation_policy_version"`
	RepeatAbusePolicyVersion   string             `json:"repeat_abuse_policy_version"`
	TabletopVersion            string             `json:"tabletop_version"`
	StaffingPlanVersion        string             `json:"staffing_plan_version"`
	ScopeDecisionID            string             `json:"scope_decision_id"`
	LaunchScopeReceiptSHA256   string             `json:"launch_scope_receipt_sha256"`
	InputSHA256                string             `json:"input_sha256"`
	ScopeDecisionVersion       string             `json:"scope_decision_version"`
	JurisdictionPolicyVersion  string             `json:"jurisdiction_policy_version"`
	LegalReviewVersion         string             `json:"legal_review_version"`
	SupportLanguageCount       int                `json:"support_language_count"`
	NoticeJurisdictionCount    int                `json:"notice_jurisdiction_count"`
	ReviewedAt                 time.Time          `json:"reviewed_at"`
	GeneratedAt                time.Time          `json:"generated_at"`
	CollectedAt                time.Time          `json:"collected_at"`
	Ready                      bool               `json:"ready"`
	RouteCount                 int                `json:"route_count"`
	CoveredRouteCount          int                `json:"covered_route_count"`
	StaffingDomainCount        int                `json:"staffing_domain_count"`
	CoveredStaffingDomainCount int                `json:"covered_staffing_domain_count"`
	ScenarioCount              int                `json:"scenario_count"`
	PassedScenarioCount        int                `json:"passed_scenario_count"`
	FailedScenarioCount        int                `json:"failed_scenario_count"`
	InconclusiveScenarioCount  int                `json:"inconclusive_scenario_count"`
	CheckCount                 int                `json:"check_count"`
	PassedCount                int                `json:"passed_count"`
	FailedCount                int                `json:"failed_count"`
	InconclusiveCount          int                `json:"inconclusive_count"`
	EvidenceDigests            map[string]string  `json:"evidence_digests"`
	Routes                     []Route            `json:"routes"`
	Staffing                   []StaffingDomain   `json:"staffing"`
	Scenarios                  []TabletopScenario `json:"scenarios"`
	Checks                     []Check            `json:"checks"`
}

func RequiredStaffingDomains() []StaffingDomainID {
	return append([]StaffingDomainID(nil), requiredStaffing...)
}
func RequiredScenarios() []ScenarioID { return append([]ScenarioID(nil), requiredScenarios...) }
func RequiredChecks() []CheckID       { return append([]CheckID(nil), requiredChecks...) }
