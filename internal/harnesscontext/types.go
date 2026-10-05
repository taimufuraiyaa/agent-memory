// Package harnesscontext assembles the evidence a harness model may see. It gathers
// authorized memory, solution, repository, instruction and run chunks, gates them by
// workspace, sensitivity and freshness, and fits them into one shared prompt budget.
// Jev may advise which chunks matter, but only by opaque ID and size; it can never
// unhide, widen access or exceed the budget. Text from untrusted sources is evidence,
// never policy: authority lives in Go policy, and the rendered prompt quotes every
// evidence line so it cannot forge structure.
package harnesscontext

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"unicode/utf8"
)

type Source string

const (
	SourceMemory      Source = "memory"
	SourceSolution    Source = "solution"
	SourceRepository  Source = "repository"
	SourceInstruction Source = "instruction"
	SourceRun         Source = "run"
)

func (s Source) valid() bool {
	switch s {
	case SourceMemory, SourceSolution, SourceRepository, SourceInstruction, SourceRun:
		return true
	}
	return false
}

// priority breaks ties between equally relevant chunks, deterministically.
func (s Source) priority() int {
	switch s {
	case SourceInstruction:
		return 0
	case SourceRun:
		return 1
	case SourceSolution:
		return 2
	case SourceMemory:
		return 3
	}
	return 4
}

// Sensitivity orders data classes. A provider is eligible for a chunk only when the
// chunk's class does not exceed the provider's maximum.
type Sensitivity int

const (
	SensitivityPublic Sensitivity = iota
	SensitivityInternal
	SensitivitySensitive
	SensitivityRestricted
)

func (s Sensitivity) valid() bool { return s >= SensitivityPublic && s <= SensitivityRestricted }

func (s Sensitivity) String() string {
	switch s {
	case SensitivityPublic:
		return "public"
	case SensitivityInternal:
		return "internal"
	case SensitivitySensitive:
		return "sensitive"
	case SensitivityRestricted:
		return "restricted"
	}
	return "unknown"
}

// ParseSensitivity maps the names used by solution evidence; unknown names are
// treated as restricted by callers so a mislabelled chunk is never over-shared.
func ParseSensitivity(name string) (Sensitivity, bool) {
	switch name {
	case "public":
		return SensitivityPublic, true
	case "internal":
		return SensitivityInternal, true
	case "sensitive":
		return SensitivitySensitive, true
	case "restricted":
		return SensitivityRestricted, true
	}
	return SensitivityRestricted, false
}

// Trust says who wrote a chunk, never what it may do. No trust level grants any
// authority: tool access and approval come only from Go policy.
type Trust string

const (
	// TrustUser is the authenticated client's own words, such as the run goal.
	TrustUser Trust = "user"
	// TrustProject is a checked-in instruction file the project owner placed.
	TrustProject Trust = "project"
	// TrustUntrusted is everything else: repository content, memory captured from
	// sessions, tool and model output.
	TrustUntrusted Trust = "untrusted"
)

func (t Trust) valid() bool { return t == TrustUser || t == TrustProject || t == TrustUntrusted }

type Visibility string

const (
	VisibilityHide  Visibility = "hide"
	VisibilityShort Visibility = "short"
	VisibilityLong  Visibility = "long"
	VisibilityFull  Visibility = "full"
)

const (
	lifecycleActive = "active"

	DefaultMaxChunkTokens = 4000
	DefaultShortTokens    = 64
	// MaxAdviceCandidates bounds what is ever offered to an advisor.
	MaxAdviceCandidates = 64
	// AdviceBoost is how much a promoted chunk's relevance is raised. It is a nudge,
	// not an override: advice can reorder close calls but not outrank clearly better evidence.
	AdviceBoost          = 0.25
	DefaultAdviceMinConf = 0.6
)

var (
	ErrInvalid      = errors.New("invalid context request")
	ErrBudgetPinned = errors.New("pinned instructions do not fit the prompt budget")

	chunkIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	wsRE      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Chunk is one candidate piece of evidence as an adapter reports it.
type Chunk struct {
	ID        string
	Workspace string
	Source    Source
	// Ref locates the source (a path or a memory ID); Revision identifies the exact
	// version read, so a changed source can be detected and provenance kept.
	Ref         string
	Revision    string
	Title       string
	Text        string
	Short       string
	Sensitivity Sensitivity
	Trust       Trust
	// Pinned marks an active instruction that must be present. Only user and project
	// trust may pin; an untrusted chunk's pin is ignored.
	Pinned    bool
	Relevance float64
	// Lifecycle is "active" or empty for usable chunks; anything else is excluded.
	Lifecycle string
}

// Item is one chunk as it will be shown, with the provenance that survives compaction.
type Item struct {
	ID          string     `json:"id"`
	Source      Source     `json:"source"`
	Ref         string     `json:"ref"`
	Revision    string     `json:"revision"`
	Title       string     `json:"title"`
	Sensitivity string     `json:"sensitivity"`
	Trust       Trust      `json:"trust"`
	Visibility  Visibility `json:"visibility"`
	Pinned      bool       `json:"pinned"`
	Tokens      int        `json:"tokens"`
	Truncated   bool       `json:"truncated,omitempty"`
	Suspicious  bool       `json:"suspicious,omitempty"`
	// Class is the item's sensitivity as an ordered value, for routing.
	Class Sensitivity `json:"-"`
	// Text is what the model sees at this visibility, already redacted and bounded.
	Text string `json:"-"`
}

// Exclusion records why a chunk is absent, by ID and a fixed reason code, never by content.
type Exclusion struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Exclusion reasons.
const (
	ReasonScope       = "scope"
	ReasonSensitivity = "sensitivity"
	ReasonLifecycle   = "lifecycle"
	ReasonStale       = "stale"
	ReasonDuplicate   = "duplicate"
	ReasonInvalid     = "invalid"
	ReasonBudget      = "budget"
	ReasonDenied      = "denied_path"
	ReasonBinary      = "binary"
	ReasonUnreadable  = "unreadable"
)

// Advice outcomes.
const (
	AdviceNone       = "none"
	AdviceApplied    = "applied"
	AdviceLowConf    = "ignored_low_confidence"
	AdviceInvalid    = "ignored_invalid"
	AdviceFailed     = "failed"
	AdviceTimeout    = "timeout"
	AdviceNoEligible = "no_eligible_candidates"
)

// Report summarizes an assembly without any content.
type Report struct {
	Candidates    int            `json:"candidates"`
	Included      int            `json:"included"`
	Excluded      map[string]int `json:"excluded"`
	Advice        string         `json:"advice"`
	Truncated     int            `json:"truncated"`
	Suspicious    int            `json:"suspicious"`
	PinIgnored    int            `json:"pin_ignored"`
	TokensUsed    int            `json:"tokens_used"`
	TokensAvail   int            `json:"tokens_available"`
	PinnedTokens  int            `json:"pinned_tokens"`
	AdviceApplied []string       `json:"-"`
}

type Assembled struct {
	Items     []Item
	Excluded  []Exclusion
	Report    Report
	Boundary  string
	PolicyTok int
	// PrefixID identifies the stable leading part of the rendered prompt across turns.
	PrefixID string
}

// MaxClass is the most sensitive class among the included items: the data class of the
// prompt, which decides which providers may be sent it.
func (a Assembled) MaxClass() Sensitivity {
	highest := SensitivityPublic
	for _, it := range a.Items {
		if it.Class > highest {
			highest = it.Class
		}
	}
	return highest
}

// Eligibility is what a provider may be shown. Advisor limits what chunk metadata may
// even be offered to Jev, which is a separate egress from the model.
type Eligibility struct {
	MaxSensitivity        Sensitivity
	AdvisorMaxSensitivity Sensitivity
}

// Budget splits one model window. Tool schemas and reserved output come off the top, so
// evidence, pinned instructions and tool schemas are coordinated against one limit.
type Budget struct {
	Total         int
	ToolSchema    int
	ReserveOutput int
}

type Request struct {
	Workspace   string
	Eligibility Eligibility
	Budget      Budget
	// BoundarySeed, when set, makes the section boundary stable for everything sharing
	// the seed, such as the turns of one run. Empty means a fresh random boundary.
	BoundarySeed string
}

func (r Request) validate() error {
	if !wsRE.MatchString(r.Workspace) {
		return fmt.Errorf("%w: workspace", ErrInvalid)
	}
	if !r.Eligibility.MaxSensitivity.valid() || !r.Eligibility.AdvisorMaxSensitivity.valid() {
		return fmt.Errorf("%w: eligibility", ErrInvalid)
	}
	b := r.Budget
	if b.Total < 1 || b.ToolSchema < 0 || b.ReserveOutput < 0 || b.ToolSchema+b.ReserveOutput >= b.Total {
		return fmt.Errorf("%w: budget", ErrInvalid)
	}
	return nil
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }
