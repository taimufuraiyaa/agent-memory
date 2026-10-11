package application

import (
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphBackpressureRejectsUnsafeWorkAndBoundsLease(t *testing.T) {
	limits := DefaultGraphLimits()
	if err := limits.Validate(); err != nil {
		t.Fatal(err)
	}
	decision := limits.Admit(GraphWorkEstimate{Changes: 51, ProjectionBytes: limits.MaxProjectionBytes + 1})
	if decision.Allowed || decision.Code != "projection_too_large" {
		t.Fatalf("decision=%+v", decision)
	}
	if got := limits.BoundLease(time.Second); got != limits.MinLease {
		t.Fatalf("short lease bounded to %s, want %s", got, limits.MinLease)
	}
	if got := limits.BoundLease(24 * time.Hour); got != limits.MaxLease {
		t.Fatalf("long lease bounded to %s, want %s", got, limits.MaxLease)
	}
	if state := limits.FailureState(limits.MaxAttempts); state != core.GraphJobDeadLetter {
		t.Fatalf("terminal failure state=%s", state)
	}
}

func TestGraphFreshnessReportsProductionTargets(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	fresh := EvaluateGraphFreshness(now.Add(-20*time.Minute), now)
	if !fresh.WithinP95Target || !fresh.WithinP99Target || fresh.Stale {
		t.Fatalf("freshness=%+v", fresh)
	}
	stale := EvaluateGraphFreshness(now.Add(-3*time.Hour), now)
	if stale.WithinP95Target || stale.WithinP99Target || !stale.Stale {
		t.Fatalf("stale freshness=%+v", stale)
	}
}
