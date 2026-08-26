package core

import "fmt"

type GraphReviewAction string

const (
	GraphReviewApprove    GraphReviewAction = "approve"
	GraphReviewReject     GraphReviewAction = "reject"
	GraphReviewSupersede  GraphReviewAction = "supersede"
	GraphReviewAnnotate   GraphReviewAction = "annotate"
	GraphReviewReconsider GraphReviewAction = "reconsider"
)

func ValidateGraphReviewAction(review GraphReview) error {
	if review.Action == "" {
		return ValidateGraphTrustTransition(review.From, review.To)
	}
	switch review.Action {
	case GraphReviewAnnotate:
		if review.From != review.To {
			return fmt.Errorf("%w: annotation cannot change trust state", ErrInvalidGraphTransition)
		}
		return nil
	case GraphReviewReconsider:
		if review.From != GraphTrustRejected && review.From != GraphTrustQuarantined && review.From != GraphTrustStale {
			return fmt.Errorf("%w: only rejected, quarantined, or stale records can be reconsidered", ErrInvalidGraphTransition)
		}
		return ValidateGraphTrustTransition(review.From, review.To)
	case GraphReviewApprove:
		if review.To != GraphTrustApproved || review.From == GraphTrustRejected {
			return fmt.Errorf("%w: approval requires an eligible non-rejected record", ErrInvalidGraphTransition)
		}
	case GraphReviewReject:
		if review.To != GraphTrustRejected {
			return fmt.Errorf("%w: rejection must enter rejected state", ErrInvalidGraphTransition)
		}
	case GraphReviewSupersede:
		if review.To != GraphTrustSuperseded {
			return fmt.Errorf("%w: supersession must enter superseded state", ErrInvalidGraphTransition)
		}
	default:
		return fmt.Errorf("%w: unsupported graph review action %q", ErrInvalidGraphTransition, review.Action)
	}
	return ValidateGraphTrustTransition(review.From, review.To)
}
