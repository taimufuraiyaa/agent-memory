package engine

import (
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// RouteDecision captures tier decision and explanation.
type RouteDecision struct {
	Tier       core.StorageTier
	Importance float64
	Rule       string
	Reason     string
}

// HybridRouter implements deterministic tier routing rules.
type HybridRouter struct{}

func NewHybridRouter() HybridRouter { return HybridRouter{} }

func (r HybridRouter) Decide(in WriteInput) RouteDecision {
	contentLen := len(strings.Fields(in.Content))

	if in.Type == core.ProceduralMemory {
		return RouteDecision{Tier: core.TierMarkdown, Importance: 0.95, Rule: "R1", Reason: "procedural memory is always-on guidance"}
	}
	if containsPinned(in.Tags) {
		return RouteDecision{Tier: core.TierMarkdown, Importance: 0.95, Rule: "R2", Reason: "pinned tag routes to markdown tier"}
	}
	if in.Outcome != nil && in.Outcome.Result == core.OutcomeSuccess {
		return RouteDecision{Tier: core.TierVectorGraph, Importance: 0.85, Rule: "R3", Reason: "successful outcome gets relationship-rich tier"}
	}
	if contentLen > 220 {
		return RouteDecision{Tier: core.TierDocument, Importance: 0.5, Rule: "R4", Reason: "large content routed to document tier"}
	}
	if in.Type == core.EpisodicMemory {
		return RouteDecision{Tier: core.TierDocument, Importance: 0.45, Rule: "R5", Reason: "episodic content defaults to document tier"}
	}
	if in.Type == core.SemanticMemory {
		return RouteDecision{Tier: core.TierVector, Importance: 0.75, Rule: "R6", Reason: "semantic facts route to vector tier"}
	}
	return RouteDecision{Tier: core.TierVector, Importance: 0.6, Rule: "R7", Reason: "fallback deterministic default"}
}

func containsPinned(tags []string) bool {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), "pinned") {
			return true
		}
	}
	return false
}

