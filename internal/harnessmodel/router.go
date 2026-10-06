package harnessmodel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

const (
	// MaxChain bounds how many providers one call may try, so fallback cannot fan out.
	MaxChain = 4

	ReasonUnavailable = "unavailable"
	ReasonClass       = "class"
	ReasonWindow      = "window"
	ReasonOutput      = "output"
	ReasonCost        = "cost"
	ReasonCooldown    = "cooldown"

	AdviceNone    = "none"
	AdviceApplied = "applied"
	AdviceInvalid = "ignored_invalid"
	AdviceLowConf = "ignored_low_confidence"
	AdviceFailed  = "failed"
	AdviceTimeout = "timeout"

	defaultMinConfidence = 0.6
	defaultAdviceTimeout = 2 * time.Second
)

var ErrNoEligible = errors.New("no eligible model provider")

// AccessProber reports a provider capability's live state. It must probe, not read a
// manifest: a catalog entry is configuration, not proof the account can use the model.
type AccessProber interface {
	Access(ctx context.Context, provider harness.ProviderID, capability harness.CapabilityID) harness.AccessState
}

// RegistryProber probes through real provider sessions and caches the answer briefly.
type RegistryProber struct {
	Registry  *harness.Registry
	Workspace string
	TTL       time.Duration
	Now       func() time.Time

	mu         sync.Mutex
	generation uint64
	cached     map[string]probed
}

type probed struct {
	state harness.AccessState
	at    time.Time
}

func (p *RegistryProber) Access(ctx context.Context, provider harness.ProviderID, capability harness.CapabilityID) harness.AccessState {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	ttl := p.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	key := string(provider) + "/" + string(capability)
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cached[key]; ok && now().Sub(c.at) < ttl {
		return c.state
	}
	if p.cached == nil {
		p.cached = map[string]probed{}
	}
	p.generation++
	state := harness.AccessUnavailable
	session, err := p.Registry.OpenSession(ctx, provider, harness.Scope{Workspace: p.Workspace, Run: "router-probe", Generation: p.generation})
	if err == nil {
		state = session.State(capability)
		_ = session.Close()
	}
	p.cached[key] = probed{state: state, at: now()}
	return state
}

// Advisor may prefer one eligible provider. It sees provider IDs only, never the task.
type Advisor interface {
	Prefer(ctx context.Context, providers []harness.ProviderID) (preferred harness.ProviderID, confidence float64, err error)
}

// SessionAdvisor asks a harness decision session. The question carries only provider IDs.
type SessionAdvisor struct {
	Session    *harness.Session
	Capability harness.CapabilityID
}

func (s SessionAdvisor) Prefer(ctx context.Context, providers []harness.ProviderID) (harness.ProviderID, float64, error) {
	if s.Session == nil {
		return "", 0, errors.New("no decision session")
	}
	envelope, err := s.Session.Envelope(s.Capability, 1024)
	if err != nil {
		return "", 0, err
	}
	ids := make([]string, len(providers))
	for i, p := range providers {
		ids[i] = string(p)
	}
	answer, err := s.Session.Decide(ctx, harness.DecisionQuestion{Envelope: envelope, Kind: "model", Candidates: ids})
	if err != nil {
		return "", 0, err
	}
	if answer.Outcome != harness.OutcomeOK || len(answer.Selected) != 1 {
		return "", 0, fmt.Errorf("decision outcome %s", answer.Outcome)
	}
	return harness.ProviderID(answer.Selected[0]), answer.Confidence, nil
}

// Need describes one model call: how sensitive the data is, how big the call is, and how
// much spend remains.
type Need struct {
	Class        Class
	InputTokens  int
	OutputTokens int
	PrefixID     string
	// HasSpendCap makes SpendCapMicros binding; without it spend is not a routing factor.
	HasSpendCap    bool
	SpendCapMicros int64
}

type Choice struct {
	Profile             Profile
	EstimatedCostMicros int64
	// Preferred marks a choice moved to the front by advice.
	Preferred bool
}

type Report struct {
	Considered int
	Excluded   map[string]int
	Advice     string
}

type RouterConfig struct {
	Profiles      []Profile
	Prober        AccessProber
	Meter         *Meter
	Advisor       Advisor
	MinConfidence float64
	AdviceTimeout time.Duration
}

type Router struct {
	profiles []Profile
	prober   AccessProber
	meter    *Meter
	advisor  Advisor
	minConf  float64
	timeout  time.Duration
}

func NewRouter(cfg RouterConfig) (*Router, error) {
	if cfg.Prober == nil || cfg.Meter == nil || len(cfg.Profiles) == 0 {
		return nil, fmt.Errorf("%w: prober, meter and at least one profile are required", ErrInvalid)
	}
	seen := map[harness.ProviderID]bool{}
	for _, p := range cfg.Profiles {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if seen[p.Provider] {
			return nil, fmt.Errorf("%w: duplicate provider %s", ErrInvalid, p.Provider)
		}
		seen[p.Provider] = true
	}
	r := &Router{profiles: append([]Profile(nil), cfg.Profiles...), prober: cfg.Prober, meter: cfg.Meter, advisor: cfg.Advisor,
		minConf: cfg.MinConfidence, timeout: cfg.AdviceTimeout}
	if r.minConf <= 0 || r.minConf > 1 {
		r.minConf = defaultMinConfidence
	}
	if r.timeout <= 0 {
		r.timeout = defaultAdviceTimeout
	}
	return r, nil
}

// Plan returns the ordered chain of eligible providers for a call, best first. A provider
// is eligible only if its capability is live now, it may be sent this data class, the
// call fits its window and output cap, it is not cooling down after failures, and its
// estimated cost fits any spend cap. Fallback is the rest of the chain, so a failure can
// only move to another eligible provider, never to one that was excluded.
func (r *Router) Plan(ctx context.Context, need Need) ([]Choice, Report, error) {
	report := Report{Excluded: map[string]int{}, Advice: AdviceNone}
	if !need.Class.valid() || need.InputTokens < 0 || need.OutputTokens < 1 {
		return nil, report, fmt.Errorf("%w: need", ErrInvalid)
	}
	var eligible []Choice
	for _, p := range r.profiles {
		report.Considered++
		switch {
		case r.meter.CooledDown(p.Provider):
			report.Excluded[ReasonCooldown]++
			continue
		case need.Class > p.MaxClass:
			report.Excluded[ReasonClass]++
			continue
		case need.OutputTokens > p.MaxOutputTokens:
			report.Excluded[ReasonOutput]++
			continue
		case need.InputTokens+need.OutputTokens > p.ContextTokens:
			report.Excluded[ReasonWindow]++
			continue
		}
		if r.prober.Access(ctx, p.Provider, p.Capability) != harness.AccessAvailable {
			report.Excluded[ReasonUnavailable]++
			continue
		}
		cost := r.estimate(p, need)
		if need.HasSpendCap && cost > need.SpendCapMicros {
			report.Excluded[ReasonCost]++
			continue
		}
		eligible = append(eligible, Choice{Profile: p, EstimatedCostMicros: cost})
	}
	if len(eligible) == 0 {
		return nil, report, ErrNoEligible
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].EstimatedCostMicros != eligible[j].EstimatedCostMicros {
			return eligible[i].EstimatedCostMicros < eligible[j].EstimatedCostMicros
		}
		if li, lj := r.meter.Latency(eligible[i].Profile.Provider), r.meter.Latency(eligible[j].Profile.Provider); li != lj {
			return li < lj
		}
		return eligible[i].Profile.Provider < eligible[j].Profile.Provider
	})
	if len(eligible) > MaxChain {
		eligible = eligible[:MaxChain]
	}
	if r.advisor != nil && len(eligible) > 1 {
		eligible = r.advise(ctx, eligible, &report)
	}
	return eligible, report, nil
}

// estimate prices the call, discounting cached input only by what this provider
// reported for this prefix before; with no report there is no discount.
func (r *Router) estimate(p Profile, need Need) int64 {
	cached := int(float64(need.InputTokens) * r.meter.ObservedCachedFraction(p.Provider, need.PrefixID))
	if cached > need.InputTokens {
		cached = need.InputTokens
	}
	return p.Pricing.Cost(harness.Usage{InputTokens: need.InputTokens, CachedInputTokens: cached, OutputTokens: need.OutputTokens})
}

// advise moves one advised provider to the front, only among eligible providers and only
// for a valid, confident answer; any failure leaves the deterministic order intact.
func (r *Router) advise(ctx context.Context, chain []Choice, report *Report) []Choice {
	ids := make([]harness.ProviderID, len(chain))
	for i, c := range chain {
		ids[i] = c.Profile.Provider
	}
	adviceCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	type answer struct {
		id       harness.ProviderID
		conf     float64
		err      error
		panicked bool
	}
	done := make(chan answer, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- answer{panicked: true}
			}
		}()
		id, conf, err := r.advisor.Prefer(adviceCtx, append([]harness.ProviderID(nil), ids...))
		done <- answer{id: id, conf: conf, err: err}
	}()
	var got answer
	select {
	case got = <-done:
	case <-adviceCtx.Done():
		report.Advice = AdviceTimeout
		return chain
	}
	if got.panicked || got.err != nil {
		report.Advice = AdviceFailed
		return chain
	}
	index := -1
	for i, id := range ids {
		if id == got.id {
			index = i
		}
	}
	if index < 0 || got.conf != got.conf || got.conf < 0 || got.conf > 1 {
		report.Advice = AdviceInvalid
		return chain
	}
	if got.conf < r.minConf {
		report.Advice = AdviceLowConf
		return chain
	}
	report.Advice = AdviceApplied
	if index == 0 {
		chain[0].Preferred = true
		return chain
	}
	preferred := chain[index]
	preferred.Preferred = true
	reordered := append([]Choice{preferred}, chain[:index]...)
	return append(reordered, chain[index+1:]...)
}

// Profile returns the configured profile for a provider.
func (r *Router) Profile(id harness.ProviderID) (Profile, bool) {
	for _, p := range r.profiles {
		if p.Provider == id {
			return p, true
		}
	}
	return Profile{}, false
}
