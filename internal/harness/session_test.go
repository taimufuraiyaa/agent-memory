package harness_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
)

var scope = harness.Scope{Workspace: "agent-memory", Generation: 1}

func open(t *testing.T, registry *harness.Registry, id string, s harness.Scope, options ...harness.SessionOption) *harness.Session {
	t.Helper()
	session, err := registry.OpenSession(context.Background(), harness.ProviderID(id), s, options...)
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func register(t *testing.T, kind harness.ProviderKind, id string, b harnesstest.Behavior, capabilities ...string) (*harness.Registry, *harnesstest.Counters) {
	t.Helper()
	registry := harness.NewRegistry()
	counters, err := harnesstest.Register(registry, harnesstest.Manifest(id, kind, capabilities...), b)
	if err != nil {
		t.Fatal(err)
	}
	return registry, counters
}

func question(t *testing.T, s *harness.Session, capability string, candidates ...string) harness.DecisionQuestion {
	t.Helper()
	envelope, err := s.Envelope(harness.CapabilityID(capability), 64)
	if err != nil {
		t.Fatal(err)
	}
	return harness.DecisionQuestion{Envelope: envelope, Kind: capability, Candidates: candidates}
}

func TestSessionDecisionTypedOutcomesAndPayloadStripping(t *testing.T) {
	for _, tc := range []struct {
		name        string
		behavior    harnesstest.Behavior
		wantOutcome harness.Outcome
		wantPayload bool
		wantCalls   int32
	}{
		{"ok", harnesstest.Behavior{}, harness.OutcomeOK, true, 1},
		{"partial keeps payload", harnesstest.Behavior{Outcome: harness.OutcomePartial}, harness.OutcomePartial, true, 1},
		{"denied by provider", harnesstest.Behavior{Outcome: harness.OutcomeDenied}, harness.OutcomeDenied, false, 1},
		{"failed by provider", harnesstest.Behavior{Outcome: harness.OutcomeFailed}, harness.OutcomeFailed, false, 1},
		{"stale outcome from provider", harnesstest.Behavior{Outcome: harness.OutcomeStale}, harness.OutcomeStale, false, 1},
		{"unsupported capability never calls provider", harnesstest.Behavior{Access: harness.AccessUnsupported}, harness.OutcomeUnsupported, false, 0},
		{"denied capability never calls provider", harnesstest.Behavior{Access: harness.AccessDenied}, harness.OutcomeDenied, false, 0},
		{"unavailable capability never calls provider", harnesstest.Behavior{Access: harness.AccessUnavailable}, harness.OutcomeUnavailable, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, counters := register(t, harness.KindDecision, "fake-decision", tc.behavior, "visibility")
			s := open(t, registry, "fake-decision", scope)
			answer, err := s.Decide(context.Background(), question(t, s, "visibility", "short", "long"))
			if err != nil {
				t.Fatal(err)
			}
			if answer.Outcome != tc.wantOutcome || (len(answer.Selected) > 0) != tc.wantPayload || counters.Calls.Load() != tc.wantCalls {
				t.Fatalf("answer=%+v calls=%d", answer, counters.Calls.Load())
			}
			if !tc.wantPayload && answer.Confidence != 0 {
				t.Fatalf("non-result outcome leaked confidence: %+v", answer)
			}
		})
	}
}

func TestSessionRejectsStaleOversizedAndUnofferedReplies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		behavior harnesstest.Behavior
		want     error
	}{
		{"next generation reply", harnesstest.Behavior{StaleReply: true}, harness.ErrStale},
		{"oversized reply", harnesstest.Behavior{Oversize: true}, harness.ErrInvalid},
		{"choice outside candidates", harnesstest.Behavior{UnknownChoice: true}, harness.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, _ := register(t, harness.KindDecision, "fake-decision", tc.behavior, "visibility")
			s := open(t, registry, "fake-decision", scope)
			answer, err := s.Decide(context.Background(), question(t, s, "visibility", "short", "long"))
			if !errors.Is(err, tc.want) || len(answer.Selected) != 0 || answer.Outcome != "" {
				t.Fatalf("answer=%+v err=%v", answer, err)
			}
		})
	}
}

func TestSessionCancellationTimeoutAndDisposal(t *testing.T) {
	t.Run("caller cancel before call", func(t *testing.T) {
		registry, _ := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{Delay: time.Second}, "visibility")
		s := open(t, registry, "fake-decision", scope)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		answer, err := s.Decide(ctx, question(t, s, "visibility", "a"))
		if err != nil || answer.Outcome != harness.OutcomeCancelled {
			t.Fatalf("answer=%+v err=%v", answer, err)
		}
	})
	t.Run("provider ignoring context cannot hold the caller past the deadline", func(t *testing.T) {
		registry, _ := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{Delay: 400 * time.Millisecond, IgnoreContext: true}, "visibility")
		s := open(t, registry, "fake-decision", scope, harness.WithCallTimeout(30*time.Millisecond))
		start := time.Now()
		answer, err := s.Decide(context.Background(), question(t, s, "visibility", "a"))
		if err != nil || answer.Outcome != harness.OutcomeTimeout || time.Since(start) > 300*time.Millisecond {
			t.Fatalf("answer=%+v err=%v after %v", answer, err, time.Since(start))
		}
	})
	t.Run("close cancels in-flight calls and later use is stale", func(t *testing.T) {
		registry, counters := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{Delay: 2 * time.Second}, "visibility")
		s := open(t, registry, "fake-decision", scope)
		q := question(t, s, "visibility", "a")
		results := make(chan harness.DecisionAnswer, 1)
		go func() {
			answer, _ := s.Decide(context.Background(), q)
			results <- answer
		}()
		time.Sleep(50 * time.Millisecond)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case answer := <-results:
			if answer.Outcome != harness.OutcomeCancelled {
				t.Fatalf("in-flight answer = %+v", answer)
			}
		case <-time.After(time.Second):
			t.Fatal("close did not cancel the in-flight call")
		}
		if _, err := s.Decide(context.Background(), q); !errors.Is(err, harness.ErrClosed) || !errors.Is(err, harness.ErrStale) {
			t.Fatalf("closed session = %v", err)
		}
		_ = s.Close()
		if counters.Closes.Load() != 1 {
			t.Fatalf("provider closed %d times", counters.Closes.Load())
		}
	})
}

func TestSessionProviderFaultsBecomeTypedFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    harnesstest.Behavior
		want harness.Outcome
	}{
		{"panic", harnesstest.Behavior{Panic: true}, harness.OutcomeFailed},
		{"transport error", harnesstest.Behavior{Err: errors.New("secret-token-in-error")}, harness.OutcomeFailed},
		{"deadline error", harnesstest.Behavior{Err: context.DeadlineExceeded}, harness.OutcomeTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, _ := register(t, harness.KindDecision, "fake-decision", tc.b, "visibility")
			s := open(t, registry, "fake-decision", scope)
			answer, err := s.Decide(context.Background(), question(t, s, "visibility", "a"))
			if err != nil || answer.Outcome != tc.want {
				t.Fatalf("answer=%+v err=%v", answer, err)
			}
		})
	}
}

func TestRegistryOpenSessionFailuresAndGenerations(t *testing.T) {
	registry := harness.NewRegistry()
	healthy := harnesstest.Manifest("healthy", harness.KindDecision, "visibility")
	first, _ := harnesstest.Register(registry, healthy, harnesstest.Behavior{})
	probeErr := harnesstest.Manifest("probe-error", harness.KindDecision, "visibility")
	probeErrCounters, _ := harnesstest.Register(registry, probeErr, harnesstest.Behavior{ProbeErr: errors.New("boom")})
	probeStale := harnesstest.Manifest("probe-stale", harness.KindDecision, "visibility")
	probeStaleCounters, _ := harnesstest.Register(registry, probeStale, harnesstest.Behavior{ProbeStale: true})

	if _, err := registry.OpenSession(context.Background(), "missing", scope); !errors.Is(err, harness.ErrUnknown) {
		t.Fatalf("missing provider = %v", err)
	}
	if _, err := registry.OpenSession(context.Background(), "healthy", harness.Scope{}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("empty scope = %v", err)
	}
	if _, err := registry.OpenSession(context.Background(), "healthy", scope, harness.WithCallTimeout(0)); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("zero timeout = %v", err)
	}
	if _, err := registry.OpenSession(context.Background(), "probe-error", scope); !errors.Is(err, harness.ErrUnavailable) || strings.Contains(err.Error(), "boom") {
		t.Fatalf("probe error = %v", err)
	}
	if probeErrCounters.Closes.Load() != 1 {
		t.Fatal("failed probe left provider open")
	}
	if _, err := registry.OpenSession(context.Background(), "probe-stale", scope); !errors.Is(err, harness.ErrStale) || probeStaleCounters.Closes.Load() != 1 {
		t.Fatalf("stale probe = %v closes=%d", err, probeStaleCounters.Closes.Load())
	}

	one := open(t, registry, "healthy", harness.Scope{Workspace: "agent-memory", Generation: 1})
	other := open(t, registry, "healthy", harness.Scope{Workspace: "other-workspace", Generation: 1})
	two := open(t, registry, "healthy", harness.Scope{Workspace: "agent-memory", Generation: 2})
	if _, err := one.Decide(context.Background(), question(t, two, "visibility", "a")); !errors.Is(err, harness.ErrClosed) {
		t.Fatalf("superseded generation still served: %v", err)
	}
	if _, err := other.Decide(context.Background(), question(t, other, "visibility", "a")); err != nil {
		t.Fatalf("unrelated workspace was disposed: %v", err)
	}
	if _, err := registry.OpenSession(context.Background(), "healthy", harness.Scope{Workspace: "agent-memory", Generation: 1}); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("older generation reopened: %v", err)
	}
	if first.Closes.Load() != 1 {
		t.Fatalf("superseded provider closes = %d", first.Closes.Load())
	}
}

func TestSessionBoundsRequestsBeforeAnyProviderWork(t *testing.T) {
	registry, counters := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{}, "visibility")
	s := open(t, registry, "fake-decision", scope)
	envelope, err := s.Envelope("visibility", 64)
	if err != nil {
		t.Fatal(err)
	}
	many := make([]string, harness.MaxCandidates+1)
	for i := range many {
		many[i] = strings.Repeat("c", 5) + string(rune('a'+i%26)) + strings.Repeat("d", i)
	}
	refs := make([]harness.EvidenceRef, harness.MaxEvidenceRefs+1)
	for i := range refs {
		refs[i] = harness.EvidenceRef{ID: "chunk"}
	}
	for name, q := range map[string]harness.DecisionQuestion{
		"no candidates":       {Envelope: envelope, Kind: "visibility"},
		"too many":            {Envelope: envelope, Kind: "visibility", Candidates: many},
		"duplicate":           {Envelope: envelope, Kind: "visibility", Candidates: []string{"a", "a"}},
		"oversized":           {Envelope: envelope, Kind: "visibility", Candidates: []string{strings.Repeat("x", harness.MaxFieldBytes+1)}},
		"control character":   {Envelope: envelope, Kind: "visibility", Candidates: []string{"a\x00b"}},
		"invalid UTF-8":       {Envelope: envelope, Kind: "visibility", Candidates: []string{"\xff\xfe"}},
		"bad kind":            {Envelope: envelope, Kind: "Not A Kind", Candidates: []string{"a"}},
		"too much evidence":   {Envelope: envelope, Kind: "visibility", Candidates: []string{"a"}, Evidence: refs},
		"evidence without ID": {Envelope: envelope, Kind: "visibility", Candidates: []string{"a"}, Evidence: []harness.EvidenceRef{{}}},
	} {
		if _, err := s.Decide(context.Background(), q); !errors.Is(err, harness.ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	if _, err := s.Envelope("undeclared", 64); !errors.Is(err, harness.ErrInvalid) {
		t.Errorf("undeclared capability envelope = %v", err)
	}
	if _, err := s.Envelope("visibility", 0); !errors.Is(err, harness.ErrInvalid) {
		t.Errorf("zero reply bound = %v", err)
	}
	forged := envelope
	forged.AccessRevision++
	if _, err := s.Decide(context.Background(), harness.DecisionQuestion{Envelope: forged, Kind: "visibility", Candidates: []string{"a"}}); !errors.Is(err, harness.ErrStale) {
		t.Errorf("forged revision = %v", err)
	}
	if counters.Calls.Load() != 0 {
		t.Fatalf("provider was called %d times for invalid requests", counters.Calls.Load())
	}
}

func TestSessionModelRequestsAndToolOffers(t *testing.T) {
	registry, _ := register(t, harness.KindModel, "fake-model", harnesstest.Behavior{RequestTool: "read"}, "generation")
	s := open(t, registry, "fake-model", scope)
	envelope, err := s.Envelope("generation", 128)
	if err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]harness.ModelRequest{
		"zero output":     {Envelope: envelope, MaxOutputTokens: 0},
		"huge output":     {Envelope: envelope, MaxOutputTokens: harness.MaxOutputTokenLimit + 1},
		"bad schema ID":   {Envelope: envelope, MaxOutputTokens: 8, ToolSchemaIDs: []string{"Bad ID"}},
		"duplicate tools": {Envelope: envelope, MaxOutputTokens: 8, ToolSchemaIDs: []string{"read", "read"}},
	} {
		if _, err := s.Generate(context.Background(), q); !errors.Is(err, harness.ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	if _, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("tool call that was never offered = %v", err)
	}
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8, ToolSchemaIDs: []string{"read"}})
	if err != nil || answer.Outcome != harness.OutcomeOK || answer.ToolID != "read" || answer.Text == "" {
		t.Fatalf("offered tool call = %+v, %v", answer, err)
	}
	if _, err := s.Decide(context.Background(), harness.DecisionQuestion{Envelope: envelope, Kind: "generation", Candidates: []string{"a"}}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("decision on a model session = %v", err)
	}
}

func TestSessionToolPreparationIsSingleUseAndDigestBound(t *testing.T) {
	registry, counters := register(t, harness.KindTool, "fake-tool", harnesstest.Behavior{}, "read")
	s := open(t, registry, "fake-tool", scope)
	envelope, _ := s.Envelope("read", 64)
	request := harness.ToolRequest{Envelope: envelope, ToolID: "read", Arguments: []byte(`{"path":"a.go"}`)}

	action, err := s.Prepare(context.Background(), request)
	if err != nil || action.Outcome != harness.OutcomeOK || action.Digest == "" {
		t.Fatalf("prepare = %+v, %v", action, err)
	}
	if counters.Invokes.Load() != 0 {
		t.Fatal("prepare executed the action")
	}
	forged := action
	forged.Digest = "forged-digest"
	if _, err := s.Invoke(context.Background(), forged); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("forged digest = %v", err)
	}
	wrongEnvelope := action
	wrongEnvelope.MaxBytes++
	if _, err := s.Invoke(context.Background(), wrongEnvelope); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("altered envelope = %v", err)
	}
	if counters.Invokes.Load() != 0 {
		t.Fatal("rejected action reached the provider")
	}
	answer, err := s.Invoke(context.Background(), action)
	if err != nil || answer.Outcome != harness.OutcomeOK || string(answer.Output) != "tool output" {
		t.Fatalf("invoke = %+v, %v", answer, err)
	}
	if _, err := s.Invoke(context.Background(), action); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("replay = %v", err)
	}
	if counters.Invokes.Load() != 1 {
		t.Fatalf("invokes = %d", counters.Invokes.Load())
	}
	if _, err := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: "Bad Tool"}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("bad tool ID = %v", err)
	}
	if _, err := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: "read", Arguments: make([]byte, harness.MaxPayloadBytes+1)}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("oversized arguments = %v", err)
	}
}

func TestSessionToolFailuresNeverYieldInvokableOrLeakedPayload(t *testing.T) {
	for name, b := range map[string]harnesstest.Behavior{
		"denied":  {Outcome: harness.OutcomeDenied},
		"partial": {Outcome: harness.OutcomePartial},
	} {
		registry, _ := register(t, harness.KindTool, "fake-tool", b, "read")
		s := open(t, registry, "fake-tool", scope)
		envelope, _ := s.Envelope("read", 64)
		action, err := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: "read"})
		if err != nil || action.Digest != "" {
			t.Fatalf("%s prepare = %+v, %v", name, action, err)
		}
		if _, err := s.Invoke(context.Background(), action); !errors.Is(err, harness.ErrInvalid) {
			t.Fatalf("%s invoked an unprepared action: %v", name, err)
		}
	}
	registry, _ := register(t, harness.KindTool, "fake-tool", harnesstest.Behavior{Digest: "x"}, "read")
	s := open(t, registry, "fake-tool", scope)
	envelope, _ := s.Envelope("read", 64)
	if _, err := s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: "read"}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("weak digest accepted: %v", err)
	}
}

func TestSessionBoundsOutstandingPreparedActions(t *testing.T) {
	registry := harness.NewRegistry()
	manifest := harnesstest.Manifest("fake-tool", harness.KindTool, "read")
	if err := registry.Register(manifest, func() (harness.Provider, error) { return &countingTool{}, nil }); err != nil {
		t.Fatal(err)
	}
	s := open(t, registry, "fake-tool", scope)
	envelope, _ := s.Envelope("read", 64)
	var last error
	for i := 0; i < harness.MaxOutstandingPrepared+1; i++ {
		_, last = s.Prepare(context.Background(), harness.ToolRequest{Envelope: envelope, ToolID: "read"})
	}
	if !errors.Is(last, harness.ErrInvalid) {
		t.Fatalf("outstanding bound not enforced: %v", last)
	}
}

// countingTool is a minimal independent tool family: every preparation has a unique digest.
type countingTool struct {
	mu sync.Mutex
	n  int
}

func (c *countingTool) Probe(_ context.Context, sc harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "fake-tool", Scope: sc, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"read": harness.AccessAvailable}}, nil
}
func (c *countingTool) Close() error { return nil }
func (c *countingTool) Prepare(_ context.Context, q harness.ToolRequest) (harness.PreparedAction, error) {
	c.mu.Lock()
	c.n++
	n := c.n
	c.mu.Unlock()
	return harness.PreparedAction{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Digest: fmt.Sprintf("digest-%06d", n), Summary: "s"}, nil
}
func (c *countingTool) Invoke(_ context.Context, a harness.PreparedAction) (harness.ToolAnswer, error) {
	return harness.ToolAnswer{Envelope: a.Envelope, Outcome: harness.OutcomeOK}, nil
}

// altDecision is a second, differently shaped decision family: it serves two
// capabilities and always picks the last candidate. It is registered next to the
// scripted fakes with no change to the shared registry or session code.
type altDecision struct{}

func (altDecision) Probe(_ context.Context, sc harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "alt-family", Scope: sc, Revision: 7,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"tool-rank": harness.AccessAvailable, "cache-strategy": harness.AccessUnsupported}}, nil
}
func (altDecision) Close() error { return nil }
func (altDecision) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Selected: []string{q.Candidates[len(q.Candidates)-1]}, Confidence: 0.6}, nil
}

func TestSecondFamilySharesWorkflowWithoutChangingRegistry(t *testing.T) {
	registry := harness.NewRegistry()
	if _, err := harnesstest.Register(registry, harnesstest.Manifest("scripted-family", harness.KindDecision, "tool-rank", "cache-strategy"), harnesstest.Behavior{}); err != nil {
		t.Fatal(err)
	}
	alt := harnesstest.Manifest("alt-family", harness.KindDecision, "tool-rank", "cache-strategy")
	if err := registry.Register(alt, func() (harness.Provider, error) { return altDecision{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(alt, func() (harness.Provider, error) { return altDecision{}, nil }); !errors.Is(err, harness.ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
	// One workflow, written against sessions only, serves both families.
	choose := func(id string) (string, harness.Outcome) {
		s := open(t, registry, id, harness.Scope{Workspace: "agent-memory", Generation: 3})
		answer, err := s.Decide(context.Background(), question(t, s, "tool-rank", "grep", "read"))
		if err != nil || len(answer.Selected) != 1 {
			t.Fatalf("%s: %+v, %v", id, answer, err)
		}
		unsupported, err := s.Decide(context.Background(), question(t, s, "cache-strategy", "reuse"))
		if err != nil {
			t.Fatal(err)
		}
		return answer.Selected[0], unsupported.Outcome
	}
	if choice, cache := choose("scripted-family"); choice != "grep" || cache != harness.OutcomeOK {
		t.Fatalf("scripted family = %s %s", choice, cache)
	}
	if choice, cache := choose("alt-family"); choice != "read" || cache != harness.OutcomeUnsupported {
		t.Fatalf("alt family = %s %s", choice, cache)
	}
	if got := registry.Manifests(); len(got) != 2 || got[0].ID != "alt-family" || got[1].ID != "scripted-family" {
		t.Fatalf("manifests = %+v", got)
	}
}

func TestSessionIsSafeForConcurrentUse(t *testing.T) {
	registry, _ := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{Delay: 5 * time.Millisecond}, "visibility")
	s := open(t, registry, "fake-decision", scope)
	q := question(t, s, "visibility", "a", "b")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i == 16 {
				_ = s.Close()
			}
			_, _ = s.Decide(context.Background(), q)
		}(i)
	}
	wg.Wait()
}

func TestSessionAccessorsReturnCopies(t *testing.T) {
	registry, _ := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{}, "visibility")
	s := open(t, registry, "fake-decision", scope)
	if s.Scope() != scope || s.Manifest().ID != "fake-decision" {
		t.Fatalf("identity = %+v %+v", s.Scope(), s.Manifest())
	}
	access := s.Access()
	access.Capabilities["visibility"] = harness.AccessDenied
	delete(access.Capabilities, "visibility")
	manifest := s.Manifest()
	manifest.Capabilities[0] = "tampered"
	if s.State("visibility") != harness.AccessAvailable || s.Manifest().Capabilities[0] != "visibility" {
		t.Fatal("caller mutation reached the session")
	}
	if s.State("absent") != harness.AccessUnavailable {
		t.Fatalf("absent capability = %s", s.State("absent"))
	}
}

func TestRunsInOneWorkspaceKeepIndependentSessions(t *testing.T) {
	registry, counters := register(t, harness.KindDecision, "fake-decision", harnesstest.Behavior{}, "visibility")
	runA := open(t, registry, "fake-decision", harness.Scope{Workspace: "agent-memory", Run: "run-a", Generation: 1})
	runB := open(t, registry, "fake-decision", harness.Scope{Workspace: "agent-memory", Run: "run-b", Generation: 1})
	if _, err := runA.Decide(context.Background(), question(t, runA, "visibility", "a")); err != nil {
		t.Fatalf("opening another run disposed this one: %v", err)
	}
	if _, err := runB.Decide(context.Background(), question(t, runB, "visibility", "a")); err != nil {
		t.Fatal(err)
	}
	// A newer generation of the same run still supersedes only that run.
	_ = open(t, registry, "fake-decision", harness.Scope{Workspace: "agent-memory", Run: "run-a", Generation: 2})
	if _, err := runA.Decide(context.Background(), question(t, runA, "visibility", "a")); !errors.Is(err, harness.ErrClosed) {
		t.Fatalf("superseded run generation = %v", err)
	}
	if _, err := runB.Decide(context.Background(), question(t, runB, "visibility", "a")); err != nil {
		t.Fatalf("unrelated run was disposed: %v", err)
	}
	if counters.Closes.Load() != 1 {
		t.Fatalf("closes = %d", counters.Closes.Load())
	}
	if _, err := registry.OpenSession(context.Background(), "fake-decision", harness.Scope{Workspace: "agent-memory", Run: "Bad Run", Generation: 1}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("invalid run ID = %v", err)
	}
}

func TestModelReplyToolArgumentsAndCostAreValidated(t *testing.T) {
	for name, tc := range map[string]struct {
		behavior harnesstest.Behavior
		offered  []string
		wantErr  bool
	}{
		"arguments with tool":    {harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Arguments: `{"p":1}`}}}, []string{"read"}, false},
		"arguments without tool": {harnesstest.Behavior{Script: []harnesstest.Step{{Text: "x", Arguments: `{"p":1}`}}}, nil, true},
		"oversized arguments":    {harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Arguments: strings.Repeat("a", 200)}}}, []string{"read"}, true},
		"reported cost":          {harnesstest.Behavior{CostMicros: 1500}, nil, false},
		"negative cost":          {harnesstest.Behavior{CostMicros: -1}, nil, true},
		"absurd cost":            {harnesstest.Behavior{CostMicros: harness.MaxCostMicros + 1}, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			registry, _ := register(t, harness.KindModel, "fake-model", tc.behavior, "generation")
			s := open(t, registry, "fake-model", scope)
			envelope, _ := s.Envelope("generation", 128)
			answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8, ToolSchemaIDs: tc.offered})
			if (err != nil) != tc.wantErr {
				t.Fatalf("answer=%+v err=%v", answer, err)
			}
			if err == nil && tc.behavior.CostMicros != 0 && answer.CostMicros != tc.behavior.CostMicros {
				t.Fatalf("cost = %d", answer.CostMicros)
			}
		})
	}
	// Cost survives a failed outcome even though the payload is stripped.
	registry, _ := register(t, harness.KindModel, "fake-model", harnesstest.Behavior{Outcome: harness.OutcomeFailed, CostMicros: 700, Script: []harnesstest.Step{{Text: "secret", ToolID: "read", Arguments: "{}"}}}, "generation")
	s := open(t, registry, "fake-model", scope)
	envelope, _ := s.Envelope("generation", 128)
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8, ToolSchemaIDs: []string{"read"}})
	if err != nil || answer.Text != "" || answer.ToolID != "" || len(answer.ToolArguments) != 0 {
		t.Fatalf("payload leaked on failure: %+v, %v", answer, err)
	}
	if answer.CostMicros != 700 {
		t.Fatalf("cost of a failed call was dropped: %d", answer.CostMicros)
	}
}

func TestDiscardReleasesAPreparedHandle(t *testing.T) {
	registry, counters := register(t, harness.KindTool, "fake-tool", harnesstest.Behavior{}, "read")
	s := open(t, registry, "fake-tool", scope)
	envelope, _ := s.Envelope("read", 64)
	request := harness.ToolRequest{Envelope: envelope, ToolID: "read"}
	first, err := s.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prepare(context.Background(), request); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("duplicate outstanding digest = %v", err)
	}
	s.Discard(first)
	s.Discard(first) // idempotent
	if _, err := s.Invoke(context.Background(), first); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("discarded handle invoked: %v", err)
	}
	if counters.Invokes.Load() != 0 {
		t.Fatal("discard reached the provider")
	}
	if _, err := s.Prepare(context.Background(), request); err != nil {
		t.Fatalf("re-preparing after discard = %v", err)
	}
}

func TestModelRequestPromptAndPrefixAreBoundedAndDeliveredIntact(t *testing.T) {
	registry, counters := register(t, harness.KindModel, "fake-model", harnesstest.Behavior{Script: []harnesstest.Step{{Text: "ok"}}}, "generation")
	s := open(t, registry, "fake-model", scope)
	envelope, _ := s.Envelope("generation", 128)
	base := harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8}
	for name, mutate := range map[string]func(*harness.ModelRequest){
		"oversized prompt": func(r *harness.ModelRequest) { r.Prompt = strings.Repeat("p", harness.MaxPromptBytes+1) },
		"invalid UTF-8":    func(r *harness.ModelRequest) { r.Prompt = "ok \xff\xfe" },
		"short prefix":     func(r *harness.ModelRequest) { r.PrefixID = "abc" },
		"non-hex prefix":   func(r *harness.ModelRequest) { r.PrefixID = strings.Repeat("g", 32) },
		"oversized prefix": func(r *harness.ModelRequest) { r.PrefixID = strings.Repeat("a", 65) },
		"uppercase prefix": func(r *harness.ModelRequest) { r.PrefixID = strings.Repeat("A", 32) },
	} {
		r := base
		mutate(&r)
		if _, err := s.Generate(context.Background(), r); !errors.Is(err, harness.ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	if counters.Calls.Load() != 0 {
		t.Fatal("an invalid request reached the provider")
	}
	good := base
	good.Prompt, good.PrefixID = "assembled prompt text", strings.Repeat("ab", 16)
	if _, err := s.Generate(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	received := counters.Requests.Load().(harness.ModelRequest)
	if received.Prompt != good.Prompt || received.PrefixID != good.PrefixID {
		t.Fatalf("the provider received %+v", received)
	}
	max := base
	max.Prompt = strings.Repeat("p", harness.MaxPromptBytes)
	if _, err := s.Generate(context.Background(), max); err != nil {
		t.Fatalf("a prompt exactly at the bound = %v", err)
	}
}

func TestReportedUsageAndModelAreValidatedAndKeptOnFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		behavior harnesstest.Behavior
		wantErr  bool
	}{
		"plain usage":       {harnesstest.Behavior{Usage: harness.Usage{InputTokens: 100, OutputTokens: 20}, ModelName: "gpt-x"}, false},
		"cached within":     {harnesstest.Behavior{Usage: harness.Usage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 100}}, false},
		"cached over input": {harnesstest.Behavior{Usage: harness.Usage{InputTokens: 10, CachedInputTokens: 11}}, true},
		"negative input":    {harnesstest.Behavior{Usage: harness.Usage{InputTokens: -1}}, true},
		"negative output":   {harnesstest.Behavior{Usage: harness.Usage{OutputTokens: -1}}, true},
		"absurd input":      {harnesstest.Behavior{Usage: harness.Usage{InputTokens: harness.MaxUsageTokens + 1}}, true},
		"absurd output":     {harnesstest.Behavior{Usage: harness.Usage{OutputTokens: harness.MaxUsageTokens + 1}}, true},
		"control in model":  {harnesstest.Behavior{ModelName: "bad\nmodel"}, true},
		"oversized model":   {harnesstest.Behavior{ModelName: strings.Repeat("m", 300)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			registry, _ := register(t, harness.KindModel, "fake-model", tc.behavior, "generation")
			s := open(t, registry, "fake-model", scope)
			envelope, _ := s.Envelope("generation", 128)
			answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8})
			if (err != nil) != tc.wantErr {
				t.Fatalf("answer=%+v err=%v", answer, err)
			}
			if err == nil && answer.Usage != tc.behavior.Usage {
				t.Fatalf("usage = %+v", answer.Usage)
			}
		})
	}
	// A failed call still reports what it consumed.
	registry, _ := register(t, harness.KindModel, "fake-model", harnesstest.Behavior{Outcome: harness.OutcomeFailed, Usage: harness.Usage{InputTokens: 50, OutputTokens: 5}, ModelName: "gpt-x", Script: []harnesstest.Step{{Text: "hidden"}}}, "generation")
	s := open(t, registry, "fake-model", scope)
	envelope, _ := s.Envelope("generation", 128)
	answer, err := s.Generate(context.Background(), harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 8})
	if err != nil || answer.Text != "" || answer.Usage.InputTokens != 50 || answer.Model != "gpt-x" {
		t.Fatalf("failed call = %+v, %v", answer, err)
	}
}
