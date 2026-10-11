package harnessmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

const workspace = "agent-memory"

var runOwner = harnessrun.Owner{ClientID: "claude-desktop", Workspace: workspace, GrantID: strings.Repeat("b", 32), GrantRevision: 1}

// fixedContext is a ContextSource returning a fixed prompt, class and prefix identity.
type fixedContext struct {
	pc  harnessrun.PromptContext
	err error
}

func (f fixedContext) Context(context.Context, harnessrun.Snapshot) (harnessrun.PromptContext, error) {
	return f.pc, f.err
}

func promptContext(class int, prefix string) harnessrun.PromptContext {
	return harnessrun.PromptContext{Refs: []harness.EvidenceRef{{ID: "goal", Revision: "1", Class: "run"}}, Prompt: "assembled prompt for the model",
		PrefixID: prefix, Class: class, InputTokens: 1000}
}

type provider struct {
	id       string
	pricing  Pricing
	class    Class
	behavior harnesstest.Behavior
}

type routed struct {
	t        *testing.T
	manager  *harnessrun.Manager
	meter    *Meter
	router   *Router
	counters map[string]*harnesstest.Counters
	prices   map[harness.ProviderID]Pricing
}

func newRouted(t *testing.T, providers []provider, source harnessrun.ContextSource, advisor Advisor, edit func(*harnessrun.Config)) *routed {
	t.Helper()
	registry := harness.NewRegistry()
	r := &routed{t: t, counters: map[string]*harnesstest.Counters{}, prices: map[harness.ProviderID]Pricing{}, meter: NewMeter(nil)}
	var profiles []Profile
	for _, p := range providers {
		counters, err := harnesstest.Register(registry, harnesstest.Manifest(p.id, harness.KindModel, "generation"), p.behavior)
		if err != nil {
			t.Fatal(err)
		}
		r.counters[p.id] = counters
		r.prices[harness.ProviderID(p.id)] = p.pricing
		profiles = append(profiles, Profile{Provider: harness.ProviderID(p.id), Capability: "generation", Pricing: p.pricing, MaxClass: p.class, ContextTokens: 100_000, MaxOutputTokens: 4000})
	}
	router, err := NewRouter(RouterConfig{Profiles: profiles, Prober: &RegistryProber{Registry: registry, Workspace: workspace}, Meter: r.meter, Advisor: advisor, AdviceTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	r.router = router
	cfg := harnessrun.Config{DataDir: t.TempDir(), Registry: registry, Model: harnessrun.Binding{Provider: harness.ProviderID(providers[0].id), Capability: "generation"},
		Router: LoopRouter{Router: router, Meter: r.meter}, Context: source}
	if edit != nil {
		edit(&cfg)
	}
	manager, err := harnessrun.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	r.manager = manager
	return r
}

func (r *routed) run(key string, budget harnessrun.Budget) harnessrun.Status {
	r.t.Helper()
	started, err := r.manager.Start(context.Background(), runOwner, harnessrun.StartRequest{IdempotencyKey: key, Goal: "route this run", Budget: budget})
	if err != nil {
		r.t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		status, err := r.manager.Status(context.Background(), runOwner, started.ID)
		if err != nil {
			r.t.Fatal(err)
		}
		if status.State.Terminal() {
			return status
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *routed) codes(id string) []string {
	r.t.Helper()
	var codes []string
	cursor := ""
	for {
		page, err := r.manager.Events(context.Background(), runOwner, id, cursor, 100)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, e := range page.Events {
			codes = append(codes, e.Kind+":"+e.Code)
		}
		if len(page.Events) == 0 {
			return codes
		}
		cursor = page.Next
	}
}

var done = []harnesstest.Step{{Text: "finished"}}

func TestTheRouterPicksTheCheapestEligibleProviderAndTheRunRecordsItsUsage(t *testing.T) {
	usage := harness.Usage{InputTokens: 1000, OutputTokens: 100}
	cheap := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 4_000_000}
	dear := Pricing{InputPerMTok: 5_000_000, CachedInputPerMTok: 500_000, OutputPerMTok: 20_000_000}
	r := newRouted(t, []provider{
		{"model-dear", dear, ClassInternal, harnesstest.Behavior{Script: done, Usage: usage, CostMicros: dear.Cost(usage)}},
		{"model-cheap", cheap, ClassInternal, harnesstest.Behavior{Script: done, Usage: usage, CostMicros: cheap.Cost(usage)}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	status := r.run("key-00000001", harnessrun.Budget{})
	if status.State != harnessrun.StateCompleted || r.counters["model-cheap"].Calls.Load() != 1 || r.counters["model-dear"].Calls.Load() != 0 {
		t.Fatalf("status=%+v cheap=%d dear=%d", status, r.counters["model-cheap"].Calls.Load(), r.counters["model-dear"].Calls.Load())
	}
	if status.Usage.InputTokens != 1000 || status.Usage.OutputTokens != 100 || status.Usage.SpendMicros != cheap.Cost(usage) {
		t.Fatalf("usage = %+v", status.Usage)
	}
	if s := r.meter.Stats("model-cheap"); s.Calls != 1 || s.CostMicros != cheap.Cost(usage) || s.LatencySamples != 1 {
		t.Fatalf("the router did not learn from the call: %+v", s)
	}
}

func TestThePromptAndPrefixIdentityReachTheProviderIntact(t *testing.T) {
	const prefix = "0123456789abcdef0123456789abcdef"
	r := newRouted(t, []provider{{"model-a", Pricing{InputPerMTok: 1, CachedInputPerMTok: 1}, ClassInternal, harnesstest.Behavior{Script: done}}},
		fixedContext{pc: promptContext(int(ClassInternal), prefix)}, nil, nil)
	if status := r.run("key-00000001", harnessrun.Budget{}); status.State != harnessrun.StateCompleted {
		t.Fatalf("status = %+v", status)
	}
	request := r.counters["model-a"].Requests.Load().(harness.ModelRequest)
	if request.Prompt != "assembled prompt for the model" || request.PrefixID != prefix || len(request.InputRefs) != 1 {
		t.Fatalf("request = %+v", request)
	}
}

func TestRestrictedDataNeverReachesAProviderNotEligibleForIt(t *testing.T) {
	p := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	// Only an internal-class cloud provider exists: restricted data has nowhere to go.
	cloudOnly := newRouted(t, []provider{{"cloud", p, ClassInternal, harnesstest.Behavior{Script: done}}},
		fixedContext{pc: promptContext(int(ClassRestricted), "")}, nil, nil)
	status := cloudOnly.run("key-00000001", harnessrun.Budget{})
	if status.State != harnessrun.StateFailed || status.Code != "model_ineligible" || cloudOnly.counters["cloud"].Calls.Load() != 0 || cloudOnly.counters["cloud"].Probes.Load() != 0 && cloudOnly.counters["cloud"].Calls.Load() != 0 {
		t.Fatalf("status=%+v calls=%d", status, cloudOnly.counters["cloud"].Calls.Load())
	}
	if r := cloudOnly.counters["cloud"].Requests.Load(); r != nil {
		t.Fatalf("the cloud provider was sent the restricted prompt: %+v", r)
	}

	// With a local restricted-capable provider and a cheaper cloud one, restricted data goes only local.
	cheaper := Pricing{InputPerMTok: 1, CachedInputPerMTok: 1, OutputPerMTok: 1}
	mixed := newRouted(t, []provider{
		{"cloud", cheaper, ClassInternal, harnesstest.Behavior{Script: done}},
		{"local", p, ClassRestricted, harnesstest.Behavior{Script: done}},
	}, fixedContext{pc: promptContext(int(ClassRestricted), "")}, nil, nil)
	if status := mixed.run("key-00000001", harnessrun.Budget{}); status.State != harnessrun.StateCompleted || mixed.counters["local"].Calls.Load() != 1 || mixed.counters["cloud"].Calls.Load() != 0 {
		t.Fatalf("status=%+v local=%d cloud=%d", status, mixed.counters["local"].Calls.Load(), mixed.counters["cloud"].Calls.Load())
	}
}

func TestFallbackStaysInsideTheEligibleChain(t *testing.T) {
	p := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	cheaper := Pricing{InputPerMTok: 1, CachedInputPerMTok: 1, OutputPerMTok: 1}
	// The only restricted-capable provider fails; the cheap cloud one must NOT be tried.
	r := newRouted(t, []provider{
		{"cloud", cheaper, ClassInternal, harnesstest.Behavior{Script: done}},
		{"local", p, ClassRestricted, harnesstest.Behavior{Outcome: harness.OutcomeFailed}},
	}, fixedContext{pc: promptContext(int(ClassRestricted), "")}, nil, nil)
	status := r.run("key-00000001", harnessrun.Budget{})
	if status.State != harnessrun.StateFailed || status.Code != "model_failed" || r.counters["cloud"].Calls.Load() != 0 || r.counters["cloud"].Requests.Load() != nil {
		t.Fatalf("a failure fell back to a provider that may not see this data: %+v cloud calls=%d", status, r.counters["cloud"].Calls.Load())
	}
}

func TestAFailedAttemptFallsBackToTheNextEligibleProviderAndIsAccountedFor(t *testing.T) {
	usage := harness.Usage{InputTokens: 800, OutputTokens: 0}
	cheap := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 2_000_000}
	dear := Pricing{InputPerMTok: 3_000_000, CachedInputPerMTok: 300_000, OutputPerMTok: 6_000_000}
	okUsage := harness.Usage{InputTokens: 800, OutputTokens: 50}
	r := newRouted(t, []provider{
		{"model-cheap", cheap, ClassInternal, harnesstest.Behavior{Outcome: harness.OutcomeFailed, Usage: usage, CostMicros: cheap.Cost(usage)}},
		{"model-dear", dear, ClassInternal, harnesstest.Behavior{Script: done, Usage: okUsage, CostMicros: dear.Cost(okUsage)}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	status := r.run("key-00000001", harnessrun.Budget{})
	if status.State != harnessrun.StateCompleted || r.counters["model-cheap"].Calls.Load() != 1 || r.counters["model-dear"].Calls.Load() != 1 {
		t.Fatalf("status=%+v", status)
	}
	if !contains(r.codes(status.ID), "turn:model_fallback") {
		t.Fatalf("events = %v", r.codes(status.ID))
	}
	// The failed attempt cost money and tokens too, and both are on the run.
	if status.Usage.InputTokens != 1600 || status.Usage.OutputTokens != 50 || status.Usage.SpendMicros != cheap.Cost(usage)+dear.Cost(okUsage) || status.Usage.Turns != 1 {
		t.Fatalf("usage = %+v", status.Usage)
	}
	if r.meter.Stats("model-cheap").Failures != 1 || r.meter.Stats("model-dear").Calls != 1 {
		t.Fatalf("meter cheap=%+v dear=%+v", r.meter.Stats("model-cheap"), r.meter.Stats("model-dear"))
	}
}

func TestARepeatedlyFailingProviderIsCooledDownAndSkipped(t *testing.T) {
	cheap := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	dear := Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 2_000_000}
	r := newRouted(t, []provider{
		{"model-flaky", cheap, ClassInternal, harnesstest.Behavior{Outcome: harness.OutcomeFailed}},
		{"model-steady", dear, ClassInternal, harnesstest.Behavior{Script: done}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	for i := 0; i < failureThreshold; i++ {
		if status := r.run("key-0000000"+string(rune('1'+i)), harnessrun.Budget{}); status.State != harnessrun.StateCompleted {
			t.Fatalf("run %d = %+v", i, status)
		}
	}
	if r.counters["model-flaky"].Calls.Load() != int32(failureThreshold) {
		t.Fatalf("flaky was tried %d times", r.counters["model-flaky"].Calls.Load())
	}
	status := r.run("key-00000009", harnessrun.Budget{})
	if status.State != harnessrun.StateCompleted || r.counters["model-flaky"].Calls.Load() != int32(failureThreshold) || contains(r.codes(status.ID), "turn:model_fallback") {
		t.Fatalf("a cooled-down provider was tried again: calls=%d events=%v", r.counters["model-flaky"].Calls.Load(), r.codes(status.ID))
	}
}

func TestACostCapExcludesAProviderRatherThanOverspending(t *testing.T) {
	huge := Pricing{InputPerMTok: 900_000_000, CachedInputPerMTok: 90_000_000, OutputPerMTok: 900_000_000}
	r := newRouted(t, []provider{{"model-huge", huge, ClassInternal, harnesstest.Behavior{Script: done}}},
		fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	status := r.run("key-00000001", harnessrun.Budget{MaxSpendMicros: 1000})
	if status.State != harnessrun.StateFailed || status.Code != "model_ineligible" || r.counters["model-huge"].Calls.Load() != 0 {
		t.Fatalf("status=%+v calls=%d", status, r.counters["model-huge"].Calls.Load())
	}
}

func TestCacheSavingsAreCreditedOnlyWhenQualified(t *testing.T) {
	const prefix = "0123456789abcdef0123456789abcdef"
	pricing := Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 8_000_000}
	cached := harness.Usage{InputTokens: 4000, CachedInputTokens: 3000, OutputTokens: 100}
	build := func(prefixID string) *routed {
		return newRouted(t, []provider{{"model-a", pricing, ClassInternal, harnesstest.Behavior{Script: done, Usage: cached, CostMicros: pricing.Cost(cached)}}},
			fixedContext{pc: promptContext(int(ClassInternal), prefixID)}, nil, nil)
	}
	qualified := build(prefix)
	qualified.run("key-00000001", harnessrun.Budget{})
	want := pricing.CacheSavings(cached, prefix)
	if got := qualified.meter.Stats("model-a").CacheSavingsMicros; got != want || want <= 0 {
		t.Fatalf("qualified savings = %d, want %d", got, want)
	}
	if qualified.meter.ObservedCachedFraction("model-a", prefix) != 0.75 {
		t.Fatal("the provider's report was not remembered for this prefix")
	}
	// The provider reports cached tokens, but the request made no prefix claim: nothing is credited.
	unqualified := build("")
	unqualified.run("key-00000001", harnessrun.Budget{})
	if got := unqualified.meter.Stats("model-a").CacheSavingsMicros; got != 0 {
		t.Fatalf("savings credited without a prefix identity: %d", got)
	}
	if unqualified.meter.Stats("model-a").CachedInputTokens != 3000 {
		t.Fatal("the reported cached tokens should still be recorded as usage")
	}
}

func TestReplayReproducesTheCostOfARealWorkload(t *testing.T) {
	a := Pricing{InputPerMTok: 1_500_000, CachedInputPerMTok: 150_000, OutputPerMTok: 6_000_000}
	b := Pricing{InputPerMTok: 2_500_000, CachedInputPerMTok: 250_000, OutputPerMTok: 10_000_000}
	ua := harness.Usage{InputTokens: 1200, CachedInputTokens: 400, OutputTokens: 80}
	ub := harness.Usage{InputTokens: 900, OutputTokens: 40}
	const prefix = "0123456789abcdef0123456789abcdef"
	r := newRouted(t, []provider{
		{"model-a", a, ClassInternal, harnesstest.Behavior{Script: done, Usage: ua, CostMicros: a.Cost(ua)}},
		{"model-b", b, ClassInternal, harnesstest.Behavior{Outcome: harness.OutcomeFailed, Usage: ub, CostMicros: b.Cost(ub)}},
	}, fixedContext{pc: promptContext(int(ClassInternal), prefix)}, nil, nil)
	var spend int64
	for i := 0; i < 6; i++ {
		status := r.run("key-0000000"+string(rune('1'+i)), harnessrun.Budget{})
		if status.State != harnessrun.StateCompleted {
			t.Fatalf("run %d = %+v", i, status)
		}
		spend += status.Usage.SpendMicros
	}
	replayed := Replay(r.meter.Journal(), r.prices)
	if replayed.CostMicros != spend || replayed.Calls != 6 {
		t.Fatalf("replayed cost %d over %d calls does not match the %d the runs recorded", replayed.CostMicros, replayed.Calls, spend)
	}
	if got := r.meter.Stats("model-a"); got.CostMicros != spend || replayed.CacheSavingsMicros != got.CacheSavingsMicros || got.CacheSavingsMicros <= 0 {
		t.Fatalf("meter=%+v replay=%+v", got, replayed)
	}
}

func TestAnAdvisorCanReorderTheEligibleChainButTheRunStillFallsBackWithinIt(t *testing.T) {
	p1 := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	p2 := Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 2_000_000}
	advisor := &fakeAdvisor{prefer: "model-dear", conf: 0.9}
	r := newRouted(t, []provider{
		{"model-cheap", p1, ClassInternal, harnesstest.Behavior{Script: done}},
		{"model-dear", p2, ClassInternal, harnesstest.Behavior{Outcome: harness.OutcomeFailed}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, advisor, nil)
	status := r.run("key-00000001", harnessrun.Budget{})
	if status.State != harnessrun.StateCompleted || r.counters["model-dear"].Calls.Load() != 1 || r.counters["model-cheap"].Calls.Load() != 1 || !contains(r.codes(status.ID), "turn:model_fallback") {
		t.Fatalf("status=%+v dear=%d cheap=%d", status, r.counters["model-dear"].Calls.Load(), r.counters["model-cheap"].Calls.Load())
	}
}

func TestARouterErrorOrUnopenableProviderFailsTheRunWithAFixedCode(t *testing.T) {
	p := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	// A provider whose live probe fails is excluded, so the run fails as ineligible without calling it.
	down := newRouted(t, []provider{{"model-down", p, ClassInternal, harnesstest.Behavior{ProbeErr: errors.New("down"), Script: done}}},
		fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	if status := down.run("key-00000001", harnessrun.Budget{}); status.State != harnessrun.StateFailed || status.Code != "model_ineligible" || down.counters["model-down"].Calls.Load() != 0 {
		t.Fatalf("status = %+v", status)
	}
	denied := newRouted(t, []provider{{"model-denied", p, ClassInternal, harnesstest.Behavior{Access: harness.AccessDenied, Script: done}}},
		fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	if status := denied.run("key-00000001", harnessrun.Budget{}); status.Code != "model_ineligible" || denied.counters["model-denied"].Calls.Load() != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestAnOversizedOrMalformedPromptContextFallsBackWithoutStoppingTheRun(t *testing.T) {
	p := Pricing{InputPerMTok: 1, CachedInputPerMTok: 1, OutputPerMTok: 1}
	for name, pc := range map[string]harnessrun.PromptContext{
		"oversized prompt": {Refs: promptContext(1, "").Refs, Prompt: strings.Repeat("p", harness.MaxPromptBytes+1), Class: 1},
		"bad class":        {Refs: promptContext(1, "").Refs, Prompt: "x", Class: 9},
		"negative tokens":  {Refs: promptContext(1, "").Refs, Prompt: "x", Class: 1, InputTokens: -1},
		"no refs":          {Prompt: "x", Class: 1},
	} {
		r := newRouted(t, []provider{{"model-a", p, ClassInternal, harnesstest.Behavior{Script: done}}}, fixedContext{pc: pc}, nil, nil)
		status := r.run("key-00000001", harnessrun.Budget{})
		if status.State != harnessrun.StateCompleted || !contains(r.codes(status.ID), "turn:context_fallback") {
			t.Errorf("%s: status=%+v events=%v", name, status, r.codes(status.ID))
		}
		if request := r.counters["model-a"].Requests.Load().(harness.ModelRequest); request.Prompt != "" {
			t.Errorf("%s: the unusable prompt was sent: %q", name, request.Prompt)
		}
	}
}

func TestAFailedCallEarnsNoCacheSavingsEvenIfItReportsCachedTokens(t *testing.T) {
	const prefix = "0123456789abcdef0123456789abcdef"
	pricing := Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 8_000_000}
	cached := harness.Usage{InputTokens: 4000, CachedInputTokens: 3000}
	r := newRouted(t, []provider{
		{"model-failing", pricing, ClassInternal, harnesstest.Behavior{Outcome: harness.OutcomeFailed, Usage: cached, CostMicros: pricing.Cost(cached)}},
		{"model-backup", Pricing{InputPerMTok: 9_000_000, CachedInputPerMTok: 900_000, OutputPerMTok: 9_000_000}, ClassInternal, harnesstest.Behavior{Script: done}},
	}, fixedContext{pc: promptContext(int(ClassInternal), prefix)}, nil, nil)
	if status := r.run("key-00000001", harnessrun.Budget{}); status.State != harnessrun.StateCompleted {
		t.Fatalf("status = %+v", status)
	}
	failing := r.meter.Stats("model-failing")
	if failing.Failures != 1 || failing.CacheSavingsMicros != 0 {
		t.Fatalf("a failed call was credited cache savings: %+v", failing)
	}
	if r.meter.ObservedCachedFraction("model-failing", prefix) != 0 {
		t.Fatal("a failed call became cache evidence")
	}
	if failing.CachedInputTokens != 3000 {
		t.Fatal("its reported usage should still be recorded")
	}
}

func TestEveryFallbackableOutcomeMovesToTheNextProviderAndNothingElseDoes(t *testing.T) {
	cheap := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	dear := Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 2_000_000}
	for _, outcome := range []harness.Outcome{harness.OutcomeFailed, harness.OutcomeUnavailable, harness.OutcomeDenied, harness.OutcomeUnsupported, harness.OutcomeStale, harness.OutcomeTimeout} {
		t.Run("falls back on "+string(outcome), func(t *testing.T) {
			r := newRouted(t, []provider{
				{"model-first", cheap, ClassInternal, harnesstest.Behavior{Outcome: outcome}},
				{"model-second", dear, ClassInternal, harnesstest.Behavior{Script: done}},
			}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
			status := r.run("key-00000001", harnessrun.Budget{})
			if status.State != harnessrun.StateCompleted || r.counters["model-second"].Calls.Load() != 1 {
				t.Fatalf("status=%+v second calls=%d", status, r.counters["model-second"].Calls.Load())
			}
		})
	}
	// A partial answer is a result, not a failure: the run ends there and the backup is untouched.
	partial := newRouted(t, []provider{
		{"model-first", cheap, ClassInternal, harnesstest.Behavior{Outcome: harness.OutcomePartial, Script: []harnesstest.Step{{Text: "cut off"}}}},
		{"model-second", dear, ClassInternal, harnesstest.Behavior{Script: done}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	if status := partial.run("key-00000001", harnessrun.Budget{}); status.State != harnessrun.StatePartial || partial.counters["model-second"].Calls.Load() != 0 {
		t.Fatalf("a partial answer fell back: %+v second calls=%d", status, partial.counters["model-second"].Calls.Load())
	}
}

func TestAContractFailedAttemptStillCostsTheTimeItTook(t *testing.T) {
	cheap := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	dear := Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 2_000_000}
	r := newRouted(t, []provider{
		{"model-stale", cheap, ClassInternal, harnesstest.Behavior{StaleReply: true, Delay: 120 * time.Millisecond, Script: done}},
		{"model-good", dear, ClassInternal, harnesstest.Behavior{Script: done}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	status := r.run("key-00000001", harnessrun.Budget{})
	if status.State != harnessrun.StateCompleted || status.Usage.ActiveMillis < 100 {
		t.Fatalf("the failed attempt's time was not accounted: %+v", status)
	}
	if r.meter.Stats("model-stale").Failures != 1 {
		t.Fatalf("meter = %+v", r.meter.Stats("model-stale"))
	}
}

// stubRouter returns a fixed chain, to test the loop's own limits independent of the real router.
type stubRouter struct{ chain []harnessrun.Binding }

func (s stubRouter) Plan(context.Context, harnessrun.ModelNeed) ([]harnessrun.Binding, error) {
	return s.chain, nil
}
func (stubRouter) Observe(harness.ProviderID, harnessrun.ModelObservation) {}

func TestTheLoopCapsAnyRoutersChainAtFourAttempts(t *testing.T) {
	registry := harness.NewRegistry()
	counters := map[string]*harnesstest.Counters{}
	var chain []harnessrun.Binding
	for _, id := range []string{"m1", "m2", "m3", "m4", "m5", "m6"} {
		c, err := harnesstest.Register(registry, harnesstest.Manifest(id, harness.KindModel, "generation"), harnesstest.Behavior{Outcome: harness.OutcomeFailed})
		if err != nil {
			t.Fatal(err)
		}
		counters[id] = c
		chain = append(chain, harnessrun.Binding{Provider: harness.ProviderID(id), Capability: "generation"})
	}
	manager, err := harnessrun.NewManager(harnessrun.Config{DataDir: t.TempDir(), Registry: registry, Model: chain[0], Router: stubRouter{chain: chain},
		Context: fixedContext{pc: promptContext(1, "")}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	started, err := manager.Start(context.Background(), runOwner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "g"})
	if err != nil {
		t.Fatal(err)
	}
	var status harnessrun.Status
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		status, _ = manager.Status(context.Background(), runOwner, started.ID)
		if status.State.Terminal() {
			break
		}
	}
	attempts := 0
	for _, c := range counters {
		attempts += int(c.Calls.Load())
	}
	if status.State != harnessrun.StateFailed || attempts != 4 || counters["m5"].Calls.Load() != 0 || counters["m6"].Calls.Load() != 0 {
		t.Fatalf("status=%+v attempts=%d", status, attempts)
	}
}

func TestABudgetTimeoutEndsTheRunInsteadOfTryingAnotherProvider(t *testing.T) {
	p := Pricing{InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 1_000_000}
	r := newRouted(t, []provider{
		{"model-slow", p, ClassInternal, harnesstest.Behavior{Delay: 10 * time.Second, Script: done}},
		{"model-backup", Pricing{InputPerMTok: 2_000_000, CachedInputPerMTok: 200_000, OutputPerMTok: 2_000_000}, ClassInternal, harnesstest.Behavior{Script: done}},
	}, fixedContext{pc: promptContext(int(ClassInternal), "")}, nil, nil)
	status := r.run("key-00000001", harnessrun.Budget{MaxActive: 150 * time.Millisecond})
	if status.State != harnessrun.StatePartial || status.Code != "budget_time" {
		t.Fatalf("status = %+v", status)
	}
	if contains(r.codes(status.ID), "turn:model_fallback") || r.counters["model-backup"].Calls.Load() != 0 {
		t.Fatalf("a spent time budget triggered a fallback attempt: events=%v backup calls=%d", r.codes(status.ID), r.counters["model-backup"].Calls.Load())
	}
}
