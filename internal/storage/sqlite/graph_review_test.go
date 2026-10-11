package sqlite

import (
	"context"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestRejectedGraphEdgeRemainsExcludedAfterLaterRevisionImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	seedGraphEntities(t, store)
	edge, version, evidence := graphEdgeFixture("edge-1", "revision-1")
	if err := store.ImportGraphEdge(ctx, edge, version, evidence); err != nil {
		t.Fatal(err)
	}

	review := core.GraphReview{
		ID: "review-1", Scope: edge.Scope, TargetKind: "edge", TargetID: edge.ID,
		From: core.GraphTrustProposed, To: core.GraphTrustRejected, ExpectedVersion: 1,
		ReviewerID: "reviewer-1", Reason: "unsupported inference",
	}
	if err := store.ReviewGraphRecord(ctx, review); err != nil {
		t.Fatal(err)
	}

	laterEdge, laterVersion, laterEvidence := graphEdgeFixture("edge-1", "revision-2")
	if err := store.ImportGraphEdge(ctx, laterEdge, laterVersion, laterEvidence); err != nil {
		t.Fatal(err)
	}
	queryable, err := store.ListQueryableGraphEdges(ctx, edge.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(queryable) != 0 {
		t.Fatalf("rejected edge was reactivated: %#v", queryable)
	}

	var trust string
	if err := store.db.QueryRowContext(ctx, `SELECT trust FROM graph_edges WHERE id = ?`, edge.ID).Scan(&trust); err != nil {
		t.Fatal(err)
	}
	if trust != string(core.GraphTrustRejected) {
		t.Fatalf("stored trust = %q", trust)
	}
}

func TestGraphReviewReportUsesReviewVersionAndBecomesImmediatelyNonQueryable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	seedGraphEntities(t, store)
	scope := core.GraphScope{WorkspaceID: "workspace-a"}
	community := core.GraphCommunity{ID: "community-review", Scope: scope, ConfigurationID: "configuration-1", RevisionID: "revision-1", ExternalID: "external", EntityCount: 1, SourceCount: 1, MembershipFingerprint: "sha256:members", EvidenceFingerprint: "sha256:evidence"}
	report := core.GraphReport{ID: "report-review", Scope: scope, CommunityID: community.ID, RevisionID: "revision-1", Title: "Review", Summary: "Navigation only", Rank: 0.8, Trust: core.GraphTrustProposed, AdmissionState: core.GraphReportAdmitted, EvidenceCount: 1}
	if err := store.ImportGraphCommunity(ctx, community, []GraphCommunityMember{{Kind: "entity", TargetID: "entity-1"}}, report); err != nil {
		t.Fatal(err)
	}
	if err := store.ReviewGraphRecord(ctx, core.GraphReview{ID: "review-report", Scope: scope, Action: core.GraphReviewReject, TargetKind: "report", TargetID: report.ID, From: core.GraphTrustProposed, To: core.GraphTrustRejected, ExpectedVersion: 1, ReviewerID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GraphReport(ctx, scope, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Trust != core.GraphTrustRejected || !stored.Stale || stored.ReviewVersion != 2 {
		t.Fatalf("reviewed report = %#v", stored)
	}
}

func TestGraphReviewUsesOptimisticVersionAndFeedbackIsTargeted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openGraphIndexStore(t)
	seedGraphEntities(t, store)
	edge, version, evidence := graphEdgeFixture("edge-1", "revision-1")
	if err := store.ImportGraphEdge(ctx, edge, version, evidence); err != nil {
		t.Fatal(err)
	}
	review := core.GraphReview{
		ID: "review-stale", Scope: edge.Scope, TargetKind: "edge", TargetID: edge.ID,
		From: core.GraphTrustProposed, To: core.GraphTrustApproved, ExpectedVersion: 2, ReviewerID: "reviewer-1",
	}
	if err := store.ReviewGraphRecord(ctx, review); err == nil {
		t.Fatal("stale review version must fail")
	}

	feedback := core.GraphFeedback{
		ID: "feedback-1", Scope: edge.Scope, RequestID: "request-1", TargetKind: "edge",
		TargetID: edge.ID, Outcome: "helpful",
	}
	if err := store.RecordGraphFeedback(ctx, feedback); err != nil {
		t.Fatal(err)
	}
	var targetKind, targetID string
	if err := store.db.QueryRowContext(ctx, `SELECT target_kind, target_id FROM graph_feedback WHERE id = ?`, feedback.ID).Scan(&targetKind, &targetID); err != nil {
		t.Fatal(err)
	}
	if targetKind != "edge" || targetID != edge.ID {
		t.Fatalf("feedback target = %s/%s", targetKind, targetID)
	}
	for _, targetKind := range []string{"request", "route", "path", "entity", "edge", "report", "memory"} {
		feedback.ID = "feedback-" + targetKind
		feedback.TargetKind = targetKind
		feedback.TargetID = "target-" + targetKind
		if err := store.RecordGraphFeedback(ctx, feedback); err != nil {
			t.Fatalf("targeted %s feedback: %v", targetKind, err)
		}
	}
	feedback.ID = "feedback-invalid"
	feedback.TargetKind = "community_summary_as_evidence"
	if err := store.RecordGraphFeedback(ctx, feedback); err == nil {
		t.Fatal("unsupported graph feedback target was accepted")
	}
}
