// Package harness defines provider-neutral contracts for the local coding harness.
// It does not execute models, Jev decisions, tools, or commands.
package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
)

const ContractVersion = 1
const MaxPayloadBytes = 1 << 20

type ProviderID string
type CapabilityID string
type ProviderKind string

const (
	KindDecision ProviderKind = "decision"
	KindModel    ProviderKind = "model"
	KindTool     ProviderKind = "tool"
)

type Outcome string

const (
	OutcomeOK          Outcome = "ok"
	OutcomePartial     Outcome = "partial"
	OutcomeUnsupported Outcome = "unsupported"
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeDenied      Outcome = "denied"
	OutcomeStale       Outcome = "stale"
	OutcomeTimeout     Outcome = "timeout"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeFailed      Outcome = "failed"
)

var (
	ErrInvalid   = errors.New("invalid harness contract")
	ErrDuplicate = errors.New("duplicate harness provider")
	ErrUnknown   = errors.New("unknown harness provider")
	ErrStale     = errors.New("stale harness result")
	identityRE   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

// Manifest declares potential support. It is not proof of live access.
type Manifest struct {
	Version      int
	ID           ProviderID
	Kind         ProviderKind
	Capabilities []CapabilityID
}

func (m Manifest) Validate() error {
	if m.Version != ContractVersion || !identityRE.MatchString(string(m.ID)) {
		return fmt.Errorf("%w: unsupported version or provider ID", ErrInvalid)
	}
	if m.Kind != KindDecision && m.Kind != KindModel && m.Kind != KindTool {
		return fmt.Errorf("%w: provider kind", ErrInvalid)
	}
	if len(m.Capabilities) == 0 || len(m.Capabilities) > 64 {
		return fmt.Errorf("%w: capability count", ErrInvalid)
	}
	seen := make(map[CapabilityID]struct{}, len(m.Capabilities))
	for _, capability := range m.Capabilities {
		if !identityRE.MatchString(string(capability)) {
			return fmt.Errorf("%w: capability ID", ErrInvalid)
		}
		if _, exists := seen[capability]; exists {
			return fmt.Errorf("%w: duplicate capability", ErrInvalid)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

type AccessState string

const (
	AccessAvailable   AccessState = "available"
	AccessUnavailable AccessState = "unavailable"
	AccessDenied      AccessState = "denied"
	AccessUnsupported AccessState = "unsupported"
)

// Scope binds a live provider session and every operation to one run generation.
type Scope struct {
	Workspace string
	// Run optionally identifies one run so concurrent runs in a workspace keep
	// independent provider sessions instead of superseding each other.
	Run        string
	Generation uint64
}

func (s Scope) Validate() error {
	if !identityRE.MatchString(s.Workspace) || s.Generation == 0 || (s.Run != "" && !identityRE.MatchString(s.Run)) {
		return fmt.Errorf("%w: workspace, run or generation", ErrInvalid)
	}
	return nil
}

type LiveAccess struct {
	Version      int
	Provider     ProviderID
	Scope        Scope
	Revision     uint64
	Capabilities map[CapabilityID]AccessState
}

// Validate checks live states against a static manifest; absence is not availability.
func (a LiveAccess) Validate(m Manifest, scope Scope) error {
	if a.Version != ContractVersion || a.Provider != m.ID || a.Scope != scope || a.Revision == 0 {
		return ErrStale
	}
	allowed := make(map[CapabilityID]struct{}, len(m.Capabilities))
	for _, id := range m.Capabilities {
		allowed[id] = struct{}{}
	}
	for id, state := range a.Capabilities {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("%w: undeclared capability", ErrInvalid)
		}
		switch state {
		case AccessAvailable, AccessUnavailable, AccessDenied, AccessUnsupported:
		default:
			return fmt.Errorf("%w: access state", ErrInvalid)
		}
	}
	return nil
}

// Envelope is echoed by a provider result; it prevents late or cross-scope use.
type Envelope struct {
	Version        int
	Provider       ProviderID
	Scope          Scope
	AccessRevision uint64
	Capability     CapabilityID
	MaxBytes       int
}

func (e Envelope) Validate(access LiveAccess) error {
	if err := e.validateShape(access); err != nil {
		return err
	}
	if access.Capabilities[e.Capability] != AccessAvailable {
		return fmt.Errorf("%w: capability is not available", ErrInvalid)
	}
	return nil
}

// validateShape checks identity, scope, revision and bounds but not availability,
// so a session can report a typed unsupported or denied outcome instead of an error.
func (e Envelope) validateShape(access LiveAccess) error {
	if e.Version != ContractVersion || e.Provider != access.Provider || e.Scope != access.Scope || e.AccessRevision != access.Revision {
		return ErrStale
	}
	if e.MaxBytes < 1 || e.MaxBytes > MaxPayloadBytes || !identityRE.MatchString(string(e.Capability)) {
		return fmt.Errorf("%w: operation bounds", ErrInvalid)
	}
	return nil
}

func ValidateReply(request, reply Envelope, outcome Outcome, payloadBytes int) error {
	if reply != request {
		return ErrStale
	}
	if payloadBytes < 0 || payloadBytes > request.MaxBytes {
		return fmt.Errorf("%w: response size", ErrInvalid)
	}
	switch outcome {
	case OutcomeOK, OutcomePartial, OutcomeUnsupported, OutcomeUnavailable, OutcomeDenied, OutcomeStale, OutcomeTimeout, OutcomeCancelled, OutcomeFailed:
		return nil
	default:
		return fmt.Errorf("%w: outcome", ErrInvalid)
	}
}

type EvidenceRef struct {
	ID       string
	Revision string
	Class    string
}

type DecisionQuestion struct {
	Envelope
	Kind       string
	Candidates []string
	Evidence   []EvidenceRef
}

type DecisionAnswer struct {
	Envelope
	Outcome    Outcome
	Selected   []string
	Confidence float64
}

// ValidateDecisionAnswer rejects advisory choices outside the supplied set.
// Access and execution authority remain with deterministic policy.
func ValidateDecisionAnswer(question DecisionQuestion, answer DecisionAnswer) error {
	bytes := 0
	for _, selected := range answer.Selected {
		bytes += len(selected)
	}
	if err := ValidateReply(question.Envelope, answer.Envelope, answer.Outcome, bytes); err != nil {
		return err
	}
	if math.IsNaN(answer.Confidence) || math.IsInf(answer.Confidence, 0) || answer.Confidence < 0 || answer.Confidence > 1 {
		return fmt.Errorf("%w: decision confidence", ErrInvalid)
	}
	candidates := make(map[string]struct{}, len(question.Candidates))
	seen := make(map[string]struct{}, len(answer.Selected))
	for _, candidate := range question.Candidates {
		candidates[candidate] = struct{}{}
	}
	for _, selected := range answer.Selected {
		if _, exists := candidates[selected]; !exists {
			return fmt.Errorf("%w: unknown decision candidate", ErrInvalid)
		}
		if _, duplicate := seen[selected]; duplicate {
			return fmt.Errorf("%w: duplicate decision candidate", ErrInvalid)
		}
		seen[selected] = struct{}{}
	}
	return nil
}

// MaxPromptBytes bounds the assembled prompt a model request may carry.
const MaxPromptBytes = 256 << 10

type ModelRequest struct {
	Envelope
	InputRefs       []EvidenceRef
	MaxOutputTokens int
	ToolSchemaIDs   []string
	// Prompt is the assembled, redacted context to send, within MaxPromptBytes. A
	// provider sends nothing else from the run.
	Prompt string
	// PrefixID is a stable identity for the leading part of the prompt that does not
	// change between turns (policy, pinned instructions, tool schemas). Empty means no
	// cache claim is made; it is never a retrieval-query cache key.
	PrefixID string
}

// Usage is what a provider reports it consumed. Cached input is evidence of prompt-prefix
// reuse only when the provider itself reports it.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

// MaxUsageTokens bounds a reported token count so accounting arithmetic cannot overflow.
const MaxUsageTokens = 1 << 31

func (u Usage) validate() bool {
	return u.InputTokens >= 0 && u.InputTokens <= MaxUsageTokens && u.OutputTokens >= 0 && u.OutputTokens <= MaxUsageTokens &&
		u.CachedInputTokens >= 0 && u.CachedInputTokens <= u.InputTokens
}

type ModelAnswer struct {
	Envelope
	Outcome Outcome
	Text    string
	ToolID  string
	// ToolArguments accompany ToolID and are bounded with the rest of the reply.
	ToolArguments []byte
	// CostMicros is the provider-reported cost of this call in millionths of the
	// account currency unit; it is kept on every outcome because failed calls can
	// still cost money.
	CostMicros int64
	// Usage and Model are the provider's own report of what ran and how much it used;
	// they are kept on every outcome because failed calls can still consume tokens.
	Usage Usage
	Model string
}

// MaxCostMicros bounds one reported call cost so a faulty provider cannot overflow
// spend accounting.
const MaxCostMicros int64 = 1_000_000_000_000

type ToolRequest struct {
	Envelope
	ToolID    string
	Arguments []byte
}

// Limits on what a prepared action may report about a change.
const (
	MaxActionPaths   = 32
	MaxActionPath    = 512
	MaxPreviewBytes  = 48 << 10
	MaxEscalations   = 8
	maxEscalationLen = 32
)

type PreparedAction struct {
	Envelope
	Outcome Outcome
	Digest  string
	Summary string
	// Paths are the project-relative paths the action would change; empty for a read.
	Paths []string
	// Preview is a bounded description of the change for a person to review. It goes to
	// the trusted approval channel only and is never offered to a model or a client.
	Preview string
	// Reason is a fixed code saying why a preparation did not succeed, such as
	// "no_match", so a model can correct itself; it carries no file content.
	Reason string
	// Escalate lists fixed reason codes that can only raise how carefully a change is
	// approved, never lower it: policy classifies the paths itself and treats these as
	// extra reasons for friction.
	Escalate []string
}

type ToolAnswer struct {
	Envelope
	Outcome Outcome
	Output  []byte
}

// Provider only probes live access and closes its session. Operations are split
// into capability-specific ports so partial providers need not stub methods.
type Provider interface {
	Probe(context.Context, Scope) (LiveAccess, error)
	Close() error
}

type DecisionProvider interface {
	Provider
	Decide(context.Context, DecisionQuestion) (DecisionAnswer, error)
}

type ModelProvider interface {
	Provider
	Generate(context.Context, ModelRequest) (ModelAnswer, error)
}

type ToolProvider interface {
	Provider
	Prepare(context.Context, ToolRequest) (PreparedAction, error)
	Invoke(context.Context, PreparedAction) (ToolAnswer, error)
}
