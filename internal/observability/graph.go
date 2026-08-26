package observability

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var ErrGraphLimitExceeded = errors.New("graph workspace limit exceeded")

type GraphWorkspaceLimits struct {
	MaxPendingRecords int64 `json:"max_pending_records"`
	MaxInputTokens    int64 `json:"max_input_tokens"`
	MaxCostMicroUSD   int64 `json:"max_cost_microusd"`
	MaxArtifactBytes  int64 `json:"max_artifact_bytes"`
}

type GraphPreflight struct {
	PendingRecords int64
	InputTokens    int64
	CostMicroUSD   int64
	ArtifactBytes  int64
}

func CheckGraphPreflight(limits GraphWorkspaceLimits, input GraphPreflight) error {
	checks := []struct {
		name         string
		value, limit int64
	}{{"pending_records", input.PendingRecords, limits.MaxPendingRecords}, {"input_tokens", input.InputTokens, limits.MaxInputTokens}, {"cost_microusd", input.CostMicroUSD, limits.MaxCostMicroUSD}, {"artifact_bytes", input.ArtifactBytes, limits.MaxArtifactBytes}}
	for _, check := range checks {
		if check.value < 0 || check.limit < 1 || check.value > check.limit {
			return fmt.Errorf("%w: %s", ErrGraphLimitExceeded, check.name)
		}
	}
	return nil
}

type GraphObservation struct {
	Stage             string
	Mode              string
	Route             string
	Outcome           string
	Reason            string
	Duration          time.Duration
	QueueAge          time.Duration
	RevisionAge       time.Duration
	Records           int64
	CoalescedRecords  int64
	Entities          int64
	Relationships     int64
	Rejected          int64
	InputTokens       int64
	CostMicroUSD      int64
	CacheHit          bool
	CacheObserved     bool
	Fallback          bool
	DeadLetter        bool
	ProjectionBytes   int64
	NormalizedBytes   int64
	AdapterStateBytes int64
	CacheBytes        int64
	Freshness         string
	FeedbackOutcome   string
}

type GraphMetrics struct {
	operations      *prometheus.CounterVec
	duration        *prometheus.HistogramVec
	queueAge        prometheus.Histogram
	revisionAge     *prometheus.GaugeVec
	records         *prometheus.CounterVec
	coalesced       *prometheus.CounterVec
	tokens          *prometheus.CounterVec
	cost            *prometheus.CounterVec
	cache           *prometheus.CounterVec
	fallbacks       *prometheus.CounterVec
	deadLetters     prometheus.Counter
	storage         *prometheus.GaugeVec
	qualityFeedback *prometheus.CounterVec
}

func NewGraphMetrics(registerer prometheus.Registerer) *GraphMetrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	m := &GraphMetrics{
		operations:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_operations_total", Help: "Content-free graph operation outcomes."}, []string{"stage", "mode", "outcome"}),
		duration:        prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_graph_duration_seconds", Help: "Graph indexing and query latency by bounded stage and route.", Buckets: []float64{.005, .01, .025, .05, .075, .1, .25, .5, 1, 5, 30, 300, 3600}}, []string{"stage", "route"}),
		queueAge:        prometheus.NewHistogram(prometheus.HistogramOpts{Name: "agent_memory_graph_queue_age_seconds", Help: "Age of graph jobs when claimed.", Buckets: []float64{1, 10, 30, 60, 300, 900, 3600, 21600}}),
		revisionAge:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_graph_revision_age_seconds", Help: "Age of the active derived revision by freshness state."}, []string{"freshness"}),
		records:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_records_total", Help: "Projection, extraction, and rejection counts without content."}, []string{"kind", "outcome"}),
		coalesced:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_coalesced_records_total", Help: "Canonical changes coalesced into graph jobs by mode."}, []string{"mode"}),
		tokens:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_tokens_total", Help: "Graph indexing tokens by mode."}, []string{"mode"}),
		cost:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_cost_microusd_total", Help: "Graph indexing cost in integer millionths of USD."}, []string{"mode"}),
		cache:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_cache_total", Help: "Graph cache outcomes by route."}, []string{"route", "outcome"}),
		fallbacks:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_fallbacks_total", Help: "Graph-to-Basic fallbacks by bounded reason."}, []string{"route", "reason"}),
		deadLetters:     prometheus.NewCounter(prometheus.CounterOpts{Name: "agent_memory_graph_dead_letters_total", Help: "Graph jobs entering dead letter state."}),
		storage:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_graph_storage_bytes", Help: "Graph derived storage bytes by custody class."}, []string{"class"}),
		qualityFeedback: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_graph_quality_feedback_total", Help: "Bounded graph feedback by route and outcome."}, []string{"route", "outcome"}),
	}
	registerer.MustRegister(m.operations, m.duration, m.queueAge, m.revisionAge, m.records, m.coalesced, m.tokens, m.cost, m.cache, m.fallbacks, m.deadLetters, m.storage, m.qualityFeedback)
	return m
}

func (m *GraphMetrics) Observe(value GraphObservation) error {
	if m == nil {
		return errors.New("graph metrics are unavailable")
	}
	stage, mode, route, outcome := boundedGraphLabel(value.Stage, graphStages), boundedGraphLabel(value.Mode, graphModes), boundedGraphLabel(value.Route, graphRoutes), boundedGraphLabel(value.Outcome, graphOutcomes)
	reason := ""
	if value.Fallback {
		reason = boundedGraphLabel(value.Reason, graphReasons)
	}
	feedback := ""
	if strings.TrimSpace(value.FeedbackOutcome) != "" {
		feedback = boundedGraphLabel(value.FeedbackOutcome, graphFeedback)
	}
	freshness := ""
	if strings.TrimSpace(value.Freshness) != "" {
		freshness = boundedGraphLabel(value.Freshness, graphFreshness)
	}
	if stage == "" || mode == "" || route == "" || outcome == "" || (value.Fallback && reason == "") || (value.FeedbackOutcome != "" && feedback == "") || (value.Freshness != "" && freshness == "") || value.Duration < 0 || value.QueueAge < 0 || value.RevisionAge < 0 || minGraphCount(value.Records, value.CoalescedRecords, value.Entities, value.Relationships, value.Rejected, value.InputTokens, value.CostMicroUSD, value.ProjectionBytes, value.NormalizedBytes, value.AdapterStateBytes, value.CacheBytes) < 0 {
		return errors.New("graph observation is outside policy")
	}
	m.operations.WithLabelValues(stage, mode, outcome).Inc()
	if value.Duration > 0 {
		m.duration.WithLabelValues(stage, route).Observe(value.Duration.Seconds())
	}
	if value.QueueAge > 0 {
		m.queueAge.Observe(value.QueueAge.Seconds())
	}
	if value.RevisionAge > 0 {
		if freshness == "" {
			freshness = "unknown"
		}
		m.revisionAge.WithLabelValues(freshness).Set(value.RevisionAge.Seconds())
	}
	for kind, count := range map[string]int64{"projection": value.Records, "entity": value.Entities, "relationship": value.Relationships, "rejected": value.Rejected} {
		if count > 0 {
			m.records.WithLabelValues(kind, outcome).Add(float64(count))
		}
	}
	if value.CoalescedRecords > 0 {
		m.coalesced.WithLabelValues(mode).Add(float64(value.CoalescedRecords))
	}
	if value.InputTokens > 0 {
		m.tokens.WithLabelValues(mode).Add(float64(value.InputTokens))
	}
	if value.CostMicroUSD > 0 {
		m.cost.WithLabelValues(mode).Add(float64(value.CostMicroUSD))
	}
	if value.CacheObserved {
		cacheOutcome := "miss"
		if value.CacheHit {
			cacheOutcome = "hit"
		}
		m.cache.WithLabelValues(route, cacheOutcome).Inc()
	}
	if value.Fallback {
		m.fallbacks.WithLabelValues(route, reason).Inc()
	}
	if value.DeadLetter {
		m.deadLetters.Inc()
	}
	for class, size := range map[string]int64{"projection": value.ProjectionBytes, "normalized": value.NormalizedBytes, "adapter_state": value.AdapterStateBytes, "cache": value.CacheBytes} {
		if size > 0 {
			m.storage.WithLabelValues(class).Set(float64(size))
		}
	}
	if feedback != "" {
		m.qualityFeedback.WithLabelValues(route, feedback).Inc()
	}
	return nil
}

func minGraphCount(values ...int64) int64 {
	result := int64(0)
	for _, value := range values {
		if value < result {
			result = value
		}
	}
	return result
}
func boundedGraphLabel(value string, allowed map[string]struct{}) string {
	value = strings.TrimSpace(value)
	if _, ok := allowed[value]; ok {
		return value
	}
	return ""
}

var graphStages = setGraphLabels("projection", "index", "import", "query", "review", "deletion", "restore")
var graphModes = setGraphLabels("full", "incremental", "basic", "local_graph", "global")
var graphRoutes = setGraphLabels("none", "basic", "local_graph", "global")
var graphOutcomes = setGraphLabels("queued", "running", "completed", "failed", "cancelled", "rejected", "fallback")
var graphReasons = setGraphLabels("policy_disabled", "mode_disallowed", "index_unavailable", "index_stale", "read_failed", "artifact_rejected", "limit_exceeded")
var graphFeedback = setGraphLabels("helpful", "ignored", "rejected", "harmful")
var graphFreshness = setGraphLabels("fresh", "stale", "degraded", "unknown")

func setGraphLabels(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

var defaultGraphMetrics *GraphMetrics
var defaultGraphMetricsOnce sync.Once

func GetGraphMetrics() *GraphMetrics {
	defaultGraphMetricsOnce.Do(func() { defaultGraphMetrics = NewGraphMetrics(prometheus.DefaultRegisterer) })
	return defaultGraphMetrics
}
