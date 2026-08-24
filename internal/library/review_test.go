package library_test

import (
	"context"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"path/filepath"
	"testing"
	"time"
)

func TestKnowledgeReviewRequiresCuratorAndPreservesAuditHistory(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	org := core.Principal{ID: "org", Kind: core.PrincipalOrganization}
	curator := core.Principal{ID: "curator", Kind: core.PrincipalUser}
	now := time.Now().UTC()
	policy := core.AccessPolicy{Version: "v1", Ownership: core.ResourceOwnership{Owner: org, Visibility: core.VisibilityOrganization, OrganizationID: "org"}, Grants: []core.AccessGrant{{PrincipalID: "curator", Capabilities: []core.Capability{core.CapabilityApproveKnowledge}}}}
	review := library.KnowledgeReview{ID: "review", OrganizationID: "org", ProposalID: "proposal", State: core.ReviewProposed, Version: 1, Policy: policy, CreatedAt: now, UpdatedAt: now}
	service := library.NewKnowledgeReviewService(store)
	if err := service.Create(ctx, review); err != nil {
		t.Fatal(err)
	}
	peerScope := core.AuthorizationScope{Principal: core.Principal{ID: "peer", Kind: core.PrincipalUser}, OrganizationIDs: []string{"org"}, Capabilities: []core.Capability{core.CapabilityReadSource}, PolicyVersion: "v1"}
	if _, err := service.Transition(ctx, peerScope, "review", core.ReviewReviewed, "peer review", now.Add(time.Second)); err == nil {
		t.Fatal("ungranted organization peer reviewed knowledge")
	}
	scope := core.AuthorizationScope{Principal: curator, OrganizationIDs: []string{"org"}, Capabilities: []core.Capability{core.CapabilityApproveKnowledge}, PolicyVersion: "v1"}
	for index, target := range []core.ReviewState{core.ReviewReviewed, core.ReviewApproved, core.ReviewSuperseded} {
		updated, err := service.Transition(ctx, scope, "review", target, "curator decision", now.Add(time.Duration(index+1)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if updated.State != target {
			t.Fatal("transition lost")
		}
	}
	history, err := store.ListReviewTransitions(ctx, "review")
	if err != nil || len(history) != 3 || history[0].From != core.ReviewProposed || history[2].To != core.ReviewSuperseded {
		t.Fatalf("audit history incomplete: %+v err=%v", history, err)
	}
}
