package harnessrun

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// ErrNoEligibleModel is returned by a ModelRouter when no provider may serve the call.
// The run fails with an explicit code rather than sending the data to an ineligible one.
var ErrNoEligibleModel = errors.New("no eligible model provider")

// ModelNeed describes one model call to a router.
type ModelNeed struct {
	// Class is the data class of the prompt, 0 (public) through 3 (restricted).
	Class          int
	InputTokens    int
	OutputTokens   int
	PrefixID       string
	HasSpendCap    bool
	SpendCapMicros int64
}

// ModelObservation reports how one provider call went, so a router can learn latency,
// cost and cache evidence from the provider's own reports.
type ModelObservation struct {
	Outcome    harness.Outcome
	Latency    time.Duration
	Usage      harness.Usage
	CostMicros int64
	PrefixID   string
}

// ModelRouter plans the ordered chain of providers eligible for a call and learns from
// outcomes. The loop falls back only along the chain it returns.
type ModelRouter interface {
	Plan(ctx context.Context, need ModelNeed) ([]Binding, error)
	Observe(provider harness.ProviderID, observation ModelObservation)
}

// modelSet holds the provider sessions one run has opened. Sessions are opened on first
// use and all closed together when the run stops.
type modelSet struct {
	manager  *Manager
	ctx      context.Context
	scope    harness.Scope
	mu       sync.Mutex
	sessions map[harness.ProviderID]*harness.Session
}

func (s *modelSet) get(binding Binding) (*harness.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[binding.Provider]; session != nil {
		return session, nil
	}
	session, err := s.manager.cfg.Registry.OpenSession(s.ctx, binding.Provider, s.scope, harness.WithCallTimeout(s.manager.cfg.CallTimeout))
	if err != nil {
		return nil, err
	}
	if s.sessions == nil {
		s.sessions = make(map[harness.ProviderID]*harness.Session)
	}
	s.sessions[binding.Provider] = session
	return session, nil
}

func (s *modelSet) closeAll(timeout time.Duration) {
	s.mu.Lock()
	sessions := make([]*harness.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.sessions = nil
	s.mu.Unlock()
	for _, session := range sessions {
		closeBounded(session, timeout)
	}
}

// fallbackable reports whether a provider outcome means another eligible provider may
// be tried. Success, cancellation and a budget-driven timeout end the attempt chain.
func fallbackable(outcome harness.Outcome) bool {
	switch outcome {
	case harness.OutcomeFailed, harness.OutcomeUnavailable, harness.OutcomeDenied, harness.OutcomeUnsupported, harness.OutcomeStale, harness.OutcomeTimeout:
		return true
	}
	return false
}
