package harnessmodel

import (
	"context"
	"errors"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// LoopRouter adapts a Router and its Meter to the run loop's ModelRouter. The loop asks it
// for an eligible chain each turn and reports every attempt back, so latency, cost and
// provider-reported cache evidence feed the next selection.
type LoopRouter struct {
	Router *Router
	Meter  *Meter
}

var _ harnessrun.ModelRouter = LoopRouter{}

func (l LoopRouter) Plan(ctx context.Context, need harnessrun.ModelNeed) ([]harnessrun.Binding, error) {
	chain, _, err := l.Router.Plan(ctx, Need{Class: Class(need.Class), InputTokens: need.InputTokens, OutputTokens: need.OutputTokens,
		PrefixID: need.PrefixID, HasSpendCap: need.HasSpendCap, SpendCapMicros: need.SpendCapMicros})
	if errors.Is(err, ErrNoEligible) {
		return nil, harnessrun.ErrNoEligibleModel
	}
	if err != nil {
		return nil, err
	}
	bindings := make([]harnessrun.Binding, len(chain))
	for i, c := range chain {
		bindings[i] = harnessrun.Binding{Provider: c.Profile.Provider, Capability: c.Profile.Capability}
	}
	return bindings, nil
}

// Observe records one attempt. Cache savings are credited only for a completed call that
// carried a prefix identity and in which the provider itself reported cached input.
func (l LoopRouter) Observe(provider harness.ProviderID, o harnessrun.ModelObservation) {
	var savings int64
	if p, ok := l.Router.Profile(provider); ok && (o.Outcome == harness.OutcomeOK || o.Outcome == harness.OutcomePartial) {
		savings = p.Pricing.CacheSavings(o.Usage, o.PrefixID)
	}
	l.Meter.Observe(Observation{Provider: provider, Outcome: o.Outcome, Latency: o.Latency, Usage: o.Usage, PrefixID: o.PrefixID,
		CostMicros: o.CostMicros, CacheSavingsMicros: savings})
}
