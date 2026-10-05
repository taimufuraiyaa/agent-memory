package harness

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultCallTimeout     = 30 * time.Second
	MaxCallTimeout         = 10 * time.Minute
	MaxCandidates          = 64
	MaxFieldBytes          = 256
	MaxEvidenceRefs        = 256
	MaxToolSchemas         = 64
	MaxOutputTokenLimit    = 1 << 16
	MaxOutstandingPrepared = 1024
)

var (
	ErrUnavailable = errors.New("harness provider unavailable")
	// ErrClosed wraps ErrStale: a disposed session can never serve a late result.
	ErrClosed = fmt.Errorf("%w: session closed", ErrStale)
)

type sessionConfig struct{ timeout time.Duration }

// SessionOption adjusts one provider session.
type SessionOption func(*sessionConfig)

// WithCallTimeout bounds every provider call made through the session.
func WithCallTimeout(d time.Duration) SessionOption { return func(c *sessionConfig) { c.timeout = d } }

type preparedKey struct {
	capability CapabilityID
	digest     string
}

// Session binds one provider to one workspace generation. It is the only path the
// run loop uses to reach a provider: it validates requests before any provider
// work, bounds every call by deadline and cancellation, rejects stale or oversized
// replies before orchestration sees them, and reports every provider problem as a
// typed outcome rather than an empty success.
type Session struct {
	manifest Manifest
	declared map[CapabilityID]struct{}
	provider Provider
	scope    Scope
	access   LiveAccess
	timeout  time.Duration
	release  func()

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	closed    bool
	prepared  map[preparedKey]PreparedAction
	closeOnce sync.Once
	closeErr  error
}

// OpenSession opens a provider, probes live access for the scope and returns a
// session. A registry keeps one open session per provider, workspace and run:
// opening a higher generation disposes the previous one, and a lower generation is
// stale. Runs are independent of each other.
func (r *Registry) OpenSession(ctx context.Context, id ProviderID, scope Scope, options ...SessionOption) (*Session, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	config := sessionConfig{timeout: DefaultCallTimeout}
	for _, option := range options {
		option(&config)
	}
	if config.timeout <= 0 || config.timeout > MaxCallTimeout {
		return nil, fmt.Errorf("%w: call timeout", ErrInvalid)
	}
	key := liveKey{provider: id, workspace: scope.Workspace, run: scope.Run}
	r.mu.RLock()
	current := r.live[key]
	r.mu.RUnlock()
	if current != nil && current.scope.Generation > scope.Generation {
		return nil, ErrStale
	}
	manifest, provider, err := r.Open(id)
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &Session{
		manifest: manifest,
		declared: make(map[CapabilityID]struct{}, len(manifest.Capabilities)),
		provider: provider,
		scope:    scope,
		timeout:  config.timeout,
		ctx:      sessionCtx,
		cancel:   cancel,
		prepared: make(map[preparedKey]PreparedAction),
	}
	for _, capability := range manifest.Capabilities {
		session.declared[capability] = struct{}{}
	}
	access, outcome := invoke(session, ctx, func(c context.Context) (LiveAccess, error) { return provider.Probe(c, scope) })
	if outcome != "" {
		session.dispose()
		return nil, fmt.Errorf("%w: probe %s", ErrUnavailable, outcome)
	}
	if err := access.Validate(manifest, scope); err != nil {
		session.dispose()
		return nil, err
	}
	session.access = copyAccess(access)

	r.mu.Lock()
	if r.live == nil {
		r.live = make(map[liveKey]*Session)
	}
	previous := r.live[key]
	if previous != nil && previous.scope.Generation > scope.Generation {
		r.mu.Unlock()
		session.dispose()
		return nil, ErrStale
	}
	r.live[key] = session
	session.release = func() {
		r.mu.Lock()
		if r.live[key] == session {
			delete(r.live, key)
		}
		r.mu.Unlock()
	}
	r.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return session, nil
}

func (s *Session) Scope() Scope { return s.scope }

func (s *Session) Manifest() Manifest {
	manifest := s.manifest
	manifest.Capabilities = append([]CapabilityID(nil), s.manifest.Capabilities...)
	return manifest
}

func (s *Session) Access() LiveAccess { return copyAccess(s.access) }

// State reports live access for a capability; an absent entry is unavailable.
func (s *Session) State(capability CapabilityID) AccessState {
	if state, ok := s.access.Capabilities[capability]; ok {
		return state
	}
	return AccessUnavailable
}

// Envelope mints the scope- and revision-bound envelope for one declared capability.
// maxBytes caps the reply payload.
func (s *Session) Envelope(capability CapabilityID, maxBytes int) (Envelope, error) {
	if _, ok := s.declared[capability]; !ok {
		return Envelope{}, fmt.Errorf("%w: undeclared capability", ErrInvalid)
	}
	if maxBytes < 1 || maxBytes > MaxPayloadBytes {
		return Envelope{}, fmt.Errorf("%w: operation bounds", ErrInvalid)
	}
	return Envelope{Version: ContractVersion, Provider: s.manifest.ID, Scope: s.scope,
		AccessRevision: s.access.Revision, Capability: capability, MaxBytes: maxBytes}, nil
}

// Close cancels in-flight calls and disposes the provider exactly once.
func (s *Session) Close() error {
	s.dispose()
	return s.closeErr
}

func (s *Session) dispose() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.prepared = nil
		s.mu.Unlock()
		s.cancel()
		if s.release != nil {
			s.release()
		}
		if err := s.provider.Close(); err != nil {
			s.closeErr = errors.New("harness provider close failed")
		}
	})
}

// gate rejects closed, stale and undeclared use with errors, and turns a
// non-available capability into a typed outcome without calling the provider.
func (s *Session) gate(e Envelope) (Outcome, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return "", ErrClosed
	}
	if err := e.validateShape(s.access); err != nil {
		return "", err
	}
	if _, ok := s.declared[e.Capability]; !ok {
		return "", fmt.Errorf("%w: undeclared capability", ErrInvalid)
	}
	switch s.State(e.Capability) {
	case AccessAvailable:
		return "", nil
	case AccessUnsupported:
		return OutcomeUnsupported, nil
	case AccessDenied:
		return OutcomeDenied, nil
	default:
		return OutcomeUnavailable, nil
	}
}

// Decide asks an advisory question. The answer never carries authority.
func (s *Session) Decide(ctx context.Context, question DecisionQuestion) (DecisionAnswer, error) {
	provider, ok := s.provider.(DecisionProvider)
	if !ok {
		return DecisionAnswer{}, fmt.Errorf("%w: not a decision provider", ErrInvalid)
	}
	if err := validateQuestion(question); err != nil {
		return DecisionAnswer{}, err
	}
	gated, err := s.gate(question.Envelope)
	if err != nil {
		return DecisionAnswer{}, err
	}
	if gated != "" {
		return DecisionAnswer{Envelope: question.Envelope, Outcome: gated}, nil
	}
	sent := question
	sent.Candidates = append([]string(nil), question.Candidates...)
	sent.Evidence = append([]EvidenceRef(nil), question.Evidence...)
	answer, outcome := invoke(s, ctx, func(c context.Context) (DecisionAnswer, error) { return provider.Decide(c, sent) })
	if outcome != "" {
		return DecisionAnswer{Envelope: question.Envelope, Outcome: outcome}, nil
	}
	if err := ValidateDecisionAnswer(question, answer); err != nil {
		return DecisionAnswer{}, err
	}
	if !carriesPayload(answer.Outcome) {
		answer.Selected, answer.Confidence = nil, 0
	}
	return answer, nil
}

// Generate requests bounded model output.
func (s *Session) Generate(ctx context.Context, request ModelRequest) (ModelAnswer, error) {
	provider, ok := s.provider.(ModelProvider)
	if !ok {
		return ModelAnswer{}, fmt.Errorf("%w: not a model provider", ErrInvalid)
	}
	if err := validateModelRequest(request); err != nil {
		return ModelAnswer{}, err
	}
	gated, err := s.gate(request.Envelope)
	if err != nil {
		return ModelAnswer{}, err
	}
	if gated != "" {
		return ModelAnswer{Envelope: request.Envelope, Outcome: gated}, nil
	}
	sent := request
	sent.InputRefs = append([]EvidenceRef(nil), request.InputRefs...)
	sent.ToolSchemaIDs = append([]string(nil), request.ToolSchemaIDs...)
	answer, outcome := invoke(s, ctx, func(c context.Context) (ModelAnswer, error) { return provider.Generate(c, sent) })
	if outcome != "" {
		return ModelAnswer{Envelope: request.Envelope, Outcome: outcome}, nil
	}
	if err := ValidateReply(request.Envelope, answer.Envelope, answer.Outcome, len(answer.Text)+len(answer.ToolID)+len(answer.ToolArguments)); err != nil {
		return ModelAnswer{}, err
	}
	if answer.CostMicros < 0 || answer.CostMicros > MaxCostMicros {
		return ModelAnswer{}, fmt.Errorf("%w: reported cost", ErrInvalid)
	}
	if !answer.Usage.validate() || (answer.Model != "" && !boundedText(answer.Model, false)) {
		return ModelAnswer{}, fmt.Errorf("%w: reported usage or model", ErrInvalid)
	}
	if len(answer.ToolArguments) > 0 && answer.ToolID == "" {
		return ModelAnswer{}, fmt.Errorf("%w: tool arguments without a tool", ErrInvalid)
	}
	if answer.ToolID != "" {
		offered := false
		for _, id := range request.ToolSchemaIDs {
			offered = offered || id == answer.ToolID
		}
		if !offered {
			return ModelAnswer{}, fmt.Errorf("%w: tool was not offered", ErrInvalid)
		}
	}
	if !carriesPayload(answer.Outcome) {
		answer.Text, answer.ToolID, answer.ToolArguments = "", "", nil
	}
	return answer, nil
}

// Prepare validates a native action and returns a digest-bound, single-use handle.
// Preparing never executes anything.
func (s *Session) Prepare(ctx context.Context, request ToolRequest) (PreparedAction, error) {
	provider, ok := s.provider.(ToolProvider)
	if !ok {
		return PreparedAction{}, fmt.Errorf("%w: not a tool provider", ErrInvalid)
	}
	if err := validateToolRequest(request); err != nil {
		return PreparedAction{}, err
	}
	gated, err := s.gate(request.Envelope)
	if err != nil {
		return PreparedAction{}, err
	}
	if gated != "" {
		return PreparedAction{Envelope: request.Envelope, Outcome: gated}, nil
	}
	sent := request
	sent.Arguments = append([]byte(nil), request.Arguments...)
	action, outcome := invoke(s, ctx, func(c context.Context) (PreparedAction, error) { return provider.Prepare(c, sent) })
	if outcome != "" {
		return PreparedAction{Envelope: request.Envelope, Outcome: outcome}, nil
	}
	if err := ValidateReply(request.Envelope, action.Envelope, action.Outcome, len(action.Summary)+len(action.Digest)); err != nil {
		return PreparedAction{}, err
	}
	if action.Outcome != OutcomeOK {
		// Only a fully successful preparation yields an invokable handle.
		action.Digest, action.Paths, action.Preview, action.Escalate = "", nil, "", nil
		if !carriesPayload(action.Outcome) {
			action.Summary = ""
		}
		if !reasonCodeOK(action.Reason) {
			action.Reason = ""
		}
		return action, nil
	}
	action.Reason = ""
	if !digestOK(action.Digest) {
		return PreparedAction{}, fmt.Errorf("%w: action digest", ErrInvalid)
	}
	if err := validateChange(action); err != nil {
		return PreparedAction{}, err
	}
	key := preparedKey{capability: action.Capability, digest: action.Digest}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return PreparedAction{}, ErrClosed
	}
	if _, exists := s.prepared[key]; exists || len(s.prepared) >= MaxOutstandingPrepared {
		return PreparedAction{}, fmt.Errorf("%w: prepared action", ErrInvalid)
	}
	s.prepared[key] = action
	return action, nil
}

// Invoke runs one action the session itself prepared. A handle is single use:
// replays, forged digests and actions from another scope fail closed. Approval
// and policy belong to the caller and must run between Prepare and Invoke.
func (s *Session) Invoke(ctx context.Context, action PreparedAction) (ToolAnswer, error) {
	provider, ok := s.provider.(ToolProvider)
	if !ok {
		return ToolAnswer{}, fmt.Errorf("%w: not a tool provider", ErrInvalid)
	}
	if action.Outcome != OutcomeOK || !digestOK(action.Digest) {
		return ToolAnswer{}, fmt.Errorf("%w: action is not prepared", ErrInvalid)
	}
	gated, err := s.gate(action.Envelope)
	if err != nil {
		return ToolAnswer{}, err
	}
	if gated != "" {
		return ToolAnswer{Envelope: action.Envelope, Outcome: gated}, nil
	}
	key := preparedKey{capability: action.Capability, digest: action.Digest}
	s.mu.Lock()
	stored, found := s.prepared[key]
	found = found && stored.Envelope == action.Envelope
	if found {
		// Consume before running so a replay fails even if execution errors.
		// A mismatched attempt leaves the genuine handle untouched.
		delete(s.prepared, key)
	}
	s.mu.Unlock()
	if !found {
		return ToolAnswer{}, fmt.Errorf("%w: action was not prepared or was already invoked", ErrStale)
	}
	answer, outcome := invoke(s, ctx, func(c context.Context) (ToolAnswer, error) { return provider.Invoke(c, stored) })
	if outcome != "" {
		return ToolAnswer{Envelope: stored.Envelope, Outcome: outcome}, nil
	}
	if err := ValidateReply(stored.Envelope, answer.Envelope, answer.Outcome, len(answer.Output)); err != nil {
		return ToolAnswer{}, err
	}
	if !carriesPayload(answer.Outcome) {
		answer.Output, answer.Audit = nil, nil
	}
	if len(answer.Audit) > MaxAuditCodes {
		return ToolAnswer{}, fmt.Errorf("%w: audit codes", ErrInvalid)
	}
	for _, code := range answer.Audit {
		if !reasonCodeOK(code) {
			return ToolAnswer{}, fmt.Errorf("%w: audit code", ErrInvalid)
		}
	}
	return answer, nil
}

// Discard releases a prepared action that policy or approval declined, so the
// handle cannot be invoked later and an identical action can be prepared again.
// It is idempotent and is not a provider call: it never waits on a provider.
func (s *Session) Discard(action PreparedAction) {
	s.mu.Lock()
	_, held := s.prepared[preparedKey{capability: action.Capability, digest: action.Digest}]
	delete(s.prepared, preparedKey{capability: action.Capability, digest: action.Digest})
	s.mu.Unlock()
	// A provider that keeps its own state for a prepared action may offer Release so a
	// declined action does not occupy it. Release must not block and is never given a
	// handle this session did not prepare.
	if releaser, ok := s.provider.(Releaser); ok && held {
		releaser.Release(action.Digest)
	}
}

// Releaser is implemented by a tool provider that holds state for prepared actions.
type Releaser interface{ Release(digest string) }

// invoke runs one provider call under the session and caller contexts. A provider
// that ignores cancellation cannot hold the run loop: the call returns a typed
// timeout or cancelled outcome and the abandoned goroutine finishes on its own.
// An empty outcome means the provider returned a value to validate.
func invoke[T any](s *Session, ctx context.Context, call func(context.Context) (T, error)) (T, Outcome) {
	var zero T
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	type result struct {
		value    T
		err      error
		panicked bool
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{panicked: true}
			}
		}()
		value, err := call(callCtx)
		done <- result{value: value, err: err}
	}()
	select {
	case r := <-done:
		switch {
		case r.panicked:
			return zero, OutcomeFailed
		case r.err != nil:
			if callCtx.Err() != nil {
				return zero, contextOutcome(callCtx)
			}
			if errors.Is(r.err, context.DeadlineExceeded) {
				return zero, OutcomeTimeout
			}
			if errors.Is(r.err, context.Canceled) {
				return zero, OutcomeCancelled
			}
			return zero, OutcomeFailed
		default:
			return r.value, ""
		}
	case <-callCtx.Done():
		return zero, contextOutcome(callCtx)
	}
}

func contextOutcome(ctx context.Context) Outcome {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	return OutcomeCancelled
}

// carriesPayload reports which outcomes may expose provider content. Everything
// else is stripped so a denied or failed reply cannot be mistaken for a result.
func carriesPayload(outcome Outcome) bool { return outcome == OutcomeOK || outcome == OutcomePartial }

func copyAccess(a LiveAccess) LiveAccess {
	copied := a
	copied.Capabilities = make(map[CapabilityID]AccessState, len(a.Capabilities))
	for id, state := range a.Capabilities {
		copied.Capabilities[id] = state
	}
	return copied
}

// reasonCodeOK accepts a short lowercase code, so a reason can never carry file content.
func reasonCodeOK(code string) bool {
	if code == "" || len(code) > maxEscalationLen {
		return false
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return false
		}
	}
	return true
}

// validateChange bounds what a prepared action reports about the change it would make.
func validateChange(action PreparedAction) error {
	if len(action.Paths) > MaxActionPaths || len(action.Preview) > MaxPreviewBytes || !utf8.ValidString(action.Preview) || len(action.Escalate) > MaxEscalations {
		return fmt.Errorf("%w: change description", ErrInvalid)
	}
	for _, path := range action.Paths {
		if path == "" || len(path) > MaxActionPath || !utf8.ValidString(path) || strings.ContainsAny(path, "\n\r\t\x00") {
			return fmt.Errorf("%w: action path", ErrInvalid)
		}
	}
	for _, code := range action.Escalate {
		if code == "" || len(code) > maxEscalationLen {
			return fmt.Errorf("%w: escalation code", ErrInvalid)
		}
		for _, r := range code {
			if !(r >= 'a' && r <= 'z' || r == '_') {
				return fmt.Errorf("%w: escalation code", ErrInvalid)
			}
		}
	}
	return nil
}

func digestOK(digest string) bool {
	if len(digest) < 8 || len(digest) > 128 {
		return false
	}
	for _, r := range digest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}

func boundedText(value string, allowEmpty bool) bool {
	if (value == "" && !allowEmpty) || len(value) > MaxFieldBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateEvidence(refs []EvidenceRef) error {
	if len(refs) > MaxEvidenceRefs {
		return fmt.Errorf("%w: evidence count", ErrInvalid)
	}
	for _, ref := range refs {
		if !boundedText(ref.ID, false) || !boundedText(ref.Revision, true) || !boundedText(ref.Class, true) {
			return fmt.Errorf("%w: evidence reference", ErrInvalid)
		}
	}
	return nil
}

func validateQuestion(q DecisionQuestion) error {
	if !identityRE.MatchString(q.Kind) {
		return fmt.Errorf("%w: decision kind", ErrInvalid)
	}
	if len(q.Candidates) < 1 || len(q.Candidates) > MaxCandidates {
		return fmt.Errorf("%w: candidate count", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(q.Candidates))
	for _, candidate := range q.Candidates {
		if !boundedText(candidate, false) {
			return fmt.Errorf("%w: candidate", ErrInvalid)
		}
		if _, duplicate := seen[candidate]; duplicate {
			return fmt.Errorf("%w: duplicate candidate", ErrInvalid)
		}
		seen[candidate] = struct{}{}
	}
	return validateEvidence(q.Evidence)
}

var prefixIDRE = regexp.MustCompile(`^[a-f0-9]{16,64}$`)

func validateModelRequest(q ModelRequest) error {
	if q.MaxOutputTokens < 1 || q.MaxOutputTokens > MaxOutputTokenLimit {
		return fmt.Errorf("%w: output token bound", ErrInvalid)
	}
	if len(q.Prompt) > MaxPromptBytes || !utf8.ValidString(q.Prompt) {
		return fmt.Errorf("%w: prompt", ErrInvalid)
	}
	if q.PrefixID != "" && !prefixIDRE.MatchString(q.PrefixID) {
		return fmt.Errorf("%w: prefix identity", ErrInvalid)
	}
	if len(q.ToolSchemaIDs) > MaxToolSchemas {
		return fmt.Errorf("%w: tool schema count", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(q.ToolSchemaIDs))
	for _, id := range q.ToolSchemaIDs {
		if !identityRE.MatchString(id) {
			return fmt.Errorf("%w: tool schema ID", ErrInvalid)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: duplicate tool schema", ErrInvalid)
		}
		seen[id] = struct{}{}
	}
	return validateEvidence(q.InputRefs)
}

func validateToolRequest(q ToolRequest) error {
	if !identityRE.MatchString(q.ToolID) {
		return fmt.Errorf("%w: tool ID", ErrInvalid)
	}
	if len(q.Arguments) > MaxPayloadBytes {
		return fmt.Errorf("%w: tool arguments size", ErrInvalid)
	}
	return nil
}
