package harnesstools

import (
	"context"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// Tier is the approval level of one tool. The zero value denies, so a tool nobody
// classified can never run.
type Tier int

const (
	TierDeny Tier = iota
	// TierAllow runs without asking.
	TierAllow
	// TierAsk parks the run for a trusted local approval.
	TierAsk
)

// Policy maps each capability to a tier. It is deterministic local policy: nothing in
// the model's text, a file's content or a Jev recommendation can change it.
type Policy struct{ tiers map[harness.CapabilityID]Tier }

// NewPolicy builds a policy from an explicit table.
func NewPolicy(tiers map[harness.CapabilityID]Tier) Policy {
	copied := make(map[harness.CapabilityID]Tier, len(tiers))
	for id, tier := range tiers {
		copied[id] = tier
	}
	return Policy{tiers: copied}
}

// ReadOnlyPolicy allows the three read tools and nothing else.
func ReadOnlyPolicy() Policy {
	return NewPolicy(map[harness.CapabilityID]Tier{ToolReadFile: TierAllow, ToolListDir: TierAllow, ToolSearch: TierAllow})
}

var _ harnessrun.ToolPolicy = Policy{}

// Decide classifies one prepared action by its capability. An action that did not
// prepare successfully, or that has no digest to bind to, is denied.
func (p Policy) Decide(_ context.Context, _ harnessrun.Owner, action harness.PreparedAction) harnessrun.Decision {
	if action.Outcome != harness.OutcomeOK || action.Digest == "" {
		return harnessrun.DecisionDeny
	}
	switch p.tiers[action.Capability] {
	case TierAllow:
		return harnessrun.DecisionAllow
	case TierAsk:
		return harnessrun.DecisionAsk
	}
	return harnessrun.DecisionDeny
}
