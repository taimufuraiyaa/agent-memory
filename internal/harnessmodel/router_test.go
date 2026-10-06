package harnessmodel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
)

type fakeProber struct {
	mu     sync.Mutex
	states map[harness.ProviderID]harness.AccessState
	calls  atomic.Int32
}

func (f *fakeProber) Access(_ context.Context, p harness.ProviderID, _ harness.CapabilityID) harness.AccessState {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.states[p]; ok {
		return s
	}
	return harness.AccessAvailable
}

func profile(id string, in, out int64, class Class) Profile {
	return Profile{Provider: harness.ProviderID(id), Capability: "generation",
		Pricing: Pricing{InputPerMTok: in, CachedInputPerMTok: in / 10, OutputPerMTok: out}, MaxClass: class, ContextTokens: 100_000, MaxOutputTokens: 4000}
}

func newRouter(t *testing.T, prober AccessProber, meter *Meter, advisor Advisor, profiles ...Profile) *Router {
	t.Helper()
	r, err := NewRouter(RouterConfig{Profiles: profiles, Prober: prober, Meter: meter, Advisor: advisor, AdviceTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ids(chain []Choice) []string {
	out := make([]string, len(chain))
	for i, c := range chain {
		out[i] = string(c.Profile.Provider)
	}
	return out
}

var need = Need{Class: ClassInternal, InputTokens: 10_000, OutputTokens: 500}

func TestEligibilityGatesAndTheirReasons(t *testing.T) {
	clock := newClock()
	meter := NewMeter(clock.now)
	for i := 0; i < failureThreshold; i++ {
		meter.Observe(Observation{Provider: "cooling", Outcome: harness.OutcomeFailed})
	}
	small := profile("small-window", 1, 1, ClassRestricted)
	small.ContextTokens = 5000
	smallOut := profile("small-output", 1, 1, ClassRestricted)
	smallOut.MaxOutputTokens = 100
	prober := &fakeProber{states: map[harness.ProviderID]harness.AccessState{"down": harness.AccessUnavailable, "denied": harness.AccessDenied, "unsupported": harness.AccessUnsupported}}
	r := newRouter(t, prober, meter, nil,
		profile("good", 100, 100, ClassInternal), profile("public-only", 1, 1, ClassPublic), small, smallOut,
		profile("down", 1, 1, ClassRestricted), profile("denied", 1, 1, ClassRestricted), profile("unsupported", 1, 1, ClassRestricted),
		profile("cooling", 1, 1, ClassRestricted), profile("pricey", 900_000_000, 900_000_000, ClassRestricted))
	tight := need
	tight.HasSpendCap, tight.SpendCapMicros = true, 10_000
	chain, report, err := r.Plan(context.Background(), tight)
	if err != nil || !reflect.DeepEqual(ids(chain), []string{"good"}) {
		t.Fatalf("chain = %v, %v", ids(chain), err)
	}
	want := map[string]int{ReasonClass: 1, ReasonWindow: 1, ReasonOutput: 1, ReasonUnavailable: 3, ReasonCooldown: 1, ReasonCost: 1}
	if report.Considered != 9 || !reflect.DeepEqual(report.Excluded, want) {
		t.Fatalf("report = %+v", report)
	}
	clock.advance(cooldown + time.Second)
	again, _, _ := r.Plan(context.Background(), need)
	if !contains(ids(again), "cooling") {
		t.Fatalf("a recovered provider is still excluded: %v", ids(again))
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestTheDataClassDecidesWhichProvidersMayBeUsed(t *testing.T) {
	r := newRouter(t, &fakeProber{}, NewMeter(nil), nil,
		profile("local", 1, 1, ClassRestricted), profile("cloud", 1, 1, ClassInternal), profile("open", 1, 1, ClassPublic))
	for class, want := range map[Class]int{ClassPublic: 3, ClassInternal: 2, ClassSensitive: 1, ClassRestricted: 1} {
		n := need
		n.Class = class
		chain, _, err := r.Plan(context.Background(), n)
		if err != nil || len(chain) != want {
			t.Errorf("class %d: %v, %v (want %d)", class, ids(chain), err, want)
		}
		for _, c := range chain {
			if class > c.Profile.MaxClass {
				t.Errorf("class %d routed to %s, whose limit is %d", class, c.Profile.Provider, c.Profile.MaxClass)
			}
		}
	}
	// With only an under-eligible provider, the call fails outright rather than leaking data.
	only := newRouter(t, &fakeProber{}, NewMeter(nil), nil, profile("cloud", 1, 1, ClassInternal))
	n := need
	n.Class = ClassRestricted
	if _, report, err := only.Plan(context.Background(), n); !errors.Is(err, ErrNoEligible) || report.Excluded[ReasonClass] != 1 {
		t.Fatalf("restricted data with no eligible provider = %v %+v", err, report)
	}
}

func TestRankingIsByCostThenLatencyThenID(t *testing.T) {
	meter := NewMeter(newClock().now)
	meter.Observe(ok("fast-b", 50*time.Millisecond, harness.Usage{}))
	meter.Observe(ok("slow-a", 900*time.Millisecond, harness.Usage{}))
	r := newRouter(t, &fakeProber{}, meter, nil,
		profile("slow-a", 100, 100, ClassInternal), profile("fast-b", 100, 100, ClassInternal), profile("cheap-z", 10, 10, ClassInternal), profile("unknown-c", 100, 100, ClassInternal))
	chain, _, err := r.Plan(context.Background(), need)
	if err != nil {
		t.Fatal(err)
	}
	// Unobserved providers rank as UnknownLatency (2s): behind a measured 900ms, ahead of a measured 3s.
	if want := []string{"cheap-z", "fast-b", "slow-a", "unknown-c"}; !reflect.DeepEqual(ids(chain), want) {
		t.Fatalf("order = %v, want %v", ids(chain), want)
	}
	meter.Observe(ok("slow-a", 20*time.Second, harness.Usage{}))
	for i := 0; i < 30; i++ {
		meter.Observe(ok("slow-a", 20*time.Second, harness.Usage{}))
	}
	settled, _, _ := r.Plan(context.Background(), need)
	if !reflect.DeepEqual(ids(settled), []string{"cheap-z", "fast-b", "unknown-c", "slow-a"}) {
		t.Fatalf("a provider measured slower than the default must rank below an unobserved one: %v", ids(settled))
	}
	chain = settled
	for i := 1; i < len(chain); i++ {
		if chain[i-1].EstimatedCostMicros > chain[i].EstimatedCostMicros {
			t.Fatal("costs are not non-decreasing")
		}
	}
	for run := 0; run < 20; run++ {
		again, _, _ := r.Plan(context.Background(), need)
		if !reflect.DeepEqual(ids(again), ids(chain)) {
			t.Fatal("planning is not deterministic")
		}
	}
}

func TestCacheDiscountsNeedAProviderReportForTheSamePrefix(t *testing.T) {
	const prefix = "0123456789abcdef"
	meter := NewMeter(newClock().now)
	r := newRouter(t, &fakeProber{}, meter, nil,
		profile("caching", 1_000_000, 1_000_000, ClassInternal), profile("plain", 800_000, 1_000_000, ClassInternal))
	withPrefix := need
	withPrefix.PrefixID = prefix
	chain, _, _ := r.Plan(context.Background(), withPrefix)
	if chain[0].Profile.Provider != "plain" {
		t.Fatalf("with no cache evidence the cheaper list price wins: %v", ids(chain))
	}
	// The provider reports that most of the input was served from cache for this prefix.
	meter.Observe(Observation{Provider: "caching", Outcome: harness.OutcomeOK, PrefixID: prefix, Usage: harness.Usage{InputTokens: 10_000, CachedInputTokens: 9_000}})
	chain, _, _ = r.Plan(context.Background(), withPrefix)
	if chain[0].Profile.Provider != "caching" {
		t.Fatalf("reported cache reuse should now make it cheaper: %v %+v", ids(chain), chain)
	}
	// The same evidence earns nothing for another prefix or a call that makes no prefix claim.
	other := need
	other.PrefixID = "ffffffffffffffff"
	if chain, _, _ = r.Plan(context.Background(), other); chain[0].Profile.Provider != "plain" {
		t.Fatalf("a different prefix was discounted: %v", ids(chain))
	}
	if chain, _, _ = r.Plan(context.Background(), need); chain[0].Profile.Provider != "plain" {
		t.Fatalf("a call with no prefix identity was discounted: %v", ids(chain))
	}
}

func TestTheChainIsBoundedAndNeverContainsAnExcludedProvider(t *testing.T) {
	var profiles []Profile
	for i := 0; i < 9; i++ {
		class := ClassInternal
		if i%3 == 0 {
			class = ClassPublic
		}
		profiles = append(profiles, profile(fmt.Sprintf("p%d", i), int64(10+i), 10, class))
	}
	r := newRouter(t, &fakeProber{}, NewMeter(nil), nil, profiles...)
	chain, report, err := r.Plan(context.Background(), need)
	if err != nil || len(chain) != MaxChain || report.Excluded[ReasonClass] != 3 {
		t.Fatalf("chain=%v report=%+v err=%v", ids(chain), report, err)
	}
	for _, c := range chain {
		if c.Profile.MaxClass < need.Class {
			t.Fatalf("an ineligible provider %s is in the fallback chain", c.Profile.Provider)
		}
	}
}

func TestNeedValidation(t *testing.T) {
	r := newRouter(t, &fakeProber{}, NewMeter(nil), nil, profile("p", 1, 1, ClassInternal))
	for _, n := range []Need{{Class: 9, OutputTokens: 1}, {Class: -1, OutputTokens: 1}, {OutputTokens: 0}, {InputTokens: -1, OutputTokens: 1}} {
		if _, _, err := r.Plan(context.Background(), n); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v = %v", n, err)
		}
	}
}

func TestNewRouterValidation(t *testing.T) {
	good := []Profile{profile("p", 1, 1, ClassInternal)}
	for name, cfg := range map[string]RouterConfig{
		"no profiles":     {Prober: &fakeProber{}, Meter: NewMeter(nil)},
		"no prober":       {Profiles: good, Meter: NewMeter(nil)},
		"no meter":        {Profiles: good, Prober: &fakeProber{}},
		"duplicate":       {Profiles: append(append([]Profile(nil), good...), good...), Prober: &fakeProber{}, Meter: NewMeter(nil)},
		"invalid profile": {Profiles: []Profile{{Provider: "p"}}, Prober: &fakeProber{}, Meter: NewMeter(nil)},
	} {
		if _, err := NewRouter(cfg); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}
}

type fakeAdvisor struct {
	prefer harness.ProviderID
	conf   float64
	err    error
	delay  time.Duration
	panics bool
	seen   []harness.ProviderID
	calls  atomic.Int32
}

func (f *fakeAdvisor) Prefer(ctx context.Context, providers []harness.ProviderID) (harness.ProviderID, float64, error) {
	f.calls.Add(1)
	f.seen = providers
	if f.panics {
		panic("advisor exploded")
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", 0, ctx.Err()
		}
	}
	return f.prefer, f.conf, f.err
}

func adviceProfiles() []Profile {
	return []Profile{profile("a-cheap", 10, 10, ClassInternal), profile("b-mid", 20, 20, ClassInternal), profile("c-pricey", 30, 30, ClassInternal), profile("d-public", 1, 1, ClassPublic)}
}

func TestAdviceReordersOnlyEligibleProvidersAndNeverWidensTheSet(t *testing.T) {
	advisor := &fakeAdvisor{prefer: "c-pricey", conf: 0.9}
	r := newRouter(t, &fakeProber{}, NewMeter(nil), advisor, adviceProfiles()...)
	chain, report, err := r.Plan(context.Background(), need)
	if err != nil || report.Advice != AdviceApplied || !reflect.DeepEqual(ids(chain), []string{"c-pricey", "a-cheap", "b-mid"}) || !chain[0].Preferred || chain[1].Preferred {
		t.Fatalf("chain=%v report=%+v err=%v", ids(chain), report, err)
	}
	if !reflect.DeepEqual(advisor.seen, []harness.ProviderID{"a-cheap", "b-mid", "c-pricey"}) {
		t.Fatalf("the advisor was offered %v; the class-excluded provider must not be among them", advisor.seen)
	}
	// Advice naming a provider that was excluded is void, not a way around the gate.
	widening := &fakeAdvisor{prefer: "d-public", conf: 0.99}
	r = newRouter(t, &fakeProber{}, NewMeter(nil), widening, adviceProfiles()...)
	chain, report, _ = r.Plan(context.Background(), need)
	if report.Advice != AdviceInvalid || contains(ids(chain), "d-public") {
		t.Fatalf("advice widened the eligible set: %v %+v", ids(chain), report)
	}
	// Advice for the provider that is already first keeps the order and marks it.
	first := &fakeAdvisor{prefer: "a-cheap", conf: 0.9}
	r = newRouter(t, &fakeProber{}, NewMeter(nil), first, adviceProfiles()...)
	chain, report, _ = r.Plan(context.Background(), need)
	if report.Advice != AdviceApplied || !reflect.DeepEqual(ids(chain), []string{"a-cheap", "b-mid", "c-pricey"}) || !chain[0].Preferred {
		t.Fatalf("chain=%v report=%+v", ids(chain), report)
	}
}

func TestEveryAdviceFailureLeavesTheDeterministicChain(t *testing.T) {
	baseline, _, _ := newRouter(t, &fakeProber{}, NewMeter(nil), nil, adviceProfiles()...).Plan(context.Background(), need)
	for name, tc := range map[string]struct {
		advisor *fakeAdvisor
		outcome string
	}{
		"unknown":        {&fakeAdvisor{prefer: "ghost", conf: 0.9}, AdviceInvalid},
		"NaN":            {&fakeAdvisor{prefer: "c-pricey", conf: nan()}, AdviceInvalid},
		"over one":       {&fakeAdvisor{prefer: "c-pricey", conf: 1.5}, AdviceInvalid},
		"negative":       {&fakeAdvisor{prefer: "c-pricey", conf: -0.2}, AdviceInvalid},
		"low confidence": {&fakeAdvisor{prefer: "c-pricey", conf: 0.3}, AdviceLowConf},
		"error":          {&fakeAdvisor{err: errors.New("boom")}, AdviceFailed},
		"panic":          {&fakeAdvisor{panics: true}, AdviceFailed},
		"slow":           {&fakeAdvisor{prefer: "c-pricey", conf: 0.9, delay: 5 * time.Second}, AdviceTimeout},
	} {
		started := time.Now()
		r := newRouter(t, &fakeProber{}, NewMeter(nil), tc.advisor, adviceProfiles()...)
		chain, report, err := r.Plan(context.Background(), need)
		if err != nil || report.Advice != tc.outcome || !reflect.DeepEqual(ids(chain), ids(baseline)) {
			t.Errorf("%s: chain=%v advice=%s err=%v", name, ids(chain), report.Advice, err)
		}
		if time.Since(started) > 2*time.Second {
			t.Errorf("%s held the router for %v", name, time.Since(started))
		}
	}
	// With one eligible provider there is nothing to choose between, so the advisor is not consulted.
	single := &fakeAdvisor{prefer: "a-cheap", conf: 0.9}
	r := newRouter(t, &fakeProber{}, NewMeter(nil), single, profile("a-cheap", 1, 1, ClassInternal))
	if _, report, _ := r.Plan(context.Background(), need); single.calls.Load() != 0 || report.Advice != AdviceNone {
		t.Fatalf("calls=%d advice=%s", single.calls.Load(), report.Advice)
	}
}

func nan() float64 { var z float64; return z / z }

type capturingDecision struct {
	mu       sync.Mutex
	question harness.DecisionQuestion
	selected []string
	outcome  harness.Outcome
}

func (c *capturingDecision) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "router-advisor", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"model": harness.AccessAvailable}}, nil
}
func (c *capturingDecision) Close() error { return nil }
func (c *capturingDecision) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.question = q
	outcome := c.outcome
	if outcome == "" {
		outcome = harness.OutcomeOK
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: outcome, Selected: c.selected, Confidence: 0.9}, nil
}

func TestADecisionSessionAdvisorSeesOnlyProviderIDs(t *testing.T) {
	provider := &capturingDecision{selected: []string{"b-mid"}}
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "router-advisor", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"model"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return provider, nil }); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "router-advisor", harness.Scope{Workspace: "agent-memory", Run: "run-x", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	r := newRouter(t, &fakeProber{}, NewMeter(nil), SessionAdvisor{Session: session, Capability: "model"}, adviceProfiles()...)
	chain, report, err := r.Plan(context.Background(), need)
	if err != nil || report.Advice != AdviceApplied || chain[0].Profile.Provider != "b-mid" {
		t.Fatalf("chain=%v report=%+v err=%v", ids(chain), report, err)
	}
	q := provider.question
	if q.Kind != "model" || !reflect.DeepEqual(q.Candidates, []string{"a-cheap", "b-mid", "c-pricey"}) || len(q.Evidence) != 0 {
		t.Fatalf("question = %+v", q)
	}
	// Anything other than exactly one selection, or a non-OK outcome, falls back.
	for name, p := range map[string]*capturingDecision{"two": {selected: []string{"a-cheap", "b-mid"}}, "none": {}, "denied": {selected: []string{"b-mid"}, outcome: harness.OutcomeDenied}} {
		reg := harness.NewRegistry()
		_ = reg.Register(manifest, func() (harness.Provider, error) { return p, nil })
		sess, _ := reg.OpenSession(context.Background(), "router-advisor", harness.Scope{Workspace: "agent-memory", Run: "run-y", Generation: 1})
		rr := newRouter(t, &fakeProber{}, NewMeter(nil), SessionAdvisor{Session: sess, Capability: "model"}, adviceProfiles()...)
		_, rep, _ := rr.Plan(context.Background(), need)
		if rep.Advice != AdviceFailed {
			t.Errorf("%s: advice %s", name, rep.Advice)
		}
		_ = sess.Close()
	}
	if _, _, err := (SessionAdvisor{}).Prefer(context.Background(), nil); err == nil {
		t.Fatal("an advisor with no session must fail")
	}
}

func TestRegistryProberProbesLiveAndCachesBriefly(t *testing.T) {
	clock := newClock()
	registry := harness.NewRegistry()
	counters, err := harnesstest.Register(registry, harnesstest.Manifest("live", harness.KindModel, "generation"), harnesstest.Behavior{})
	if err != nil {
		t.Fatal(err)
	}
	deniedCounters, _ := harnesstest.Register(registry, harnesstest.Manifest("denied", harness.KindModel, "generation"), harnesstest.Behavior{Access: harness.AccessDenied})
	_, _ = harnesstest.Register(registry, harnesstest.Manifest("broken", harness.KindModel, "generation"), harnesstest.Behavior{ProbeErr: errors.New("down")})
	p := &RegistryProber{Registry: registry, Workspace: "agent-memory", TTL: time.Minute, Now: clock.now}
	ctx := context.Background()
	if p.Access(ctx, "live", "generation") != harness.AccessAvailable || p.Access(ctx, "denied", "generation") != harness.AccessDenied ||
		p.Access(ctx, "broken", "generation") != harness.AccessUnavailable || p.Access(ctx, "ghost", "generation") != harness.AccessUnavailable {
		t.Fatal("live states were not reported")
	}
	if p.Access(ctx, "live", "undeclared") != harness.AccessUnavailable {
		t.Fatal("an undeclared capability must be unavailable")
	}
	for i := 0; i < 5; i++ {
		p.Access(ctx, "live", "generation")
	}
	if counters.Probes.Load() != 2 {
		t.Fatalf("probes inside the TTL = %d", counters.Probes.Load())
	}
	if counters.Closes.Load() != counters.Probes.Load() || deniedCounters.Closes.Load() != 1 {
		t.Fatal("a probe left its session open")
	}
	// After the TTL it probes again, and repeated probes never trip the stale-generation guard.
	for i := 0; i < 20; i++ {
		clock.advance(2 * time.Minute)
		if p.Access(ctx, "live", "generation") != harness.AccessAvailable {
			t.Fatalf("probe %d failed", i)
		}
	}
	if counters.Probes.Load() != 2+20 {
		t.Fatalf("probes after expiry = %d", counters.Probes.Load())
	}
}

func TestEndToEndRouterOverRealProviderSessions(t *testing.T) {
	registry := harness.NewRegistry()
	for _, id := range []string{"model-a", "model-b"} {
		if _, err := harnesstest.Register(registry, harnesstest.Manifest(id, harness.KindModel, "generation"), harnesstest.Behavior{}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = harnesstest.Register(registry, harnesstest.Manifest("model-c", harness.KindModel, "generation"), harnesstest.Behavior{Access: harness.AccessUnsupported})
	prober := &RegistryProber{Registry: registry, Workspace: "agent-memory"}
	r := newRouter(t, prober, NewMeter(nil), nil, profile("model-a", 2_000_000, 8_000_000, ClassInternal), profile("model-b", 1_000_000, 4_000_000, ClassInternal), profile("model-c", 1, 1, ClassInternal))
	chain, report, err := r.Plan(context.Background(), need)
	if err != nil || !reflect.DeepEqual(ids(chain), []string{"model-b", "model-a"}) || report.Excluded[ReasonUnavailable] != 1 {
		t.Fatalf("chain=%v report=%+v err=%v", ids(chain), report, err)
	}
}
