package harnessdecide

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// Status is why a request ended the way it did. Applied is the only one that carries advice;
// every other status means the caller's deterministic choice stands.
type Status string

const (
	StatusApplied       Status = "applied"
	StatusDisabled      Status = "disabled"       // the operator did not enable the kind
	StatusIneligible    Status = "ineligible"     // the question would carry more than the provider may receive
	StatusNotNeeded     Status = "not_needed"     // there is nothing to decide, or it cannot be measured yet
	StatusInvalid       Status = "invalid"        // the request or the answer is malformed or outside its candidates
	StatusBusy          Status = "busy"           // too many requests in flight or too fast
	StatusExhausted     Status = "exhausted"      // the run's decision budget is spent
	StatusPaused        Status = "paused"         // repeated failures: not asking for a while
	StatusLowConfidence Status = "low_confidence" // below the kind's confidence floor
	StatusTimeout       Status = "timeout"
	StatusDenied        Status = "denied"
	StatusUnavailable   Status = "unavailable"
	StatusStale         Status = "stale"
	StatusCancelled     Status = "cancelled"
	StatusFailed        Status = "failed"
)

// Statuses lists every status in a fixed order.
func Statuses() []Status {
	return []Status{StatusApplied, StatusDisabled, StatusIneligible, StatusNotNeeded, StatusInvalid, StatusBusy, StatusExhausted, StatusPaused,
		StatusLowConfidence, StatusTimeout, StatusDenied, StatusUnavailable, StatusStale, StatusCancelled, StatusFailed}
}

// Outcome describes one request. It carries no identifier and no text.
type Outcome struct {
	Kind       Kind
	Status     Status
	Confidence float64
	Latency    time.Duration
	// Asked is how many candidates the question held.
	Asked int
	// Cautious says a required kind's cautious fallback was applied instead of advice.
	Cautious bool
}

// providerFault says whether a status shows the provider itself is unhealthy, as opposed to a
// bad request or a merely unhelpful answer.
func providerFault(s Status) bool {
	switch s {
	case StatusTimeout, StatusUnavailable, StatusDenied, StatusFailed:
		return true
	}
	return false
}

// statusOfOutcome maps a provider's own outcome to a status. OK is handled by the caller.
func statusOfOutcome(o harness.Outcome) Status {
	switch o {
	case harness.OutcomeDenied:
		return StatusDenied
	case harness.OutcomeStale:
		return StatusStale
	case harness.OutcomeTimeout:
		return StatusTimeout
	case harness.OutcomeCancelled:
		return StatusCancelled
	case harness.OutcomeUnavailable, harness.OutcomeUnsupported:
		return StatusUnavailable
	default: // failed, partial or anything unrecognized
		return StatusFailed
	}
}

// statusOfError maps an error from the session or the provider.
func statusOfError(err error) Status {
	switch {
	case errors.Is(err, harness.ErrClosed), errors.Is(err, context.Canceled): // ErrClosed wraps ErrStale, so it is asked first
		return StatusCancelled
	case errors.Is(err, harness.ErrStale):
		return StatusStale
	case errors.Is(err, harness.ErrInvalid):
		return StatusInvalid
	case errors.Is(err, context.DeadlineExceeded):
		return StatusTimeout
	}
	return StatusFailed
}
