package harnessdecide

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
)

// openSession registers a fake decision provider with the given behavior in a real registry
// and opens a real session on it, so the service is exercised against the session's own
// gating, validation and panic containment as well as its own.
func openSession(t *testing.T, behavior harnesstest.Behavior) (*harness.Session, *harnesstest.Counters) {
	t.Helper()
	registry := harness.NewRegistry()
	counters, err := harnesstest.Register(registry, harnesstest.Manifest("fake-decision", harness.KindDecision, "decide"), behavior)
	if err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "fake-decision", harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, counters
}

func realService(t *testing.T, session *harness.Session, tweak ...func(*Config)) *Service {
	t.Helper()
	cfg := Config{Asker: session, Capability: "decide", MaxClass: ClassInternal, Enabled: Kinds()}
	for _, fn := range tweak {
		fn(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAHealthyProviderThroughARealSessionAnswersEveryKind(t *testing.T) {
	session, counters := openSession(t, harnesstest.Behavior{})
	s := realService(t, session)
	for _, p := range probes() {
		got, out := p.run(s)
		if out.Status != StatusApplied || got == "" || out.Confidence != 0.9 {
			t.Errorf("%s: %q %+v", p.kind, got, out)
		}
	}
	if counters.Calls.Load() != int32(len(probes())) {
		t.Fatalf("calls = %d", counters.Calls.Load())
	}
}

// The provider matrix: every way the fake provider family can misbehave, for every kind. In
// each case the caller gets no advice and a status that says why, and nothing panics.
func TestEveryProviderMisbehaviorForEveryKindLeavesTheCallersChoiceInPlace(t *testing.T) {
	matrix := map[string]struct {
		behavior harnesstest.Behavior
		want     map[Status]bool
	}{
		"unavailable at probe":   {harnesstest.Behavior{Access: harness.AccessUnavailable}, map[Status]bool{StatusUnavailable: true, StatusFailed: true}},
		"denied at probe":        {harnesstest.Behavior{Access: harness.AccessDenied}, map[Status]bool{StatusDenied: true, StatusFailed: true}},
		"unsupported at probe":   {harnesstest.Behavior{Access: harness.AccessUnsupported}, map[Status]bool{StatusUnavailable: true, StatusFailed: true}},
		"a denied reply":         {harnesstest.Behavior{Outcome: harness.OutcomeDenied}, map[Status]bool{StatusDenied: true}},
		"an unavailable reply":   {harnesstest.Behavior{Outcome: harness.OutcomeUnavailable}, map[Status]bool{StatusUnavailable: true}},
		"a failed reply":         {harnesstest.Behavior{Outcome: harness.OutcomeFailed}, map[Status]bool{StatusFailed: true}},
		"a partial reply":        {harnesstest.Behavior{Outcome: harness.OutcomePartial}, map[Status]bool{StatusFailed: true}},
		"a timed out reply":      {harnesstest.Behavior{Outcome: harness.OutcomeTimeout}, map[Status]bool{StatusTimeout: true}},
		"a stale reply outcome":  {harnesstest.Behavior{Outcome: harness.OutcomeStale}, map[Status]bool{StatusStale: true}},
		"a stale envelope":       {harnesstest.Behavior{StaleReply: true}, map[Status]bool{StatusStale: true, StatusFailed: true, StatusInvalid: true}},
		"a transport error":      {harnesstest.Behavior{Err: errors.New("connection reset")}, map[Status]bool{StatusFailed: true, StatusUnavailable: true}},
		"a panic":                {harnesstest.Behavior{Panic: true}, map[Status]bool{StatusFailed: true, StatusUnavailable: true}},
		"an unknown choice":      {harnesstest.Behavior{UnknownChoice: true}, map[Status]bool{StatusInvalid: true}},
		"a reply over its bound": {harnesstest.Behavior{Oversize: true}, map[Status]bool{StatusInvalid: true}},
	}
	for name, tc := range matrix {
		for _, p := range probes() {
			session, _ := openSession(t, tc.behavior)
			s := realService(t, session)
			got, out := p.run(s)
			if got != "" || out.Status == StatusApplied || !tc.want[out.Status] {
				t.Errorf("%s / %s: %q %+v", name, p.kind, got, out)
			}
		}
	}
}

func TestASlowProviderThatIgnoresItsContextCostsOneDeadlineThroughARealSession(t *testing.T) {
	session, counters := openSession(t, harnesstest.Behavior{Delay: 10 * time.Second, IgnoreContext: true})
	s := realService(t, session)
	begin := time.Now()
	got, out := s.CommandRisk(context.Background(), "run_command", Facts{"n": 1})
	spec, _ := SpecOf(KindCommandRisk)
	if got != "" || out.Status != StatusTimeout || time.Since(begin) > spec.Deadline+time.Second {
		t.Fatalf("%q %+v after %v", got, out, time.Since(begin))
	}
	if counters.Calls.Load() != 1 {
		t.Fatalf("calls = %d", counters.Calls.Load())
	}
}

func TestAClosedSessionIsAStaleProviderNotAnAnswer(t *testing.T) {
	session, _ := openSession(t, harnesstest.Behavior{})
	s := realService(t, session)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range probes() {
		if got, out := p.run(s); got != "" || (out.Status != StatusCancelled && out.Status != StatusStale && out.Status != StatusFailed) {
			t.Errorf("%s on a closed session: %q %+v", p.kind, got, out)
		}
	}
}
