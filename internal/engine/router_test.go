package engine

import (
	"testing"

	"github.com/time/timebooks/agent-memory/internal/core"
)

func TestHybridRouterDeterministicRules(t *testing.T) {
	r := NewHybridRouter()

	procedural := r.Decide(WriteInput{Type: core.ProceduralMemory, Content: "always run migration first"})
	if procedural.Tier != core.TierMarkdown || procedural.Rule != "R1" {
		t.Fatalf("expected R1 markdown, got %+v", procedural)
	}

	pinned := r.Decide(WriteInput{Type: core.SemanticMemory, Tags: []string{"Pinned"}, Content: "important fact"})
	if pinned.Tier != core.TierMarkdown || pinned.Rule != "R2" {
		t.Fatalf("expected R2 markdown, got %+v", pinned)
	}

	outcome := r.Decide(WriteInput{
		Type:    core.OutcomeMemory,
		Content: "retry strategy worked",
		Outcome: &core.Outcome{Result: core.OutcomeSuccess},
	})
	if outcome.Tier != core.TierVectorGraph || outcome.Rule != "R3" {
		t.Fatalf("expected R3 vector+graph, got %+v", outcome)
	}
}

