package engine

import (
	"context"
	"regexp"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	maxSolutionAdmissionGoalBytes      = core.MaxSolutionGoalSummaryBytes
	maxSolutionAdmissionStepBytes      = core.MaxSolutionStepSummaryBytes
	maxSolutionAdmissionRationaleBytes = core.MaxSolutionRationaleSummaryBytes
)

type SolutionAdmissionDisposition string

const (
	SolutionAdmissionAllow      SolutionAdmissionDisposition = "allow"
	SolutionAdmissionRedact     SolutionAdmissionDisposition = "redact"
	SolutionAdmissionQuarantine SolutionAdmissionDisposition = "quarantine"
	SolutionAdmissionReject     SolutionAdmissionDisposition = "reject"
)

type SolutionAdmissionReason string

const (
	SolutionAdmissionAccepted        SolutionAdmissionReason = "accepted"
	SolutionAdmissionEmpty           SolutionAdmissionReason = "empty"
	SolutionAdmissionInvalidField    SolutionAdmissionReason = "invalid_field"
	SolutionAdmissionInvalidOrigin   SolutionAdmissionReason = "invalid_origin"
	SolutionAdmissionTooLarge        SolutionAdmissionReason = "too_large"
	SolutionAdmissionRawReasoning    SolutionAdmissionReason = "raw_reasoning"
	SolutionAdmissionPrivateContent  SolutionAdmissionReason = "private_content"
	SolutionAdmissionSecret          SolutionAdmissionReason = "secret"
	SolutionAdmissionPII             SolutionAdmissionReason = "pii"
	SolutionAdmissionPromptInjection SolutionAdmissionReason = "prompt_injection"
)

type SolutionAdmissionOrigin string

const (
	SolutionOriginAgent     SolutionAdmissionOrigin = "agent"
	SolutionOriginModel     SolutionAdmissionOrigin = "model"
	SolutionOriginTool      SolutionAdmissionOrigin = "tool"
	SolutionOriginHuman     SolutionAdmissionOrigin = "human"
	SolutionOriginHeuristic SolutionAdmissionOrigin = "heuristic"
)

func (o SolutionAdmissionOrigin) valid() bool {
	switch o {
	case SolutionOriginAgent, SolutionOriginModel, SolutionOriginTool, SolutionOriginHuman, SolutionOriginHeuristic:
		return true
	default:
		return false
	}
}

type SolutionAdmissionField string

const (
	SolutionFieldGoalSummary       SolutionAdmissionField = "goal_summary"
	SolutionFieldStepSummary       SolutionAdmissionField = "step_summary"
	SolutionFieldRationaleSummary  SolutionAdmissionField = "rationale_summary"
	SolutionFieldWorkingStateItem  SolutionAdmissionField = "working_state_item"
	SolutionFieldToolInputSummary  SolutionAdmissionField = "tool_input_summary"
	SolutionFieldToolResultSummary SolutionAdmissionField = "tool_result_summary"
)

func (f SolutionAdmissionField) maxBytes() (int, bool) {
	switch f {
	case SolutionFieldGoalSummary:
		return maxSolutionAdmissionGoalBytes, true
	case SolutionFieldStepSummary:
		return maxSolutionAdmissionStepBytes, true
	case SolutionFieldRationaleSummary:
		return maxSolutionAdmissionRationaleBytes, true
	case SolutionFieldWorkingStateItem, SolutionFieldToolInputSummary, SolutionFieldToolResultSummary:
		return core.MaxSolutionStateItemBytes, true
	default:
		return 0, false
	}
}

type SolutionAdmissionInput struct {
	Workspace string
	Origin    SolutionAdmissionOrigin
	Field     SolutionAdmissionField
	Content   string
}

type SolutionAdmissionDecision struct {
	Disposition SolutionAdmissionDisposition `json:"disposition"`
	Reason      SolutionAdmissionReason      `json:"reason"`
	SafeContent string                       `json:"safe_content,omitempty"`
}

type SolutionAdmissionPolicy struct {
	rawReasoningPatterns []*regexp.Regexp
	promptInjection      []*regexp.Regexp
}

func NewSolutionAdmissionPolicy() *SolutionAdmissionPolicy {
	return &SolutionAdmissionPolicy{
		rawReasoningPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b(chain[- ]of[- ]thought|hidden reasoning|private reasoning|internal monologue|reasoning scratchpad)\s*[:=]`),
			regexp.MustCompile(`(?i)\b(save|store|record|persist|include|reveal|show)\b.{0,48}\b(chain[- ]of[- ]thought|hidden reasoning|private reasoning|internal monologue|reasoning scratchpad)\b`),
		},
		promptInjection: []*regexp.Regexp{
			regexp.MustCompile(`(?i)ignore (all )?previous instructions`),
			regexp.MustCompile(`(?i)reveal (the )?system prompt`),
			regexp.MustCompile(`(?i)exfiltrate|steal credentials|bypass guardrails`),
		},
	}
}

func (p *SolutionAdmissionPolicy) Evaluate(_ context.Context, in SolutionAdmissionInput) SolutionAdmissionDecision {
	if p == nil {
		return rejectedSolutionAdmission(SolutionAdmissionInvalidField)
	}
	maxBytes, validField := in.Field.maxBytes()
	if !validField {
		return rejectedSolutionAdmission(SolutionAdmissionInvalidField)
	}
	if !in.Origin.valid() {
		return rejectedSolutionAdmission(SolutionAdmissionInvalidOrigin)
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return rejectedSolutionAdmission(SolutionAdmissionEmpty)
	}
	if len(content) > maxBytes {
		return rejectedSolutionAdmission(SolutionAdmissionTooLarge)
	}
	if matchesSolutionPattern(p.rawReasoningPatterns, content) {
		return rejectedSolutionAdmission(SolutionAdmissionRawReasoning)
	}
	if privateTagRE.MatchString(content) {
		return SolutionAdmissionDecision{
			Disposition: SolutionAdmissionRedact,
			Reason:      SolutionAdmissionPrivateContent,
			SafeContent: RedactSecretsAndPII(content),
		}
	}
	if matchesSolutionPattern(SecretPatterns, content) {
		return quarantinedSolutionAdmission(SolutionAdmissionSecret)
	}
	if matchesSolutionPattern(PIISecretPatterns, content) {
		return quarantinedSolutionAdmission(SolutionAdmissionPII)
	}
	if matchesSolutionPattern(p.promptInjection, content) {
		return rejectedSolutionAdmission(SolutionAdmissionPromptInjection)
	}
	return SolutionAdmissionDecision{
		Disposition: SolutionAdmissionAllow,
		Reason:      SolutionAdmissionAccepted,
		SafeContent: content,
	}
}

func matchesSolutionPattern(patterns []*regexp.Regexp, content string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}

func rejectedSolutionAdmission(reason SolutionAdmissionReason) SolutionAdmissionDecision {
	return SolutionAdmissionDecision{Disposition: SolutionAdmissionReject, Reason: reason}
}

func quarantinedSolutionAdmission(reason SolutionAdmissionReason) SolutionAdmissionDecision {
	return SolutionAdmissionDecision{Disposition: SolutionAdmissionQuarantine, Reason: reason}
}
