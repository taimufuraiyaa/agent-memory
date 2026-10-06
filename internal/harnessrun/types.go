// Package harnessrun owns the transient run state of the local coding harness: a
// versioned state machine, a private file repository for bounded chunks, events,
// checkpoints, idempotency records and artifacts, and the bounded loop that drives
// providers through harness sessions. Nothing here is durable memory: runs expire,
// raw provider output is bounded, and status and events carry codes, not content.
package harnessrun

import (
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	schemaVersion = 1

	MaxGoalBytes      = 4096
	MaxInputBytes     = 4096
	MaxPromptBytes    = 1024
	MaxChunkBytes     = 16 << 10
	MaxChunks         = 16
	MaxArtifactBytes  = 64 << 10
	MaxArtifacts      = 8
	MaxEvents         = 256
	MaxIdempotency    = 64
	MaxEventPageLimit = 100
	maxRunFileBytes   = 2 << 20

	// ToolClarify is the reserved pseudo-tool a model uses to ask the client a
	// question. It never reaches a tool provider.
	ToolClarify = "clarify"
)

type State string

const (
	StateQueued         State = "queued"
	StateRunning        State = "running"
	StateNeedsAttention State = "needs_attention"
	StateCancelling     State = "cancelling"
	StateCompleted      State = "completed"
	StatePartial        State = "partial"
	StateFailed         State = "failed"
	StateCancelled      State = "cancelled"
)

func (s State) Terminal() bool {
	return s == StateCompleted || s == StatePartial || s == StateFailed || s == StateCancelled
}

func (s State) valid() bool {
	switch s {
	case StateQueued, StateRunning, StateNeedsAttention, StateCancelling, StateCompleted, StatePartial, StateFailed, StateCancelled:
		return true
	}
	return false
}

var (
	ErrNotFound            = errors.New("harness run not found")
	ErrStaleGeneration     = errors.New("stale run generation")
	ErrInvalidState        = errors.New("run state does not allow this operation")
	ErrIdempotencyConflict = errors.New("idempotency key reused for a different request")
	ErrInvalid             = errors.New("invalid harness run request")
	ErrBudget              = errors.New("harness run budget exceeded")
	ErrBusy                = errors.New("harness runtime is busy")
	ErrInvalidCursor       = errors.New("invalid event cursor")
	ErrApprovalRequired    = errors.New("run is waiting for trusted local approval")
	ErrStorage             = errors.New("harness run storage unavailable")
	ErrClosed              = errors.New("harness runtime is closed")

	runIDRE       = regexp.MustCompile(`^run_[a-f0-9]{32}$`)
	clientIDRE    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	workspaceRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	grantIDRE     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	idempotencyRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)
	codeRE        = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	approvalIDRE  = regexp.MustCompile(`^apr_[0-9a-f]{32}$`)
)

func errStorage(detail string) error { return fmt.Errorf("%w: %s", ErrStorage, detail) }

// Owner binds a run to the verified client and workspace that started it. A run is
// visible only to the same client and workspace; anyone else sees ErrNotFound, the
// same as for a run that does not exist.
type Owner struct {
	ClientID      string `json:"client_id"`
	Workspace     string `json:"workspace"`
	GrantID       string `json:"grant_id"`
	GrantRevision int64  `json:"grant_revision"`
}

func (o Owner) validate() error {
	if !clientIDRE.MatchString(o.ClientID) || !workspaceRE.MatchString(o.Workspace) || !grantIDRE.MatchString(o.GrantID) || o.GrantRevision < 1 {
		return fmt.Errorf("%w: owner", ErrInvalid)
	}
	return nil
}

func (o Owner) sameScope(other Owner) bool {
	return o.ClientID == other.ClientID && o.Workspace == other.Workspace
}

// Budget caps what one run may consume. Zero fields take defaults; nothing may
// exceed the ceiling.
type Budget struct {
	MaxTurns       int           `json:"max_turns"`
	MaxActive      time.Duration `json:"max_active"`
	MaxToolCalls   int           `json:"max_tool_calls"`
	MaxOutputBytes int           `json:"max_output_bytes"`
	MaxSpendMicros int64         `json:"max_spend_micros"`
	MaxDepth       int           `json:"max_depth"`
}

var (
	DefaultBudget = Budget{MaxTurns: 16, MaxActive: 10 * time.Minute, MaxToolCalls: 32, MaxOutputBytes: 256 << 10, MaxSpendMicros: 2_000_000, MaxDepth: 1}
	CeilingBudget = Budget{MaxTurns: 64, MaxActive: time.Hour, MaxToolCalls: 256, MaxOutputBytes: 4 << 20, MaxSpendMicros: 100_000_000, MaxDepth: 3}
)

// Normalize fills zero fields from the defaults and rejects values above the ceiling.
func (b Budget) Normalize() (Budget, error) {
	if b.MaxTurns == 0 {
		b.MaxTurns = DefaultBudget.MaxTurns
	}
	if b.MaxActive == 0 {
		b.MaxActive = DefaultBudget.MaxActive
	}
	if b.MaxToolCalls == 0 {
		b.MaxToolCalls = DefaultBudget.MaxToolCalls
	}
	if b.MaxOutputBytes == 0 {
		b.MaxOutputBytes = DefaultBudget.MaxOutputBytes
	}
	if b.MaxSpendMicros == 0 {
		b.MaxSpendMicros = DefaultBudget.MaxSpendMicros
	}
	if b.MaxDepth == 0 {
		b.MaxDepth = DefaultBudget.MaxDepth
	}
	if b.MaxTurns < 1 || b.MaxTurns > CeilingBudget.MaxTurns ||
		b.MaxActive < time.Millisecond || b.MaxActive > CeilingBudget.MaxActive ||
		b.MaxToolCalls < 0 || b.MaxToolCalls > CeilingBudget.MaxToolCalls ||
		b.MaxOutputBytes < 1 || b.MaxOutputBytes > CeilingBudget.MaxOutputBytes ||
		b.MaxSpendMicros < 0 || b.MaxSpendMicros > CeilingBudget.MaxSpendMicros ||
		b.MaxDepth < 0 || b.MaxDepth > CeilingBudget.MaxDepth {
		return Budget{}, fmt.Errorf("%w: budget is outside the allowed range", ErrBudget)
	}
	return b, nil
}

// Usage is what a run has consumed so far.
type Usage struct {
	Turns        int   `json:"turns"`
	ToolCalls    int   `json:"tool_calls"`
	OutputBytes  int   `json:"output_bytes"`
	SpendMicros  int64 `json:"spend_micros"`
	ActiveMillis int64 `json:"active_millis"`
	// Token counts are what providers reported for this run, summed over every attempt.
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
}

// Event is a content-free record of one change. Codes are machine words, never
// provider text, so event pages are safe to return to clients.
type Event struct {
	Seq   uint64    `json:"seq"`
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	State State     `json:"state"`
	Turn  int       `json:"turn"`
	Code  string    `json:"code"`
}

// Chunk is bounded transient run context. Raw provider output is truncated and
// retained only for the run's retention window.
type Chunk struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Turn int    `json:"turn"`
	Text string `json:"text"`
}

// Artifact is a bounded result of a run. Status reports metadata only.
type Artifact struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Turn   int    `json:"turn"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text,omitempty"`
}

// ArtifactMeta is the status view of an artifact.
type ArtifactMeta struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Turn   int    `json:"turn"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Attention explains why a run stopped for a human. A clarification may be answered
// through Continue; an approval can only be granted by the trusted local channel.
type Attention struct {
	Kind         string `json:"kind"`
	Prompt       string `json:"prompt,omitempty"`
	ActionDigest string `json:"action_digest,omitempty"`
	// ApprovalID names the approval a person must decide through the trusted local
	// channel. It is an opaque identifier: knowing it grants nothing.
	ApprovalID string `json:"approval_id,omitempty"`
	Turn       int    `json:"turn"`
}

// PendingAction is an approved call waiting to be replayed the next time the run runs. The
// run holds only identifiers; the arguments stay in the approval record until the approval
// is consumed, so an approved action cannot be altered by editing the run.
type PendingAction struct {
	ApprovalID string `json:"approval_id"`
	Digest     string `json:"digest"`
}

const (
	AttentionClarification = "clarification"
	AttentionApproval      = "approval"
)

type idemRecord struct {
	Operation  string    `json:"operation"`
	RequestSHA string    `json:"request_sha256"`
	At         time.Time `json:"at"`
}

type checkpoint struct {
	Turn  int       `json:"turn"`
	Epoch uint64    `json:"epoch"`
	At    time.Time `json:"at"`
}

// Run is the persisted record. Generation increments on every mutation and is the
// expected-generation value clients must echo; Epoch increments each time a worker
// (re)takes the run and scopes provider sessions.
type Run struct {
	SchemaVersion int                   `json:"schema_version"`
	ID            string                `json:"id"`
	Owner         Owner                 `json:"owner"`
	Parent        string                `json:"parent,omitempty"`
	Depth         int                   `json:"depth"`
	State         State                 `json:"state"`
	Generation    uint64                `json:"generation"`
	Epoch         uint64                `json:"epoch"`
	Code          string                `json:"code,omitempty"`
	Budget        Budget                `json:"budget"`
	Usage         Usage                 `json:"usage"`
	Checkpoint    checkpoint            `json:"checkpoint"`
	Attention     *Attention            `json:"attention,omitempty"`
	Pending       *PendingAction        `json:"pending,omitempty"`
	Chunks        []Chunk               `json:"chunks"`
	Artifacts     []Artifact            `json:"artifacts"`
	Events        []Event               `json:"events"`
	NextSeq       uint64                `json:"next_seq"`
	Idem          map[string]idemRecord `json:"idempotency,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	StartedAt     time.Time             `json:"started_at,omitempty"`
	ExpiresAt     time.Time             `json:"expires_at"`
}

// Status is the content-minimized view returned to clients.
type Status struct {
	ID           string         `json:"id"`
	State        State          `json:"state"`
	Generation   uint64         `json:"generation"`
	Turn         int            `json:"turn"`
	Code         string         `json:"code,omitempty"`
	Depth        int            `json:"depth"`
	Usage        Usage          `json:"usage"`
	Budget       Budget         `json:"budget"`
	Attention    *Attention     `json:"attention,omitempty"`
	Artifacts    []ArtifactMeta `json:"artifacts"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	ExpiresAt    time.Time      `json:"expires_at"`
	Deduplicated bool           `json:"deduplicated,omitempty"`
}

func (r Run) status() Status {
	status := Status{ID: r.ID, State: r.State, Generation: r.Generation, Turn: r.Checkpoint.Turn, Code: r.Code, Depth: r.Depth,
		Usage: r.Usage, Budget: r.Budget, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ExpiresAt: r.ExpiresAt,
		Artifacts: make([]ArtifactMeta, 0, len(r.Artifacts))}
	if r.Attention != nil {
		attention := *r.Attention
		status.Attention = &attention
	}
	for _, a := range r.Artifacts {
		status.Artifacts = append(status.Artifacts, ArtifactMeta{ID: a.ID, Kind: a.Kind, Turn: a.Turn, Bytes: a.Bytes, SHA256: a.SHA256})
	}
	return status
}

// validate rejects an unusable persisted record so a damaged checkpoint is
// quarantined instead of resumed.
func (r Run) validate() error {
	if r.SchemaVersion != schemaVersion || !runIDRE.MatchString(r.ID) || !r.State.valid() || r.Generation < 1 ||
		r.Owner.validate() != nil || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.ExpiresAt.IsZero() {
		return errStorage("invalid run record")
	}
	if _, err := r.Budget.Normalize(); err != nil || r.Budget != mustNormalize(r.Budget) {
		return errStorage("invalid run budget")
	}
	if r.Depth < 0 || r.Depth > CeilingBudget.MaxDepth || r.Usage.Turns < 0 || r.Usage.ToolCalls < 0 || r.Usage.OutputBytes < 0 ||
		r.Usage.SpendMicros < 0 || r.Usage.ActiveMillis < 0 || r.Checkpoint.Turn < 0 ||
		r.Usage.InputTokens < 0 || r.Usage.OutputTokens < 0 || r.Usage.CachedInputTokens < 0 || r.Usage.CachedInputTokens > r.Usage.InputTokens {
		return errStorage("invalid run usage")
	}
	if len(r.Chunks) > MaxChunks+1 || len(r.Artifacts) > MaxArtifacts || len(r.Events) > MaxEvents || len(r.Idem) > MaxIdempotency {
		return errStorage("run record exceeds bounds")
	}
	var last uint64
	for _, e := range r.Events {
		if e.Seq <= last || !e.State.valid() || (e.Code != "" && !codeRE.MatchString(e.Code)) {
			return errStorage("invalid run event")
		}
		last = e.Seq
	}
	if r.NextSeq <= last {
		return errStorage("invalid event sequence")
	}
	for _, c := range r.Chunks {
		if len(c.Text) > MaxChunkBytes {
			return errStorage("oversized chunk")
		}
	}
	for _, a := range r.Artifacts {
		if len(a.Text) > MaxArtifactBytes {
			return errStorage("oversized artifact")
		}
	}
	if r.Attention != nil && r.Attention.Kind != AttentionClarification && r.Attention.Kind != AttentionApproval {
		return errStorage("invalid attention")
	}
	if (r.State == StateNeedsAttention) != (r.Attention != nil) {
		return errStorage("attention does not match state")
	}
	if r.Attention != nil && r.Attention.ApprovalID != "" && !approvalIDRE.MatchString(r.Attention.ApprovalID) {
		return errStorage("invalid approval identifier")
	}
	if r.Pending != nil && ((r.State != StateQueued && r.State != StateRunning) || !approvalIDRE.MatchString(r.Pending.ApprovalID) ||
		r.Pending.Digest == "" || len(r.Pending.Digest) > 128) {
		return errStorage("invalid pending action")
	}
	return nil
}

func mustNormalize(b Budget) Budget {
	normalized, _ := b.Normalize()
	return normalized
}

// sanitize makes untrusted text safe to keep: valid UTF-8, no control characters
// other than newline and tab, and at most max bytes cut on a rune boundary.
func sanitize(value string, max int) string {
	if !utf8.ValidString(value) {
		value = string([]rune(value))
	}
	out := make([]rune, 0, len(value))
	size := 0
	for _, r := range value {
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			continue
		}
		width := utf8.RuneLen(r)
		if size+width > max {
			break
		}
		size += width
		out = append(out, r)
	}
	return string(out)
}
