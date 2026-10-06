package core

import "testing"

func TestGraphReviewActionsValidateAnnotationAndReconsideration(t *testing.T) {
	t.Parallel()
	annotation := GraphReview{Action: GraphReviewAnnotate, From: GraphTrustApproved, To: GraphTrustApproved}
	if err := ValidateGraphReviewAction(annotation); err != nil {
		t.Fatalf("annotation rejected: %v", err)
	}
	reconsider := GraphReview{Action: GraphReviewReconsider, From: GraphTrustRejected, To: GraphTrustReviewed}
	if err := ValidateGraphReviewAction(reconsider); err != nil {
		t.Fatalf("reconsideration rejected: %v", err)
	}
	invalid := GraphReview{Action: GraphReviewApprove, From: GraphTrustRejected, To: GraphTrustApproved}
	if err := ValidateGraphReviewAction(invalid); err == nil {
		t.Fatal("rejected record silently re-approved without reconsideration")
	}
}
