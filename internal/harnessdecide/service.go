package harnessdecide

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// Asker is what the service needs from a decision provider's session. A *harness.Session
// satisfies it, which is what production uses, so scope and generation gating, reply-size
// validation and panic containment already apply; tests substitute misbehaving ones.
type Asker interface {
	Envelope(capability harness.CapabilityID, maxBytes int) (harness.Envelope, error)
	Decide(ctx context.Context, question harness.DecisionQuestion) (harness.DecisionAnswer, error)
}

var _ Asker = (*harness.Session)(nil)

// Default limits.
const (
	DefaultMaxInFlight = 2
	DefaultRunBudget   = 40
	DefaultPerMinute   = 30
)

// Config describes one run's decision service.
type Config struct {
	Asker      Asker
	Capability harness.CapabilityID
	// MaxClass is the most sensitive data the provider may receive. The zero value is opaque.
	MaxClass Class
	// Enabled lists the kinds that may be asked. The zero value asks nothing.
	Enabled []Kind
	// Required marks raise-only kinds whose failure applies the cautious fallback. Each must
	// be enabled, allowed to be required, and eligible at MaxClass.
	Required []Kind
	// MaxInFlight bounds requests in flight; more are reported as busy, never queued.
	MaxInFlight int
	// RunBudget bounds the requests this service sends; PerMinute bounds their rate.
	RunBudget int
	PerMinute int
	// Health is shared by runs that use one provider. Nil makes a private one.
	Health *Health
	Now    func() time.Time

	// limiter is set by a Hub so that its services share one in-flight bound and rate.
	limiter *limiter
}

// Service asks one provider for advice on behalf of one run.
type Service struct {
	cfg      Config
	enabled  map[Kind]bool
	required map[Kind]bool
	health   *Health
	now      func() time.Time
	lim      *limiter

	mu   sync.Mutex
	used int
}

// limiter bounds how many requests are in flight and how fast they start. Services made
// by one Hub share it, because the provider's capacity and the person's bill do not depend
// on which run asked.
type limiter struct {
	slots     chan struct{}
	perMinute int
	now       func() time.Time

	mu     sync.Mutex
	recent []time.Time
}

func newLimiter(inFlight, perMinute int, now func() time.Time) *limiter {
	return &limiter{slots: make(chan struct{}, inFlight), perMinute: perMinute, now: now}
}

// allow takes one unit of the rate, or says the minute is full.
func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.recent[:0]
	for _, at := range l.recent {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	l.recent = kept
	if len(l.recent) >= l.perMinute {
		return false
	}
	l.recent = append(l.recent, now)
	return true
}

// New validates a configuration. A configuration that could not behave as stated, such as a
// required kind that its class forbids asking, is an error and not a silent downgrade.
func New(cfg Config) (*Service, error) {
	if cfg.Asker == nil || cfg.Capability == "" {
		return nil, errors.New("harnessdecide: a decision session and capability are required")
	}
	if cfg.MaxClass != ClassOpaque && cfg.MaxClass != ClassInternal {
		return nil, errors.New("harnessdecide: unknown data class")
	}
	if cfg.MaxInFlight < 0 || cfg.RunBudget < 0 || cfg.PerMinute < 0 {
		return nil, errors.New("harnessdecide: limits cannot be negative")
	}
	s := &Service{cfg: cfg, enabled: map[Kind]bool{}, required: map[Kind]bool{}, health: cfg.Health, now: cfg.Now}
	if s.now == nil {
		s.now = time.Now
	}
	if s.health == nil {
		s.health = NewHealth(s.now)
	}
	if s.cfg.MaxInFlight == 0 {
		s.cfg.MaxInFlight = DefaultMaxInFlight
	}
	if s.cfg.RunBudget == 0 {
		s.cfg.RunBudget = DefaultRunBudget
	}
	if s.cfg.PerMinute == 0 {
		s.cfg.PerMinute = DefaultPerMinute
	}
	s.lim = cfg.limiter
	if s.lim == nil {
		s.lim = newLimiter(s.cfg.MaxInFlight, s.cfg.PerMinute, s.now)
	}
	for _, kind := range cfg.Enabled {
		if _, ok := specs[kind]; !ok {
			return nil, fmt.Errorf("harnessdecide: unknown kind %q", kind)
		}
		s.enabled[kind] = true
	}
	for _, kind := range cfg.Required {
		spec, ok := specs[kind]
		switch {
		case !ok:
			return nil, fmt.Errorf("harnessdecide: unknown kind %q", kind)
		case !spec.CanRequire:
			return nil, fmt.Errorf("harnessdecide: %s cannot be required: its fallback would not only add caution", kind)
		case !s.enabled[kind]:
			return nil, fmt.Errorf("harnessdecide: required kind %s is not enabled", kind)
		case spec.Egress > cfg.MaxClass:
			return nil, fmt.Errorf("harnessdecide: required kind %s needs a higher data class than the provider is allowed", kind)
		}
		s.required[kind] = true
	}
	return s, nil
}

// Health returns the record the service reports into.
func (s *Service) Health() *Health { return s.health }

// Used is how many requests the run has sent.
func (s *Service) Used() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

// Enabled reports whether a kind may be asked.
func (s *Service) Enabled(kind Kind) bool { return s.enabled[kind] }

// request is a caller's input, before it becomes a question.
type request struct {
	items   []Item
	subject *Item
	// maxSelect caps a subset answer; zero uses the spec's cap.
	maxSelect int
}

// built is a question ready to send and what is needed to read the answer.
type built struct {
	question harness.DecisionQuestion
	real     map[string]string // candidate as sent -> the caller's identifier
	maxSel   int
}

func (s *Service) build(spec Spec, r request) (built, Status) {
	var b built
	b.real = map[string]string{}
	b.maxSel = spec.MaxSelect
	if r.maxSelect > 0 && (spec.MaxSelect == 0 || r.maxSelect < spec.MaxSelect) {
		b.maxSel = r.maxSelect
	}
	var candidates []string
	var evidence []harness.EvidenceRef
	seen := map[string]bool{}
	for i, item := range r.items {
		if err := spec.validateItem(item); err != nil || seen[item.ID] {
			return b, StatusInvalid
		}
		seen[item.ID] = true
		facts, _ := item.Facts.encode(spec)
		sent := item.ID
		if spec.Aliased {
			sent = "c" + strconv.Itoa(i)
		}
		b.real[sent] = item.ID
		candidates = append(candidates, sent)
		evidence = append(evidence, harness.EvidenceRef{ID: sent, Revision: facts, Class: item.Class, Note: item.Note})
	}
	switch spec.layout {
	case layoutLabels:
		candidates = append([]string(nil), spec.Labels...)
		for _, label := range candidates {
			b.real[label] = label
		}
	case layoutItemsOrNone:
		candidates = append(candidates, None)
		b.real[None] = None
	}
	if r.subject != nil {
		if err := spec.validateSubject(*r.subject); err != nil {
			return b, StatusInvalid
		}
		facts, _ := r.subject.Facts.encode(spec)
		evidence = append(evidence, harness.EvidenceRef{ID: "subject", Revision: facts, Class: r.subject.Class, Note: r.subject.Note})
	}
	if spec.Shape == ShapeSubset {
		// A subset answer is bounded, and the provider is told by how much so it does not
		// return an answer that would be thrown away.
		evidence = append(evidence, harness.EvidenceRef{ID: "subject", Revision: "k=" + strconv.Itoa(b.maxSel)})
	}
	b.question = harness.DecisionQuestion{Kind: string(spec.Kind), Candidates: candidates, Evidence: evidence}
	return b, ""
}

// validateSubject checks the one thing a labels kind asks about. Its identifier is never
// sent, so it is not checked.
func (s Spec) validateSubject(subject Item) error {
	if len(subject.Class) > maxClassBytes || !classRE.MatchString(subject.Class) {
		return fmt.Errorf("class")
	}
	if _, err := subject.Facts.encode(s); err != nil {
		return err
	}
	return s.validateNote(subject.Note)
}

// reserve takes one unit of the run's budget and of the shared rate, or says which is spent.
// A refusal for rate gives the budget back, so only requests that can go out are counted.
func (s *Service) reserve() Status {
	s.mu.Lock()
	if s.used >= s.cfg.RunBudget {
		s.mu.Unlock()
		return StatusExhausted
	}
	s.used++
	s.mu.Unlock()
	if !s.lim.allow() {
		s.mu.Lock()
		s.used--
		s.mu.Unlock()
		return StatusBusy
	}
	return ""
}

// ask runs one request end to end and returns the candidates the provider selected, as the
// caller's own identifiers, or nothing, together with why.
func (s *Service) ask(ctx context.Context, kind Kind, r request) ([]string, Outcome) {
	spec := specs[kind]
	out := Outcome{Kind: kind}
	finish := func(status Status) ([]string, Outcome) {
		out.Status = status
		s.health.record(out)
		return nil, out
	}
	switch {
	case !s.enabled[kind]:
		return finish(StatusDisabled)
	case spec.Egress > s.cfg.MaxClass:
		return finish(StatusIneligible)
	case len(r.items) < spec.MinItems:
		return finish(StatusNotNeeded)
	case len(r.items) > spec.MaxItems:
		return finish(StatusInvalid)
	}
	if spec.gate != nil {
		facts := Facts{}
		if r.subject != nil {
			facts = r.subject.Facts
		}
		if !spec.gate(facts) {
			return finish(StatusNotNeeded)
		}
	}
	if ctx.Err() != nil { // work for a caller that has gone is not started
		return finish(StatusCancelled)
	}
	b, status := s.build(spec, r)
	if status != "" {
		return finish(status)
	}
	out.Asked = len(b.question.Candidates)
	permission, ok := s.health.admit(kind)
	if !ok {
		return finish(StatusPaused)
	}
	if status := s.reserve(); status != "" {
		s.health.settle(kind, permission, status)
		return finish(status)
	}
	select {
	case s.lim.slots <- struct{}{}:
		defer func() { <-s.lim.slots }()
	default:
		s.health.settle(kind, permission, StatusBusy)
		return finish(StatusBusy)
	}
	envelope, err := s.cfg.Asker.Envelope(s.cfg.Capability, spec.MaxReplyBytes)
	if err != nil {
		s.health.settle(kind, permission, StatusFailed)
		return finish(StatusFailed)
	}
	b.question.Envelope = envelope

	started := s.now()
	answer, status := s.call(ctx, spec, b.question)
	out.Latency = s.now().Sub(started)
	if ctx.Err() != nil {
		// Whatever the provider did or did not do, the caller has gone. That is not the
		// provider's fault, advice nobody is waiting for is not applied, and the select above
		// may have seen either event first, so the result must not depend on which.
		status = StatusCancelled
	}
	if status == "" {
		status = s.check(spec, b, &answer)
	}
	out.Confidence = 0
	if status == StatusApplied || status == StatusLowConfidence {
		out.Confidence = answer.Confidence
	}
	s.health.settle(kind, permission, status)
	if status != StatusApplied {
		return finish(status)
	}
	selected := make([]string, len(answer.Selected))
	for i, candidate := range answer.Selected {
		selected[i] = b.real[candidate]
	}
	out.Status = StatusApplied
	s.health.record(out)
	return selected, out
}

// call asks the provider within the kind's deadline. The deadline is enforced here with a
// timer, not by trusting the provider to honor its context: a provider that ignores
// cancellation is abandoned, and counted until it returns so the number is bounded.
func (s *Service) call(ctx context.Context, spec Spec, question harness.DecisionQuestion) (harness.DecisionAnswer, Status) {
	callCtx, cancel := context.WithTimeout(ctx, spec.Deadline)
	defer cancel()
	type result struct {
		answer harness.DecisionAnswer
		err    error
	}
	done := make(chan result, 1)
	var mu sync.Mutex
	finished, abandoned := false, false
	go func() {
		var res result
		func() {
			defer func() {
				if recover() != nil {
					res = result{err: errors.New("provider panicked")}
				}
			}()
			res.answer, res.err = s.cfg.Asker.Decide(callCtx, question)
		}()
		done <- res
		mu.Lock()
		finished = true
		if abandoned {
			s.health.returned()
		}
		mu.Unlock()
	}()
	select {
	case res := <-done:
		switch {
		case res.err != nil:
			return harness.DecisionAnswer{}, statusOfError(res.err)
		case res.answer.Outcome != harness.OutcomeOK:
			return harness.DecisionAnswer{}, statusOfOutcome(res.answer.Outcome)
		}
		return res.answer, ""
	case <-callCtx.Done():
		mu.Lock()
		if !finished {
			abandoned = true
			s.health.abandon()
		}
		mu.Unlock()
		return harness.DecisionAnswer{}, StatusTimeout
	}
}

// check validates an answer against exactly what was asked and applies the kind's floor.
func (s *Service) check(spec Spec, b built, answer *harness.DecisionAnswer) Status {
	if err := harness.ValidateDecisionAnswer(b.question, *answer); err != nil {
		return statusOfError(err)
	}
	n := len(answer.Selected)
	switch spec.Shape {
	case ShapeOne:
		if n != 1 {
			return StatusInvalid
		}
	case ShapeSubset:
		if n < spec.MinSelect || n > b.maxSel {
			return StatusInvalid
		}
	}
	if !(answer.Confidence >= spec.MinConfidence) { // written so that NaN is below the floor too
		return StatusLowConfidence
	}
	return StatusApplied
}
