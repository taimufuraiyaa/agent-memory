package harnessmodel

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func ok(provider string, latency time.Duration, u harness.Usage) Observation {
	return Observation{Provider: harness.ProviderID(provider), Outcome: harness.OutcomeOK, Latency: latency, Usage: u}
}

func TestMeterAccumulatesUsageCostAndSmoothsLatency(t *testing.T) {
	m := NewMeter(newClock().now)
	if m.Latency("a") != UnknownLatency || m.Stats("a").Calls != 0 {
		t.Fatal("an unseen provider must look unknown, not fast")
	}
	m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeOK, Latency: 100 * time.Millisecond, Usage: harness.Usage{InputTokens: 100, OutputTokens: 10, CachedInputTokens: 40}, CostMicros: 7, CacheSavingsMicros: 2})
	if got := m.Latency("a"); got != 100*time.Millisecond {
		t.Fatalf("the first sample is the value itself, got %v", got)
	}
	m.Observe(ok("a", 200*time.Millisecond, harness.Usage{InputTokens: 50, OutputTokens: 5}))
	want := time.Duration(0.3*float64(200*time.Millisecond) + 0.7*float64(100*time.Millisecond))
	if got := m.Latency("a"); got != want {
		t.Fatalf("smoothed latency = %v, want %v", got, want)
	}
	s := m.Stats("a")
	if s.Calls != 2 || s.InputTokens != 150 || s.OutputTokens != 15 || s.CachedInputTokens != 40 || s.CostMicros != 7 || s.CacheSavingsMicros != 2 || s.LatencySamples != 2 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestOnlyCompletedCallsInfluenceLatency(t *testing.T) {
	m := NewMeter(newClock().now)
	m.Observe(ok("a", 500*time.Millisecond, harness.Usage{}))
	for _, outcome := range []harness.Outcome{harness.OutcomeFailed, harness.OutcomeTimeout, harness.OutcomeUnavailable, harness.OutcomeDenied} {
		m.Observe(Observation{Provider: "a", Outcome: outcome, Latency: time.Millisecond})
	}
	if got := m.Latency("a"); got != 500*time.Millisecond {
		t.Fatalf("a fast failure made the provider look quick: %v", got)
	}
	if m.Stats("a").Failures != 4 {
		t.Fatalf("failures = %d", m.Stats("a").Failures)
	}
	m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeCancelled})
	if m.Stats("a").Failures != 4 {
		t.Fatal("a cancellation is not the provider's failure")
	}
}

func TestRepeatedFailuresCoolAProviderDownThenItRecovers(t *testing.T) {
	clock := newClock()
	m := NewMeter(clock.now)
	fail := Observation{Provider: "a", Outcome: harness.OutcomeFailed}
	m.Observe(fail)
	m.Observe(fail)
	if m.CooledDown("a") {
		t.Fatal("two failures must not trip the breaker")
	}
	m.Observe(ok("a", time.Millisecond, harness.Usage{}))
	m.Observe(fail)
	m.Observe(fail)
	if m.CooledDown("a") {
		t.Fatal("a success must reset the streak")
	}
	m.Observe(fail)
	if !m.CooledDown("a") {
		t.Fatal("three consecutive failures must trip the breaker")
	}
	if m.CooledDown("b") {
		t.Fatal("another provider was cooled down")
	}
	clock.advance(cooldown - time.Second)
	if !m.CooledDown("a") {
		t.Fatal("the cooldown ended early")
	}
	clock.advance(2 * time.Second)
	if m.CooledDown("a") {
		t.Fatal("the cooldown did not end")
	}
}

func TestCacheEvidenceIsPerPrefixProviderReportedAndBounded(t *testing.T) {
	m := NewMeter(newClock().now)
	const prefix = "0123456789abcdef"
	if m.ObservedCachedFraction("a", prefix) != 0 || m.ObservedCachedFraction("a", "") != 0 {
		t.Fatal("no evidence must mean no discount")
	}
	m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeOK, PrefixID: prefix, Usage: harness.Usage{InputTokens: 1000, CachedInputTokens: 750}})
	if got := m.ObservedCachedFraction("a", prefix); got != 0.75 {
		t.Fatalf("fraction = %v", got)
	}
	if m.ObservedCachedFraction("b", prefix) != 0 || m.ObservedCachedFraction("a", "ffffffffffffffff") != 0 {
		t.Fatal("evidence leaked across providers or prefixes")
	}
	// A call that made no prefix claim, failed, or had no input records nothing.
	m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeOK, Usage: harness.Usage{InputTokens: 1000, CachedInputTokens: 900}})
	m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeFailed, PrefixID: prefix, Usage: harness.Usage{InputTokens: 1000, CachedInputTokens: 900}})
	m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeOK, PrefixID: prefix})
	if got := m.ObservedCachedFraction("a", prefix); got != 0.75 {
		t.Fatalf("the evidence was overwritten by an unqualified call: %v", got)
	}
	for i := 0; i < cacheEvidenceSize+50; i++ {
		m.Observe(Observation{Provider: "a", Outcome: harness.OutcomeOK, PrefixID: fmt.Sprintf("%016x", i+1), Usage: harness.Usage{InputTokens: 100, CachedInputTokens: 50}})
	}
	if len(m.cache) > cacheEvidenceSize {
		t.Fatalf("cache evidence grew to %d", len(m.cache))
	}
	if m.ObservedCachedFraction("a", prefix) != 0 {
		t.Fatal("the oldest evidence should have been evicted")
	}
}

func TestJournalIsBoundedAndReplayReproducesRecordedCost(t *testing.T) {
	m := NewMeter(newClock().now)
	prices := map[harness.ProviderID]Pricing{"a": price, "b": {InputPerMTok: 1_000_000, CachedInputPerMTok: 100_000, OutputPerMTok: 4_000_000}}
	const prefix = "0123456789abcdef"
	var recordedCost, recordedSavings int64
	for i := 0; i < journalSize+300; i++ {
		provider := harness.ProviderID([]string{"a", "b"}[i%2])
		u := harness.Usage{InputTokens: 100 + i%900, CachedInputTokens: (i % 5) * 20, OutputTokens: 10 + i%50}
		p := prices[provider]
		cost, savings := p.Cost(u), p.CacheSavings(u, prefix)
		m.Observe(Observation{Provider: provider, Outcome: harness.OutcomeOK, PrefixID: prefix, Usage: u, CostMicros: cost, CacheSavingsMicros: savings})
		if i >= 300 {
			recordedCost += cost
			recordedSavings += savings
		}
	}
	journal := m.Journal()
	if len(journal) != journalSize {
		t.Fatalf("journal length = %d", len(journal))
	}
	replayed := Replay(journal, prices)
	if replayed.Calls != journalSize || replayed.CostMicros != recordedCost || replayed.CacheSavingsMicros != recordedSavings {
		t.Fatalf("replay %+v does not reproduce the recorded cost %d / savings %d", replayed, recordedCost, recordedSavings)
	}
	// Under different prices the same traffic costs something else, which is the point of replay.
	cheaper := map[harness.ProviderID]Pricing{"a": {InputPerMTok: 1, CachedInputPerMTok: 1, OutputPerMTok: 1}, "b": prices["b"]}
	if Replay(journal, cheaper).CostMicros >= replayed.CostMicros {
		t.Fatal("repricing did not change the replayed cost")
	}
	// The journal is a copy.
	journal[0].CostMicros = -1
	if m.Journal()[0].CostMicros == -1 {
		t.Fatal("the journal was mutated through a returned copy")
	}
}

func TestMeterIsSafeForConcurrentUse(t *testing.T) {
	m := NewMeter(time.Now)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				m.Observe(Observation{Provider: harness.ProviderID(fmt.Sprintf("p%d", g%4)), Outcome: harness.OutcomeOK, Latency: time.Millisecond,
					PrefixID: "0123456789abcdef", Usage: harness.Usage{InputTokens: 10, OutputTokens: 1}, CostMicros: 1})
				_ = m.Latency("p0")
				_ = m.CooledDown("p1")
				_ = m.Journal()
			}
		}(g)
	}
	wg.Wait()
	var calls int
	var cost int64
	for i := 0; i < 4; i++ {
		s := m.Stats(harness.ProviderID(fmt.Sprintf("p%d", i)))
		calls += s.Calls
		cost += s.CostMicros
	}
	if calls != 16*200 || cost != 16*200 {
		t.Fatalf("calls=%d cost=%d", calls, cost)
	}
}
