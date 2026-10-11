package harnessmodel

import (
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const (
	journalSize       = 1024
	cacheEvidenceSize = 256
	ewmaWeight        = 0.3
	// UnknownLatency ranks a provider with no latency history, so it is neither favored
	// nor shut out before it has been observed.
	UnknownLatency   = 2 * time.Second
	failureThreshold = 3
	cooldown         = 30 * time.Second
)

// Observation is one finished provider call as the meter records it.
type Observation struct {
	Provider   harness.ProviderID
	Outcome    harness.Outcome
	Latency    time.Duration
	Usage      harness.Usage
	PrefixID   string
	CostMicros int64
	// CacheSavingsMicros is credited only when the call was cache-qualified.
	CacheSavingsMicros int64
}

// Stats is the running account for one provider.
type Stats struct {
	Calls              int
	Failures           int
	InputTokens        int64
	OutputTokens       int64
	CachedInputTokens  int64
	CostMicros         int64
	CacheSavingsMicros int64
	Latency            time.Duration
	LatencySamples     int
}

type cacheKey struct {
	provider harness.ProviderID
	prefix   string
}

// Meter records usage, cost, latency and cache evidence per provider, and trips a short
// cooldown after repeated failures so a dead provider stops costing every turn.
type Meter struct {
	mu       sync.Mutex
	now      func() time.Time
	stats    map[harness.ProviderID]*Stats
	failures map[harness.ProviderID]int
	until    map[harness.ProviderID]time.Time
	cache    map[cacheKey]float64
	order    []cacheKey
	journal  []Observation
}

func NewMeter(now func() time.Time) *Meter {
	if now == nil {
		now = time.Now
	}
	return &Meter{now: now, stats: map[harness.ProviderID]*Stats{}, failures: map[harness.ProviderID]int{},
		until: map[harness.ProviderID]time.Time{}, cache: map[cacheKey]float64{}}
}

// Observe records one call. Only a call that completed counts toward latency, so a fast
// failure cannot make a broken provider look quick.
func (m *Meter) Observe(o Observation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.stats[o.Provider]
	if s == nil {
		s = &Stats{}
		m.stats[o.Provider] = s
	}
	s.Calls++
	s.InputTokens += int64(o.Usage.InputTokens)
	s.OutputTokens += int64(o.Usage.OutputTokens)
	s.CachedInputTokens += int64(o.Usage.CachedInputTokens)
	s.CostMicros += o.CostMicros
	s.CacheSavingsMicros += o.CacheSavingsMicros
	ok := o.Outcome == harness.OutcomeOK || o.Outcome == harness.OutcomePartial
	if ok {
		m.failures[o.Provider] = 0
		if o.Latency > 0 {
			if s.LatencySamples == 0 {
				s.Latency = o.Latency
			} else {
				s.Latency = time.Duration(ewmaWeight*float64(o.Latency) + (1-ewmaWeight)*float64(s.Latency))
			}
			s.LatencySamples++
		}
		if o.PrefixID != "" && o.Usage.InputTokens > 0 {
			m.remember(cacheKey{o.Provider, o.PrefixID}, float64(o.Usage.CachedInputTokens)/float64(o.Usage.InputTokens))
		}
	} else if o.Outcome != harness.OutcomeCancelled {
		s.Failures++
		m.failures[o.Provider]++
		if m.failures[o.Provider] >= failureThreshold {
			m.until[o.Provider] = m.now().Add(cooldown)
			m.failures[o.Provider] = 0
		}
	}
	m.journal = append(m.journal, o)
	if len(m.journal) > journalSize {
		m.journal = append([]Observation(nil), m.journal[len(m.journal)-journalSize:]...)
	}
}

// remember keeps the most recent provider-reported cached fraction per prefix, bounded.
func (m *Meter) remember(k cacheKey, fraction float64) {
	if _, ok := m.cache[k]; !ok {
		m.order = append(m.order, k)
		if len(m.order) > cacheEvidenceSize {
			delete(m.cache, m.order[0])
			m.order = m.order[1:]
		}
	}
	m.cache[k] = fraction
}

// Stats returns a copy of one provider's account.
func (m *Meter) Stats(id harness.ProviderID) Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.stats[id]; s != nil {
		return *s
	}
	return Stats{}
}

// Latency is the smoothed latency of completed calls, or UnknownLatency without history.
func (m *Meter) Latency(id harness.ProviderID) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.stats[id]; s != nil && s.LatencySamples > 0 {
		return s.Latency
	}
	return UnknownLatency
}

// CooledDown reports whether a provider is paused after repeated failures.
func (m *Meter) CooledDown(id harness.ProviderID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.until[id]
	if !ok {
		return false
	}
	if !m.now().Before(until) {
		delete(m.until, id)
		return false
	}
	return true
}

// ObservedCachedFraction is the provider-reported cached share of input tokens on the
// most recent completed call with this prefix identity. It is zero when there is no such
// report, so a discount is never assumed.
func (m *Meter) ObservedCachedFraction(id harness.ProviderID, prefixID string) float64 {
	if prefixID == "" {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cache[cacheKey{id, prefixID}]
}

// Journal returns a copy of the most recent observations, oldest first.
func (m *Meter) Journal() []Observation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Observation(nil), m.journal...)
}

// Totals is the replayed account of a journal.
type Totals struct {
	Calls              int
	CostMicros         int64
	CacheSavingsMicros int64
	InputTokens        int64
	OutputTokens       int64
	CachedInputTokens  int64
}

// Replay recomputes cost and cache savings from recorded usage alone, using the given
// prices. A journal replayed under the prices in force when it was recorded reproduces the
// recorded totals exactly; under new prices it shows what the same traffic would cost.
func Replay(journal []Observation, prices map[harness.ProviderID]Pricing) Totals {
	var t Totals
	for _, o := range journal {
		p := prices[o.Provider]
		t.Calls++
		t.CostMicros += p.Cost(o.Usage)
		t.CacheSavingsMicros += p.CacheSavings(o.Usage, o.PrefixID)
		t.InputTokens += int64(o.Usage.InputTokens)
		t.OutputTokens += int64(o.Usage.OutputTokens)
		t.CachedInputTokens += int64(o.Usage.CachedInputTokens)
	}
	return t
}
