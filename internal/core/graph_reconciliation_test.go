package core

import "testing"

func TestGraphEntityReconciliationCandidateValidation(t *testing.T) {
	t.Parallel()
	scope := GraphScope{WorkspaceID: "workspace-a"}
	valid := GraphEntityCandidate{
		Scope: scope, RevisionID: "revision-2", ExternalID: "entity-7",
		Name: "Book A", EntityType: "work", Evidence: []GraphEvidence{{
			ID: "evidence-1", Scope: scope, CanonicalKind: "memory", CanonicalID: "memory-10",
			CanonicalFingerprint: "sha256:memory-10", OccurrenceCount: 1,
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}

	invalid := valid
	invalid.Evidence = nil
	if err := invalid.Validate(); err == nil {
		t.Fatal("evidence-free candidate accepted")
	}
}

func TestGraphEntityReconciliationLineageValidation(t *testing.T) {
	t.Parallel()
	lineage := GraphEntityLineage{
		Scope: GraphScope{WorkspaceID: "workspace-a"}, RevisionID: "revision-2",
		Kind: GraphEntityLineageMerge, FromEntityID: "entity-old", ToEntityID: "entity-current",
		ReasonCode: "approved_merge",
	}
	if err := lineage.Validate(); err != nil {
		t.Fatalf("valid lineage rejected: %v", err)
	}
	lineage.Kind = "collapse"
	if err := lineage.Validate(); err == nil {
		t.Fatal("unknown lineage kind accepted")
	}
}
