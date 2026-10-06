package harnessdecide

import "sync"

// DefaultMaxRuns is how many runs' services a Hub remembers.
const DefaultMaxRuns = 64

// Hub hands out one Service per run, so each run has its own decision budget, while every
// run shares what really is shared: the provider's health, the bound on requests in flight
// and the rate at which they start. The oldest run's service is forgotten when more than
// the bound are active, which gives a run that returns after that a fresh budget; the bound
// should therefore be above the number of runs that can be active at once.
type Hub struct {
	mu       sync.Mutex
	cfg      Config
	health   *Health
	services map[string]*Service
	order    []string
	max      int
}

// NewHub validates a configuration once and makes services from it on demand. An empty run
// identifier names a shared service for components that are not tied to a run.
func NewHub(cfg Config, maxRuns int) (*Hub, error) {
	if maxRuns < 0 {
		maxRuns = 0
	}
	if maxRuns == 0 {
		maxRuns = DefaultMaxRuns
	}
	first, err := New(cfg)
	if err != nil {
		return nil, err
	}
	h := &Hub{cfg: first.cfg, health: first.health, services: map[string]*Service{}, max: maxRuns}
	h.cfg.Health = first.health
	h.cfg.Now = first.now
	h.cfg.limiter = first.lim
	return h, nil
}

// Health is the record every service of the hub reports into.
func (h *Hub) Health() *Health { return h.health }

// Service returns the run's service, making it on first use.
func (h *Hub) Service(runID string) *Service {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.services[runID]; ok {
		return s
	}
	s, _ := New(h.cfg) // the configuration was validated in NewHub
	h.services[runID] = s
	h.order = append(h.order, runID)
	for len(h.order) > h.max {
		delete(h.services, h.order[0])
		h.order = h.order[1:]
	}
	return s
}
