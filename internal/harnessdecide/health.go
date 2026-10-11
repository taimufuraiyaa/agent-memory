package harnessdecide

import (
	"sort"
	"sync"
	"time"
)

const (
	failureThreshold = 3
	baseCooldown     = 10 * time.Second
	maxCooldown      = 5 * time.Minute
	// maxAbandoned bounds the calls whose caller gave up but whose provider has not returned,
	// so a provider that ignores cancellation cannot grow the process without limit.
	maxAbandoned = 8
	maxEvents    = 32
)

// breaker is a half-open circuit: after repeated failures it refuses for a cooldown, then
// lets exactly one probe through, and a failed probe doubles the cooldown up to a cap.
type breaker struct {
	failures int
	open     bool
	probing  bool
	until    time.Time
	cooldown time.Duration
}

// allow reports whether a request may go; probe says it is the single trial after a pause.
func (b *breaker) allow(now time.Time) (ok, probe bool) {
	if !b.open {
		return true, false
	}
	if now.Before(b.until) || b.probing {
		return false, false
	}
	b.probing = true
	return true, true
}

func (b *breaker) success() { *b = breaker{} }

func (b *breaker) failure(now time.Time) {
	if b.open { // the probe failed
		b.cooldown = min(b.cooldown*2, maxCooldown)
		b.until, b.probing = now.Add(b.cooldown), false
		return
	}
	b.failures++
	if b.failures >= failureThreshold {
		b.open, b.cooldown = true, baseCooldown
		b.until = now.Add(b.cooldown)
	}
}

// release ends a probe that reached no verdict, such as a cancelled call, so the next
// request can try again.
func (b *breaker) release() { b.probing = false }

// Event is one request in the journal. It holds nothing about what was asked.
type Event struct {
	Kind       Kind
	Status     Status
	Latency    string
	Candidates int
	At         time.Time
}

// Snapshot is the content-free view of a service's health: counts per kind and status, the
// most recent events, and what is paused.
type Snapshot struct {
	Counts         map[Kind]map[Status]int64
	Recent         []Event
	ProviderPaused time.Time // zero when requests are going out
	PausedKinds    map[Kind]time.Time
	Abandoned      int
}

// Health is shared by every run that uses one provider, because the provider's health does
// not depend on which run noticed it.
type Health struct {
	mu        sync.Mutex
	now       func() time.Time
	provider  breaker
	kinds     map[Kind]*breaker
	counts    map[Kind]map[Status]int64
	events    []Event
	abandoned int
}

// NewHealth returns an empty record. A nil clock uses the real one.
func NewHealth(now func() time.Time) *Health {
	if now == nil {
		now = time.Now
	}
	return &Health{now: now, kinds: map[Kind]*breaker{}, counts: map[Kind]map[Status]int64{}}
}

// permit is the right to make one request, and what to settle afterwards.
type permit struct{ providerProbe, kindProbe bool }

// admit decides whether a request for a kind may go out now.
func (h *Health) admit(kind Kind) (permit, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if h.abandoned >= maxAbandoned {
		return permit{}, false
	}
	var p permit
	ok, probe := h.provider.allow(now)
	if !ok {
		return permit{}, false
	}
	p.providerProbe = probe
	kb := h.kinds[kind]
	if kb == nil {
		kb = &breaker{}
		h.kinds[kind] = kb
	}
	ok, probe = kb.allow(now)
	if !ok {
		if p.providerProbe {
			h.provider.release()
		}
		return permit{}, false
	}
	p.kindProbe = probe
	return p, true
}

// settle records what a permitted request came to. A valid answer, useful or not, shows the
// provider is healthy; only a malformed one counts against the kind; a fault counts against
// the provider; and a request that reached no verdict only gives its probe back.
func (h *Health) settle(kind Kind, p permit, status Status) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	kb := h.kinds[kind]
	switch {
	case status == StatusApplied || status == StatusLowConfidence:
		h.provider.success()
		kb.success()
	case status == StatusInvalid:
		h.provider.success()
		kb.failure(now)
	case providerFault(status):
		h.provider.failure(now)
		if p.kindProbe {
			kb.release()
		}
	default:
		if p.providerProbe {
			h.provider.release()
		}
		if p.kindProbe {
			kb.release()
		}
	}
}

func latencyBucket(d time.Duration) string {
	switch {
	case d < 250*time.Millisecond:
		return "lt250ms"
	case d < time.Second:
		return "lt1s"
	case d < 2*time.Second:
		return "lt2s"
	}
	return "ge2s"
}

// record adds one request to the counts and the recent events.
func (h *Health) record(o Outcome) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts[o.Kind] == nil {
		h.counts[o.Kind] = map[Status]int64{}
	}
	h.counts[o.Kind][o.Status]++
	h.events = append(h.events, Event{Kind: o.Kind, Status: o.Status, Latency: latencyBucket(o.Latency), Candidates: o.Asked, At: h.now()})
	if len(h.events) > maxEvents {
		h.events = append([]Event(nil), h.events[len(h.events)-maxEvents:]...)
	}
}

func (h *Health) abandon()  { h.mu.Lock(); h.abandoned++; h.mu.Unlock() }
func (h *Health) returned() { h.mu.Lock(); h.abandoned--; h.mu.Unlock() }

// Snapshot copies the current state.
func (h *Health) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	s := Snapshot{Counts: map[Kind]map[Status]int64{}, PausedKinds: map[Kind]time.Time{}, Abandoned: h.abandoned}
	for kind, byStatus := range h.counts {
		copied := make(map[Status]int64, len(byStatus))
		for status, n := range byStatus {
			copied[status] = n
		}
		s.Counts[kind] = copied
	}
	s.Recent = append([]Event(nil), h.events...)
	if h.provider.open && now.Before(h.provider.until) {
		s.ProviderPaused = h.provider.until
	}
	for kind, b := range h.kinds {
		if b.open && now.Before(b.until) {
			s.PausedKinds[kind] = b.until
		}
	}
	return s
}

// PausedKindList lists the kinds currently paused, in a fixed order.
func (s Snapshot) PausedKindList() []Kind {
	out := make([]Kind, 0, len(s.PausedKinds))
	for kind := range s.PausedKinds {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
