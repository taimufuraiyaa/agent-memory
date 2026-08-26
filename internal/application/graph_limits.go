package application

import (
	"fmt"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphLimits struct {
	BatchChanges         int
	MaxWait              time.Duration
	MaxChanges           int
	MaxProjectionBytes   int64
	MaxEstimatedCostUSD  float64
	MaxTenantConcurrency int
	MaxGlobalConcurrency int
	MinLease             time.Duration
	MaxLease             time.Duration
	MaxAttempts          int
}

func DefaultGraphLimits() GraphLimits {
	return GraphLimits{
		BatchChanges: 50, MaxWait: 15 * time.Minute, MaxChanges: 5000,
		MaxProjectionBytes: 512 << 20, MaxEstimatedCostUSD: 100,
		MaxTenantConcurrency: 2, MaxGlobalConcurrency: 20,
		MinLease: 30 * time.Second, MaxLease: 15 * time.Minute, MaxAttempts: 5,
	}
}

func (l GraphLimits) Validate() error {
	if l.BatchChanges < 1 || l.BatchChanges > l.MaxChanges || l.MaxWait < time.Minute || l.MaxWait > 24*time.Hour ||
		l.MaxProjectionBytes < 1 || l.MaxEstimatedCostUSD <= 0 || l.MaxTenantConcurrency < 1 ||
		l.MaxGlobalConcurrency < l.MaxTenantConcurrency || l.MinLease <= 0 || l.MaxLease < l.MinLease ||
		l.MaxAttempts < 1 || l.MaxAttempts > 20 {
		return fmt.Errorf("invalid graph production limits")
	}
	return nil
}

type GraphWorkEstimate struct {
	Changes          int
	ProjectionBytes  int64
	EstimatedCostUSD float64
	TenantRunning    int
	GlobalRunning    int
}

type GraphAdmissionDecision struct {
	Allowed bool
	Code    string
}

func (l GraphLimits) Admit(work GraphWorkEstimate) GraphAdmissionDecision {
	switch {
	case work.Changes < 1:
		return GraphAdmissionDecision{Code: "no_changes"}
	case work.Changes > l.MaxChanges:
		return GraphAdmissionDecision{Code: "change_limit_exceeded"}
	case work.ProjectionBytes > l.MaxProjectionBytes:
		return GraphAdmissionDecision{Code: "projection_too_large"}
	case work.EstimatedCostUSD > l.MaxEstimatedCostUSD:
		return GraphAdmissionDecision{Code: "estimated_cost_exceeded"}
	case work.TenantRunning >= l.MaxTenantConcurrency:
		return GraphAdmissionDecision{Code: "tenant_concurrency_exhausted"}
	case work.GlobalRunning >= l.MaxGlobalConcurrency:
		return GraphAdmissionDecision{Code: "global_concurrency_exhausted"}
	default:
		return GraphAdmissionDecision{Allowed: true, Code: "admitted"}
	}
}

func (l GraphLimits) BoundLease(requested time.Duration) time.Duration {
	if requested < l.MinLease {
		return l.MinLease
	}
	if requested > l.MaxLease {
		return l.MaxLease
	}
	return requested
}

func (l GraphLimits) FailureState(attempt int) core.GraphJobState {
	if attempt >= l.MaxAttempts {
		return core.GraphJobDeadLetter
	}
	return core.GraphJobFailed
}

type GraphFreshness struct {
	Lag             time.Duration `json:"lag"`
	WithinP95Target bool          `json:"within_p95_target"`
	WithinP99Target bool          `json:"within_p99_target"`
	Stale           bool          `json:"stale"`
}

func EvaluateGraphFreshness(activeCutoff, now time.Time) GraphFreshness {
	if now.Before(activeCutoff) {
		now = activeCutoff
	}
	lag := now.Sub(activeCutoff)
	return GraphFreshness{Lag: lag, WithinP95Target: lag <= 30*time.Minute, WithinP99Target: lag <= 2*time.Hour, Stale: lag > 2*time.Hour}
}
