package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type SkillLifecycleObservation struct {
	Event    string
	Outcome  string
	Duration time.Duration
}

type SkillLifecycleMetrics struct {
	events   *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

var skillEvents = map[string]struct{}{
	"propose": {}, "evaluate": {}, "approve": {}, "canary": {}, "acknowledge": {},
	"promote": {}, "materialization": {}, "complete": {}, "disable": {}, "rollback": {},
	"resolve": {}, "pin": {}, "unknown": {},
}
var skillOutcomes = map[string]struct{}{"success": {}, "failure": {}, "rejected": {}, "skipped": {}, "unknown": {}}

func NewSkillLifecycleMetrics(registerer prometheus.Registerer) *SkillLifecycleMetrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	metrics := &SkillLifecycleMetrics{
		events:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_lifecycle_events_total", Help: "Content-free skill lifecycle outcomes by bounded event."}, []string{"event", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_lifecycle_duration_seconds", Help: "Skill lifecycle latency by bounded event and outcome.", Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 5, 30, 300}}, []string{"event", "outcome"}),
	}
	registerer.MustRegister(metrics.events, metrics.duration)
	return metrics
}

func (m *SkillLifecycleMetrics) Observe(value SkillLifecycleObservation) {
	if m == nil {
		return
	}
	event, outcome := boundedSkillMetricLabel(value.Event, skillEvents), boundedSkillMetricLabel(value.Outcome, skillOutcomes)
	m.events.WithLabelValues(event, outcome).Inc()
	if value.Duration >= 0 {
		m.duration.WithLabelValues(event, outcome).Observe(value.Duration.Seconds())
	}
}

func (m *SkillLifecycleMetrics) ObserveSkillMaterialization(outcome string, duration time.Duration) {
	m.Observe(SkillLifecycleObservation{Event: "materialization", Outcome: outcome, Duration: duration})
}

func boundedSkillMetricLabel(value string, allowed map[string]struct{}) string {
	if _, ok := allowed[value]; ok {
		return value
	}
	return "unknown"
}

var defaultSkillMetricsOnce sync.Once
var defaultSkillMetrics *SkillLifecycleMetrics

func DefaultSkillLifecycleMetrics() *SkillLifecycleMetrics {
	defaultSkillMetricsOnce.Do(func() { defaultSkillMetrics = NewSkillLifecycleMetrics(prometheus.DefaultRegisterer) })
	return defaultSkillMetrics
}
