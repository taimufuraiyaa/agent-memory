package harnessrun

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
)

// Decision is a policy verdict on one prepared tool action.
type Decision int

const (
	// DecisionDeny is the zero value so an unset policy fails closed.
	DecisionDeny Decision = iota
	DecisionAllow
	// DecisionAsk parks the run until a trusted local approval; MCP continuation
	// can never supply it.
	DecisionAsk
	// DecisionAskStrict parks the run like DecisionAsk, with extra friction: a longer code,
	// the reasons shown, and never batched. It is for changes to files that steer how the
	// project is built or run, deletions, and anything unusually large.
	DecisionAskStrict
)

// ToolPolicy decides whether a prepared action may run. It is deterministic local
// policy: a Jev recommendation can inform it but never replaces it.
type ToolPolicy interface {
	Decide(ctx context.Context, owner Owner, action harness.PreparedAction) Decision
}

// StrictReasoner is implemented by a policy that can say, as fixed codes, why an action
// asks with extra friction. The reasons are shown to the person deciding.
type StrictReasoner interface {
	Reasons(action harness.PreparedAction) []string
}

// ToolSelector advises which of the offered tools to keep when the model can be told about
// only keep of them. offered is in a fixed order and never includes the clarification
// request, which is always kept.
type ToolSelector interface {
	Select(ctx context.Context, runID string, offered []string, keep int) ([]string, error)
}

type denyAll struct{}

func (denyAll) Decide(context.Context, Owner, harness.PreparedAction) Decision { return DecisionDeny }

// StepSink receives concise, content-free progress for the durable solution
// episode. It must never be handed prompts, goals or provider output.
type StepSink interface {
	RecordStep(ctx context.Context, workspace, runID, kind, status, summary string) error
}

// Binding names the provider and capability used for one role.
type Binding struct {
	Provider   harness.ProviderID
	Capability harness.CapabilityID
}

type Config struct {
	DataDir  string
	Registry *harness.Registry
	// Model is required. Tool is optional; without it every tool request is
	// rejected as not offered.
	Model Binding
	Tool  *Binding

	Policy ToolPolicy
	Steps  StepSink
	// Context, when set, assembles the prompt and evidence for each model call. Without
	// it the loop refers to the goal and its most recent chunks and sends no prompt.
	Context ContextSource
	// Router, when set, plans an eligible chain of model providers for each call from the
	// data class, size and remaining spend. Without it every call goes to Model.
	Router ModelRouter
	// MaxOfferedTools, when positive, is how many tools the model is told about in one
	// call, the clarification request included. More tools than that are narrowed to the
	// limit before the call. Zero offers everything that is available.
	MaxOfferedTools int
	// ToolSelector advises which tools to keep when there are more than MaxOfferedTools.
	// It is advice only: the choice is validated and, if the selector is missing, slow,
	// wrong or fails, the first tools in the fixed order are kept.
	ToolSelector ToolSelector
	// StillAuthorized is consulted before every turn; false cancels the run, so a
	// revoked or expired grant stops its running work.
	StillAuthorized func(Owner) bool
	// Redact removes secrets from untrusted text before it is kept.
	Redact func(string) string
	// Approvals is the trusted local approval store. With it, an action that policy says
	// must ask is recorded there for a person to decide at a terminal, and the manager
	// applies the recorded decisions. Without it such an action parks the run with no way
	// to approve, which is the safe default.
	Approvals *harnessapproval.Store
	// ApprovalPoll is how often recorded decisions are applied; the default is one second.
	ApprovalPoll time.Duration

	Now    func() time.Time
	Random io.Reader

	MaxActive   int
	QueueSize   int
	CallTimeout time.Duration
	// ToolTimeout bounds one tool call. A command can legitimately run for minutes, so it
	// is separate from the model call timeout; zero uses CallTimeout.
	ToolTimeout time.Duration
	// CloseTimeout bounds how long a run waits for provider sessions to close before
	// it publishes a terminal state, so a provider that hangs in Close cannot hold a
	// finished run hostage.
	CloseTimeout    time.Duration
	RetainTerminal  time.Duration
	MaxAge          time.Duration
	MaxOutputTokens int
	ReplyBytes      int
}

// Snapshot is the read-only view of a run offered to a ContextSource.
type Snapshot struct {
	RunID  string
	Owner  Owner
	Turn   int
	Chunks []Chunk
}

// PromptContext is what a ContextSource assembles for one model call.
type PromptContext struct {
	Refs []harness.EvidenceRef
	// Prompt is the assembled, redacted text to send, within harness.MaxPromptBytes.
	Prompt string
	// PrefixID identifies the stable leading part of Prompt across turns; empty makes
	// no prompt-cache claim.
	PrefixID string
	// Class is the most sensitive data class in Prompt (0 public through 3 restricted),
	// which decides which providers may be sent it.
	Class       int
	InputTokens int
}

// ContextSource assembles the prompt and evidence for one model call. Returning an
// error or an unusable result makes the loop fall back to its own references for that
// turn and record a content-free event, so a faulty assembler never stops a run.
type ContextSource interface {
	Context(ctx context.Context, snapshot Snapshot) (PromptContext, error)
}

type StartRequest struct {
	IdempotencyKey string
	Goal           string
	Budget         Budget
}

// Mutation carries the two values every run mutation requires: a client-chosen
// idempotency key so retries are safe, and the generation the client last saw so a
// stale view cannot overwrite newer state.
type Mutation struct {
	IdempotencyKey     string
	ExpectedGeneration uint64
}

type EventPage struct {
	Events []Event `json:"events"`
	Next   string  `json:"next"`
	// Gap reports that older events were dropped from the bounded ring.
	Gap bool `json:"gap,omitempty"`
}

type RecoverReport struct{ Requeued, Cancelled, Quarantined int }

type Manager struct {
	cfg   Config
	store *FileStore
	now   func() time.Time
	rnd   io.Reader

	root  context.Context
	stop  context.CancelFunc
	wg    sync.WaitGroup
	queue chan string

	mu      sync.Mutex
	closed  bool
	pending int
	locks   map[string]*sync.Mutex
	active  map[string]*activeRun
}

// activeRun is what a worker registers while it owns a run: how to cancel its provider
// calls and how to release its provider sessions.
type activeRun struct {
	cancel  context.CancelFunc
	release func()
}

func NewManager(cfg Config) (*Manager, error) {
	if cfg.Registry == nil || cfg.Model.Provider == "" || cfg.Model.Capability == "" {
		return nil, fmt.Errorf("%w: registry and model binding are required", ErrInvalid)
	}
	if err := bindingDeclared(cfg.Registry, cfg.Model, harness.KindModel); err != nil {
		return nil, err
	}
	if cfg.Tool != nil {
		if err := bindingDeclared(cfg.Registry, *cfg.Tool, harness.KindTool); err != nil {
			return nil, err
		}
	}
	if cfg.Policy == nil {
		cfg.Policy = denyAll{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	if cfg.Redact == nil {
		cfg.Redact = func(s string) string { return s }
	}
	defaults := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	defaults(&cfg.MaxActive, 2)
	defaults(&cfg.QueueSize, 32)
	defaults(&cfg.MaxOutputTokens, 1024)
	defaults(&cfg.ReplyBytes, 64<<10)
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 30 * time.Second
	}
	if cfg.ToolTimeout <= 0 {
		cfg.ToolTimeout = cfg.CallTimeout
	}
	if cfg.CloseTimeout <= 0 {
		cfg.CloseTimeout = 2 * time.Second
	}
	if cfg.RetainTerminal <= 0 {
		cfg.RetainTerminal = 24 * time.Hour
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 7 * 24 * time.Hour
	}
	store, err := OpenStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	root, stop := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, store: store, now: cfg.Now, rnd: cfg.Random, root: root, stop: stop,
		queue: make(chan string, cfg.QueueSize), locks: make(map[string]*sync.Mutex), active: make(map[string]*activeRun)}
	for i := 0; i < cfg.MaxActive; i++ {
		m.wg.Add(1)
		go m.dispatch()
	}
	if cfg.Approvals != nil {
		if cfg.ApprovalPoll <= 0 {
			cfg.ApprovalPoll = time.Second
		}
		m.cfg.ApprovalPoll = cfg.ApprovalPoll
		m.wg.Add(1)
		go m.watchApprovals()
	}
	return m, nil
}

func bindingDeclared(registry *harness.Registry, b Binding, kind harness.ProviderKind) error {
	for _, manifest := range registry.Manifests() {
		if manifest.ID != b.Provider {
			continue
		}
		if manifest.Kind != kind {
			return fmt.Errorf("%w: provider %s is not a %s provider", ErrInvalid, b.Provider, kind)
		}
		for _, capability := range manifest.Capabilities {
			if capability == b.Capability {
				return nil
			}
		}
		return fmt.Errorf("%w: provider %s does not declare %s", ErrInvalid, b.Provider, b.Capability)
	}
	return fmt.Errorf("%w: provider %s is not registered", ErrInvalid, b.Provider)
}

// Close stops accepting work and cancels every active run's providers. Runs that were
// mid-flight keep their last checkpoint and resume on Recover; they are not marked
// cancelled, because the client did not ask for that.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	m.stop()
	m.wg.Wait()
	return nil
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *Manager) dispatch() {
	defer m.wg.Done()
	for {
		select {
		case <-m.root.Done():
			return
		case id := <-m.queue:
			m.mu.Lock()
			if m.pending > 0 {
				m.pending--
			}
			m.mu.Unlock()
			m.runOne(id)
		}
	}
}

// reserve claims one queue slot before any state is committed, so a full queue is
// reported without leaving a half-created run behind.
func (m *Manager) reserve() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.pending >= m.cfg.QueueSize {
		return false
	}
	m.pending++
	return true
}

func (m *Manager) release() {
	m.mu.Lock()
	if m.pending > 0 {
		m.pending--
	}
	m.mu.Unlock()
}

func (m *Manager) lockFor(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock := m.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		m.locks[id] = lock
	}
	return lock
}

// errAbort makes a mutation callback stop without saving or reporting a failure.
var errAbort = errors.New("abort mutation")

// mutate runs one atomic read-modify-write on a run.
func (m *Manager) mutate(id string, change func(*Run) (bool, error)) (Run, error) {
	lock := m.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	run, err := m.store.Load(id)
	if err != nil {
		return Run{}, err
	}
	changed, err := change(&run)
	if err != nil {
		return run, err
	}
	if changed {
		if err := m.store.Save(run); err != nil {
			return run, err
		}
	}
	return run, nil
}

func (m *Manager) load(owner Owner, id string) (Run, error) {
	run, err := m.store.Load(id)
	if err != nil {
		return Run{}, err
	}
	if !run.Owner.sameScope(owner) {
		return Run{}, ErrNotFound
	}
	return run, nil
}

func (m *Manager) newRunID() (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(m.rnd, buf); err != nil {
		return "", errStorage("cannot generate run ID")
	}
	return "run_" + hex.EncodeToString(buf), nil
}

func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func requestDigest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// maxRedactInput bounds what a redactor is asked to scan.
const maxRedactInput = 256 << 10

// clean makes untrusted text safe to keep: redact first, on text that has not been
// cut mid-secret, then bound it and drop surrounding blanks.
func (m *Manager) clean(text string, max int) string {
	return strings.TrimSpace(sanitize(m.cfg.Redact(sanitize(text, maxRedactInput)), max))
}

// Start creates a queued run and returns at once; a worker picks it up. Retrying
// with the same idempotency key and request returns the original run, and the same
// key with a different request is a conflict.
func (m *Manager) Start(ctx context.Context, owner Owner, request StartRequest) (Status, error) {
	return m.create(ctx, owner, request, nil)
}

// StartChild creates a run beneath a running parent. Depth is capped by the
// parent's budget and the child inherits only what the parent has left.
func (m *Manager) StartChild(ctx context.Context, owner Owner, parentID string, request StartRequest) (Status, error) {
	parent, err := m.load(owner, parentID)
	if err != nil {
		return Status{}, err
	}
	if parent.State != StateRunning {
		return Status{}, fmt.Errorf("%w: parent is %s", ErrInvalidState, parent.State)
	}
	return m.create(ctx, owner, request, &parent)
}

func (m *Manager) create(ctx context.Context, owner Owner, request StartRequest, parent *Run) (Status, error) {
	if m.isClosed() {
		return Status{}, ErrClosed
	}
	if err := owner.validate(); err != nil {
		return Status{}, err
	}
	if !idempotencyRE.MatchString(request.IdempotencyKey) {
		return Status{}, fmt.Errorf("%w: idempotency key", ErrInvalid)
	}
	goal := m.clean(request.Goal, MaxGoalBytes)
	if len(goal) == 0 {
		return Status{}, fmt.Errorf("%w: goal is required", ErrInvalid)
	}
	budget, err := request.Budget.Normalize()
	if err != nil {
		return Status{}, err
	}
	depth := 0
	parentID := ""
	if parent != nil {
		depth, parentID = parent.Depth+1, parent.ID
		if depth > parent.Budget.MaxDepth {
			return Status{}, fmt.Errorf("%w: recursion depth", ErrBudget)
		}
		if budget, err = inherit(budget, *parent); err != nil {
			return Status{}, err
		}
	}
	runID, err := m.newRunID()
	if err != nil {
		return Status{}, err
	}
	if err := (harness.Scope{Workspace: owner.Workspace, Run: runID, Generation: 1}).Validate(); err != nil {
		return Status{}, fmt.Errorf("%w: workspace name is not supported by the harness", ErrInvalid)
	}
	name := idemName(owner, request.IdempotencyKey)
	digest := requestDigest("start", goal, fmt.Sprint(budget), parentID)
	now := m.now().UTC()

	claimed := false
	for attempt := 0; attempt < 4 && !claimed; attempt++ {
		existing, created, err := m.store.PutIdem(name, idemEntry{RunID: runID, RequestSHA: digest, CreatedAt: now})
		if errors.Is(err, errIdemGone) {
			continue // the record vanished between the claim attempt and the read; claim again
		}
		if err != nil {
			return Status{}, err
		}
		if created {
			claimed = true
			break
		}
		if existing.RequestSHA != digest {
			return Status{}, ErrIdempotencyConflict
		}
		run, err := m.awaitRun(owner, name, existing)
		switch {
		case err == nil:
			status := run.status()
			status.Deduplicated = true
			return status, nil
		case errors.Is(err, errIdemGone), errors.Is(err, errIdemStale):
			continue
		default:
			return Status{}, err
		}
	}
	if !claimed {
		return Status{}, ErrBusy
	}
	if !m.reserve() {
		m.store.DeleteIdem(name)
		return Status{}, ErrBusy
	}
	run := Run{SchemaVersion: schemaVersion, ID: runID, Owner: owner, Parent: parentID, Depth: depth, State: StateQueued, Generation: 1,
		Budget: budget, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(m.cfg.MaxAge), NextSeq: 1}
	run.Chunks = []Chunk{{ID: "goal", Kind: "goal", Text: goal}}
	run.addEvent("state", "queued", now)
	if err := m.store.Create(run); err != nil {
		m.release()
		m.store.DeleteIdem(name)
		return Status{}, err
	}
	m.queue <- runID
	m.step(ctx, run, "action", "completed", "run queued")
	return run.status(), nil
}

var errIdemStale = errors.New("idempotency record has no run")

// awaitRun returns the run an existing idempotency record points to. The winner
// creates the record first and the run an instant later, so a missing run is waited
// for while the record is fresh; only an old record with no run is stale and removed.
func (m *Manager) awaitRun(owner Owner, name string, record idemEntry) (Run, error) {
	const grace = 10 * time.Second
	for waited := 0; waited < 200; waited++ {
		run, err := m.load(owner, record.RunID)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return run, err
		}
		current, err := m.store.GetIdem(name)
		if err != nil {
			return Run{}, err // errIdemGone when the winner gave up
		}
		if current.RunID != record.RunID {
			return Run{}, errIdemGone
		}
		if m.now().Sub(record.CreatedAt) > grace {
			m.store.DeleteIdem(name)
			return Run{}, errIdemStale
		}
		time.Sleep(10 * time.Millisecond)
	}
	return Run{}, ErrBusy
}

// inherit narrows a child's budget to what its parent has not yet used.
func inherit(requested Budget, parent Run) (Budget, error) {
	remaining := Budget{
		MaxTurns:       parent.Budget.MaxTurns - parent.Usage.Turns,
		MaxActive:      parent.Budget.MaxActive - time.Duration(parent.Usage.ActiveMillis)*time.Millisecond,
		MaxToolCalls:   parent.Budget.MaxToolCalls - parent.Usage.ToolCalls,
		MaxOutputBytes: parent.Budget.MaxOutputBytes - parent.Usage.OutputBytes,
		MaxSpendMicros: parent.Budget.MaxSpendMicros - parent.Usage.SpendMicros,
		MaxDepth:       parent.Budget.MaxDepth,
	}
	if remaining.MaxTurns < 1 || remaining.MaxActive < time.Millisecond || remaining.MaxToolCalls < 0 || remaining.MaxOutputBytes < 1 || remaining.MaxSpendMicros < 0 {
		return Budget{}, fmt.Errorf("%w: parent has no budget left to share", ErrBudget)
	}
	merged := requested
	merged.MaxTurns = minInt(requested.MaxTurns, remaining.MaxTurns)
	merged.MaxActive = minDuration(requested.MaxActive, remaining.MaxActive)
	merged.MaxToolCalls = minInt(requested.MaxToolCalls, remaining.MaxToolCalls)
	merged.MaxOutputBytes = minInt(requested.MaxOutputBytes, remaining.MaxOutputBytes)
	if requested.MaxSpendMicros > remaining.MaxSpendMicros {
		merged.MaxSpendMicros = remaining.MaxSpendMicros
	}
	merged.MaxDepth = remaining.MaxDepth
	// A shared spend or tool allowance of zero would normalize back to the default,
	// so refuse instead of silently widening it.
	if merged.MaxSpendMicros == 0 || merged.MaxToolCalls == 0 {
		return Budget{}, fmt.Errorf("%w: parent has no spend or tool budget left to share", ErrBudget)
	}
	return merged, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Status returns the content-minimized view of a run the owner may see.
func (m *Manager) Status(_ context.Context, owner Owner, id string) (Status, error) {
	run, err := m.load(owner, id)
	if err != nil {
		return Status{}, err
	}
	return run.status(), nil
}

// Events pages the content-free event log using an opaque cursor bound to this run
// and client. An empty cursor starts at the oldest retained event.
func (m *Manager) Events(_ context.Context, owner Owner, id, cursor string, limit int) (EventPage, error) {
	run, err := m.load(owner, id)
	if err != nil {
		return EventPage{}, err
	}
	after, err := m.store.decodeCursor(run.ID, owner.ClientID, cursor)
	if err != nil {
		return EventPage{}, err
	}
	if limit < 1 || limit > MaxEventPageLimit {
		limit = MaxEventPageLimit
	}
	page := EventPage{Events: []Event{}}
	if len(run.Events) > 0 && after+1 < run.Events[0].Seq && after != 0 {
		page.Gap = true
	}
	last := after
	for _, event := range run.Events {
		if event.Seq <= after {
			continue
		}
		if len(page.Events) == limit {
			break
		}
		page.Events = append(page.Events, event)
		last = event.Seq
	}
	page.Next = m.store.encodeCursor(run.ID, owner.ClientID, last)
	return page, nil
}

// Cancel asks a run to stop. A queued or waiting run is cancelled at once; a running
// run moves to cancelling and its providers are cancelled, then the worker finishes
// the transition.
func (m *Manager) Cancel(ctx context.Context, owner Owner, id string, mutation Mutation) (Status, error) {
	if err := validateMutation(mutation); err != nil {
		return Status{}, err
	}
	digest := requestDigest("cancel", id)
	signal := false
	var announce *Run
	endedApproval := ""
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if !r.Owner.sameScope(owner) {
			return false, ErrNotFound
		}
		if record, replay := r.Idem[mutation.IdempotencyKey]; replay {
			if record.Operation != "cancel" || record.RequestSHA != digest {
				return false, ErrIdempotencyConflict
			}
			return false, nil
		}
		if mutation.ExpectedGeneration != r.Generation {
			return false, ErrStaleGeneration
		}
		now := m.now().UTC()
		switch r.State {
		case StateQueued, StateNeedsAttention:
			if r.Attention != nil && r.Attention.Kind == AttentionApproval {
				endedApproval = r.Attention.ApprovalID
			} else if r.Pending != nil {
				endedApproval = r.Pending.ApprovalID
			}
			if err := r.move(StateCancelled, "cancelled_by_client", now); err != nil {
				return false, err
			}
			copied := *r
			announce = &copied
		case StateRunning:
			if err := r.move(StateCancelling, "cancel_requested", now); err != nil {
				return false, err
			}
			signal = true
		case StateCancelling:
			signal = true
		default:
			return false, fmt.Errorf("%w: run is %s", ErrInvalidState, r.State)
		}
		r.remember(mutation.IdempotencyKey, idemRecord{Operation: "cancel", RequestSHA: digest, At: now})
		return true, nil
	})
	if err != nil {
		return Status{}, err
	}
	if signal {
		m.signal(id)
	}
	if endedApproval != "" && m.cfg.Approvals != nil {
		// The run no longer waits for this approval, so it can no longer be approved.
		_, _ = m.cfg.Approvals.Resolve(ctx, endedApproval, harnessapproval.StateSuperseded, "run_cancelled")
	}
	if announce != nil {
		m.step(ctx, *announce, "result", "completed", "run cancelled")
	}
	return run.status(), nil
}

// Continue answers a clarification and requeues the run. It can never satisfy an
// approval: those wait for the trusted local channel.
func (m *Manager) Continue(ctx context.Context, owner Owner, id string, mutation Mutation, input string) (Status, error) {
	if err := validateMutation(mutation); err != nil {
		return Status{}, err
	}
	answer := m.clean(input, MaxInputBytes)
	if len(answer) == 0 {
		return Status{}, fmt.Errorf("%w: input is required", ErrInvalid)
	}
	if !m.reserve() {
		return Status{}, ErrBusy
	}
	digest := requestDigest("continue", id, answer)
	queued := false
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if !r.Owner.sameScope(owner) {
			return false, ErrNotFound
		}
		if record, replay := r.Idem[mutation.IdempotencyKey]; replay {
			if record.Operation != "continue" || record.RequestSHA != digest {
				return false, ErrIdempotencyConflict
			}
			return false, nil
		}
		if mutation.ExpectedGeneration != r.Generation {
			return false, ErrStaleGeneration
		}
		if r.State != StateNeedsAttention || r.Attention == nil {
			return false, fmt.Errorf("%w: run is %s", ErrInvalidState, r.State)
		}
		if r.Attention.Kind == AttentionApproval {
			return false, ErrApprovalRequired
		}
		now := m.now().UTC()
		r.addChunk("input", answer, r.Checkpoint.Turn)
		if err := r.move(StateQueued, "resumed", now); err != nil {
			return false, err
		}
		r.remember(mutation.IdempotencyKey, idemRecord{Operation: "continue", RequestSHA: digest, At: now})
		queued = true
		return true, nil
	})
	if err != nil || !queued {
		m.release()
		if err != nil {
			return Status{}, err
		}
		return run.status(), nil
	}
	m.queue <- id
	m.step(ctx, run, "action", "completed", "run resumed")
	return run.status(), nil
}

func validateMutation(mutation Mutation) error {
	if !idempotencyRE.MatchString(mutation.IdempotencyKey) {
		return fmt.Errorf("%w: idempotency key", ErrInvalid)
	}
	if mutation.ExpectedGeneration < 1 {
		return fmt.Errorf("%w: expected generation", ErrInvalid)
	}
	return nil
}

func (m *Manager) setActive(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	m.active[id] = &activeRun{cancel: cancel}
	m.mu.Unlock()
}

// setRelease records how to close the sessions the worker opened for this run.
func (m *Manager) setRelease(id string, release func()) {
	m.mu.Lock()
	if a := m.active[id]; a != nil {
		a.release = release
	}
	m.mu.Unlock()
}

func (m *Manager) clearActive(id string) {
	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()
}

// signal cancels a running run's context, which cancels every provider call it has
// in flight.
func (m *Manager) signal(id string) {
	m.mu.Lock()
	a := m.active[id]
	m.mu.Unlock()
	if a != nil && a.cancel != nil {
		a.cancel()
	}
}

// teardown closes the run's provider sessions before a terminal or parked state is
// published, so a client that sees the run stop can rely on no provider work for it
// remaining. It is idempotent and bounded by CloseTimeout.
func (m *Manager) teardown(id string) {
	m.mu.Lock()
	a := m.active[id]
	m.mu.Unlock()
	if a != nil && a.release != nil {
		a.release()
	}
}

func (m *Manager) isActive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.active[id]
	return ok
}

// Recover resumes work after a restart. A running run goes back to the queue and
// continues from its last checkpoint with its consumed budget intact, a run that was
// being cancelled finishes cancelling, and a damaged record is quarantined rather
// than trusted.
func (m *Manager) Recover(ctx context.Context) (RecoverReport, error) {
	ids, err := m.store.IDs()
	if err != nil {
		return RecoverReport{}, err
	}
	var report RecoverReport
	var resume []string
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if m.isActive(id) {
			continue
		}
		_, err := m.store.Load(id)
		if errors.Is(err, ErrStorage) {
			if m.store.Quarantine(id) == nil {
				report.Quarantined++
			}
			continue
		}
		if err != nil {
			continue
		}
		queue := false
		_, err = m.mutate(id, func(r *Run) (bool, error) {
			now := m.now().UTC()
			switch r.State {
			case StateRunning:
				if err := r.move(StateQueued, "recovered", now); err != nil {
					return false, err
				}
				report.Requeued++
				queue = true
				return true, nil
			case StateQueued:
				report.Requeued++
				queue = true
				return false, nil
			case StateCancelling:
				if err := r.move(StateCancelled, "cancelled_after_restart", now); err != nil {
					return false, err
				}
				report.Cancelled++
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			continue
		}
		if queue {
			resume = append(resume, id)
		}
	}
	// Recovered runs wait for a queue slot like any other, so none is dropped and a
	// concurrent Start can never block on a full channel.
	if len(resume) > 0 {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			for _, id := range resume {
				for !m.reserve() {
					select {
					case <-m.root.Done():
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
				select {
				case m.queue <- id:
				case <-m.root.Done():
					return
				}
			}
		}()
	}
	return report, nil
}

// Sweep deletes finished runs past their retention, abandoned runs past their
// maximum age, and idempotency records that outlived their run. Active runs are
// never touched.
func (m *Manager) Sweep() (int, error) {
	ids, err := m.store.IDs()
	if err != nil {
		return 0, err
	}
	now := m.now().UTC()
	removed := 0
	for _, id := range ids {
		if m.isActive(id) {
			continue
		}
		run, err := m.store.Load(id)
		if err != nil {
			continue
		}
		expired := now.After(run.ExpiresAt) || (run.State.Terminal() && now.Sub(run.UpdatedAt) > m.cfg.RetainTerminal)
		if !expired {
			continue
		}
		lock := m.lockFor(id)
		lock.Lock()
		err = m.store.Delete(id)
		lock.Unlock()
		if err == nil {
			removed++
			m.mu.Lock()
			delete(m.locks, id)
			m.mu.Unlock()
		}
	}
	m.store.SweepIdem(now.Add(-m.cfg.MaxAge), func(id string) bool { _, err := m.store.Load(id); return err == nil })
	return removed, nil
}

func (m *Manager) step(ctx context.Context, run Run, kind, status, summary string) {
	if m.cfg.Steps == nil {
		return
	}
	// Only fixed, content-free summaries are ever sent; failures never affect a run.
	_ = m.cfg.Steps.RecordStep(ctx, run.Owner.Workspace, run.ID, kind, status, summary)
}
