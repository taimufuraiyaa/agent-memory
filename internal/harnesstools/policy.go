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

// EditPolicy allows the read tools and asks for every change. Edits and creates ask with
// the ordinary friction; deletes, control-plane files and anything unusual ask with extra.
func EditPolicy() Policy {
	return NewPolicy(map[harness.CapabilityID]Tier{ToolReadFile: TierAllow, ToolListDir: TierAllow, ToolSearch: TierAllow,
		ToolEditFile: TierAsk, ToolCreateFile: TierAsk, ToolDeleteFile: TierAsk})
}

var (
	_ harnessrun.ToolPolicy     = Policy{}
	_ harnessrun.StrictReasoner = Policy{}
)

// tier is the configured tier with one rule that no table can override: a tool that
// changes files is never allowed to run without asking, so a table that says otherwise is
// treated as ask.
func (p Policy) tier(capability harness.CapabilityID) Tier {
	tier := p.tiers[capability]
	if Mutating(capability) && tier == TierAllow {
		return TierAsk
	}
	return tier
}

// Reasons lists, as fixed codes, why a change needs extra friction: the kind of
// control-plane file any touched path names (classified here, from the path, never taken
// from the provider), a deletion, and whatever the provider escalated. A provider can only
// add reasons; it cannot remove one that policy found.
func (p Policy) Reasons(action harness.PreparedAction) []string {
	var reasons []string
	for _, path := range action.Paths {
		if kind := ControlPlane(path); kind != "" {
			reasons = append(reasons, kind)
		}
	}
	if action.Capability == ToolDeleteFile {
		reasons = append(reasons, "delete")
	}
	return append(reasons, action.Escalate...)
}

// Decide classifies one prepared action by its capability and the paths it touches. An
// action that did not prepare successfully, or that has no digest to bind to, is denied.
func (p Policy) Decide(_ context.Context, _ harnessrun.Owner, action harness.PreparedAction) harnessrun.Decision {
	if action.Outcome != harness.OutcomeOK || action.Digest == "" {
		return harnessrun.DecisionDeny
	}
	switch p.tier(action.Capability) {
	case TierAllow:
		return harnessrun.DecisionAllow
	case TierAsk:
		if len(p.Reasons(action)) > 0 {
			return harnessrun.DecisionAskStrict
		}
		return harnessrun.DecisionAsk
	}
	return harnessrun.DecisionDeny
}
