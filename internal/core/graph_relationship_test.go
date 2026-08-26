package core

import "testing"

func TestGraphEdgeImportNormalizesOnlyUnambiguousRelationshipKinds(t *testing.T) {
	t.Parallel()
	cases := map[string]GraphRelationshipKind{
		"supports":        GraphRelationshipSupports,
		"CONTRADICTS":     GraphRelationshipContradicts,
		"member of":       GraphRelationshipMembership,
		"causes/supports": GraphRelationshipExternal,
		"works beside":    GraphRelationshipExternal,
	}
	for external, expected := range cases {
		if got := NormalizeGraphRelationshipKind(external); got != expected {
			t.Errorf("NormalizeGraphRelationshipKind(%q) = %q, want %q", external, got, expected)
		}
	}
}

func TestGraphEdgeImportContradictionsNeverSupportTraversal(t *testing.T) {
	t.Parallel()
	for _, kind := range []GraphRelationshipKind{GraphRelationshipContradicts, GraphRelationshipChallenges} {
		if kind.SupportsTraversal() {
			t.Fatalf("conflict kind %q counted as support", kind)
		}
	}
	if !GraphRelationshipSupports.SupportsTraversal() {
		t.Fatal("support relationship excluded from support traversal")
	}
}
