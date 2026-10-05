// Package harnessjev is the TypeSafe Jev decision provider. It implements the harness's
// decision port over the System One typed-choice API and nothing more: it carries a question
// the decision service already bounded and classified, sends it as one choice question built
// from fixed text, numbers and aliases, and returns the choice as advice. It holds no policy
// and no authority, and no outcome it produces carries service text or the credential.
package harnessjev

import (
	"context"
	"errors"
	"sort"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
)

const (
	// ProviderID and Capability are the identity this provider registers under.
	ProviderID harness.ProviderID   = "typesafe-jev"
	Capability harness.CapabilityID = "decide"

	// subsetThreshold is the probability at or above which a candidate besides the top choice
	// joins a subset answer.
	subsetThreshold = 0.2
)

var (
	errUnsupported = errors.New("harnessjev: unsupported decision kind")
	errMalformed   = errors.New("harnessjev: malformed decision question")
)

// Config describes the provider. Nothing here has a default that could widen what leaves the
// machine: the credential port and the egress confirmation are both required.
type Config struct {
	// Client talks to the service; nil uses the fixed TypeSafe endpoint.
	Client *jev.Client
	// Token returns the bearer credential. It is called for every request and its result is
	// never stored, logged or returned.
	Token func(context.Context) (string, error)
	// AllowEgress confirms that decision questions are sent to TypeSafe.
	AllowEgress bool
}

// Provider is the registered decision provider.
type Provider struct {
	client *jev.Client
	token  func(context.Context) (string, error)
}

// NewProvider validates a configuration.
func NewProvider(cfg Config) (*Provider, error) {
	if !cfg.AllowEgress {
		return nil, errors.New("harnessjev: sending decision questions to TypeSafe must be confirmed explicitly")
	}
	if cfg.Token == nil {
		return nil, errors.New("harnessjev: a credential source is required")
	}
	client := cfg.Client
	if client == nil {
		client = jev.NewClient()
	}
	return &Provider{client: client, token: cfg.Token}, nil
}

// Manifest declares the provider.
func (p *Provider) Manifest() harness.Manifest {
	return harness.Manifest{Version: harness.ContractVersion, ID: ProviderID, Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{Capability}}
}

// Register adds the provider to a registry.
func (p *Provider) Register(registry *harness.Registry) error {
	return registry.Register(p.Manifest(), func() (harness.Provider, error) { return &session{p: p}, nil })
}

type session struct{ p *Provider }

func (s *session) Close() error { return nil }

// Probe reports live access from the model listing, which costs no decision request. A
// missing credential is unavailable, a refused one is denied, and anything else that goes
// wrong is unavailable.
func (s *session) Probe(ctx context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	state := harness.AccessUnavailable
	if token, err := s.p.token(ctx); err == nil && token != "" {
		switch err := s.p.client.Probe(ctx, token); {
		case err == nil:
			state = harness.AccessAvailable
		case errors.Is(err, jev.ErrDenied):
			state = harness.AccessDenied
		}
	}
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: ProviderID, Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{Capability: state}}, nil
}

// Decide sends one decision question and returns the service's choice. Every failure is a
// typed outcome and none carries text.
func (s *session) Decide(ctx context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	reply := func(outcome harness.Outcome) (harness.DecisionAnswer, error) {
		return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: outcome}, nil
	}
	r, err := render(q)
	switch {
	case errors.Is(err, errUnsupported):
		return reply(harness.OutcomeUnsupported)
	case err != nil:
		return reply(harness.OutcomeFailed)
	}
	token, err := s.p.token(ctx)
	if err != nil || token == "" {
		return reply(harness.OutcomeDenied)
	}
	answers, err := s.p.client.Choose(ctx, token, r.state, map[string]jev.ChoiceQuestion{questionID: {Instructions: r.instructions, Criteria: r.criteria}})
	if err != nil {
		return reply(outcomeOf(err))
	}
	answer := answers[questionID] // the client has checked that there is exactly one answer, to this question
	spec, _ := harnessdecide.SpecOf(harnessdecide.Kind(q.Kind))
	selected := fit(choose(spec, r, answer), q.MaxBytes)
	if len(selected) == 0 {
		return reply(harness.OutcomeFailed)
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Selected: selected, Confidence: answer.Confidence}, nil
}

// fit keeps the leading part of an answer that fits the reply bound, so a provider answers
// within its envelope or reports a typed failure and never breaks the contract.
func fit(selected []string, maxBytes int) []string {
	used := 0
	for i, candidate := range selected {
		used += len(candidate)
		if used > maxBytes {
			return selected[:i]
		}
	}
	return selected
}

func outcomeOf(err error) harness.Outcome {
	switch {
	case errors.Is(err, context.Canceled):
		return harness.OutcomeCancelled
	case errors.Is(err, jev.ErrTimeout):
		return harness.OutcomeTimeout
	case errors.Is(err, jev.ErrDenied):
		return harness.OutcomeDenied
	case errors.Is(err, jev.ErrUnavailable):
		return harness.OutcomeUnavailable
	}
	return harness.OutcomeFailed
}

// choose maps the service's choice back to harness candidates. A one-answer kind gets the
// choice. A subset kind gets the candidates the service found at least moderately likely,
// most likely first and no more than the question allowed, and always the choice itself.
func choose(spec harnessdecide.Spec, r rendered, answer jev.ChoiceAnswer) []string {
	index := func(key string) int {
		for i := range r.candidates {
			if alias(i) == key {
				return i
			}
		}
		return -1
	}
	top := index(answer.Choice)
	if top < 0 {
		return nil
	}
	if spec.Shape == harnessdecide.ShapeOne {
		return []string{r.candidates[top]}
	}
	type scored struct {
		position int
		p        float64
	}
	var ranked []scored
	for key, p := range answer.Probabilities {
		if i := index(key); i >= 0 && i != top && p >= subsetThreshold {
			ranked = append(ranked, scored{i, p})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].p != ranked[j].p {
			return ranked[i].p > ranked[j].p
		}
		return ranked[i].position < ranked[j].position
	})
	out := []string{r.candidates[top]}
	for _, c := range ranked {
		if len(out) >= r.limit {
			break
		}
		out = append(out, r.candidates[c.position])
	}
	return out
}
