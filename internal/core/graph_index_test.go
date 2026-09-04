package core

import (
	"testing"
	"time"
)

func TestGraphScopeRequiresWorkspaceAndConsistentTenant(t *testing.T) {
	t.Parallel()

	if err := (GraphScope{}).Validate(); err == nil {
		t.Fatal("empty scope must be rejected")
	}
	if err := (GraphScope{WorkspaceID: "workspace-a"}).Validate(); err != nil {
		t.Fatalf("standalone scope rejected: %v", err)
	}
	if err := (GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}).Validate(); err != nil {
		t.Fatalf("hosted scope rejected: %v", err)
	}
}

func TestGraphRevisionTransitionsFailClosed(t *testing.T) {
	t.Parallel()

	allowed := []struct {
		from GraphRevisionState
		to   GraphRevisionState
	}{
		{GraphRevisionQueued, GraphRevisionProjecting},
		{GraphRevisionProjecting, GraphRevisionIndexing},
		{GraphRevisionIndexing, GraphRevisionValidating},
		{GraphRevisionValidating, GraphRevisionImporting},
		{GraphRevisionImporting, GraphRevisionEvaluating},
		{GraphRevisionEvaluating, GraphRevisionReady},
		{GraphRevisionReady, GraphRevisionActive},
		{GraphRevisionActive, GraphRevisionPrevious},
		{GraphRevisionPrevious, GraphRevisionActive},
		{GraphRevisionQueued, GraphRevisionCancelled},
		{GraphRevisionIndexing, GraphRevisionFailed},
	}
	for _, transition := range allowed {
		if err := ValidateGraphRevisionTransition(transition.from, transition.to); err != nil {
			t.Errorf("transition %s -> %s rejected: %v", transition.from, transition.to, err)
		}
	}

	rejected := []struct {
		from GraphRevisionState
		to   GraphRevisionState
	}{
		{GraphRevisionQueued, GraphRevisionActive},
		{GraphRevisionFailed, GraphRevisionActive},
		{GraphRevisionCancelled, GraphRevisionReady},
		{GraphRevisionPrevious, GraphRevisionImporting},
		{"unknown", GraphRevisionQueued},
	}
	for _, transition := range rejected {
		if err := ValidateGraphRevisionTransition(transition.from, transition.to); err == nil {
			t.Errorf("transition %s -> %s must be rejected", transition.from, transition.to)
		}
	}
}

func TestGraphRecordsRequireScopeAndStableIdentity(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	revision := GraphRevision{
		ID:              "revision-1",
		Scope:           GraphScope{WorkspaceID: "workspace-a"},
		ConfigurationID: "configuration-1",
		State:           GraphRevisionReady,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("valid revision rejected: %v", err)
	}

	revision.Scope.WorkspaceID = ""
	if err := revision.Validate(); err == nil {
		t.Fatal("revision without workspace must be rejected")
	}

	activation := GraphActivation{
		Scope:             GraphScope{WorkspaceID: "workspace-a"},
		ConfigurationID:   "configuration-1",
		ExpectedRevision:  "revision-0",
		CandidateRevision: "revision-1",
	}
	if err := activation.Validate(); err != nil {
		t.Fatalf("valid activation rejected: %v", err)
	}
	activation.CandidateRevision = activation.ExpectedRevision
	if err := activation.Validate(); err == nil {
		t.Fatal("activation must not replace a revision with itself")
	}
}

func TestGraphTrustTransitionsDoNotSilentlyReactivateRejectedKnowledge(t *testing.T) {
	t.Parallel()

	if err := ValidateGraphTrustTransition(GraphTrustProposed, GraphTrustApproved); err != nil {
		t.Fatalf("review approval rejected: %v", err)
	}
	if err := ValidateGraphTrustTransition(GraphTrustRejected, GraphTrustProposed); err == nil {
		t.Fatal("a later index output must not silently reactivate rejected knowledge")
	}
	if err := ValidateGraphTrustTransition(GraphTrustDeleted, GraphTrustApproved); err == nil {
		t.Fatal("deleted knowledge must not become approved")
	}
}
