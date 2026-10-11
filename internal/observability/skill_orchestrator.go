package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var defaultSkillOrchestratorMetricsOnce sync.Once
var defaultSkillOrchestratorMetrics *SkillOrchestratorMetrics

func DefaultSkillOrchestratorMetrics() *SkillOrchestratorMetrics {
	defaultSkillOrchestratorMetricsOnce.Do(func() { defaultSkillOrchestratorMetrics = NewSkillOrchestratorMetrics(prometheus.DefaultRegisterer) })
	return defaultSkillOrchestratorMetrics
}

type SkillOrchestratorMetrics struct {
	queueDepth           *prometheus.GaugeVec
	configurationMode    *prometheus.GaugeVec
	sloTarget            *prometheus.GaugeVec
	oldestReadyAge       *prometheus.GaugeVec
	claimDelay           *prometheus.HistogramVec
	runningAge           *prometheus.HistogramVec
	stageDuration        *prometheus.HistogramVec
	retries              *prometheus.CounterVec
	blockedAge           *prometheus.HistogramVec
	deadLetters          *prometheus.CounterVec
	leaseEvents          *prometheus.CounterVec
	reconciliation       *prometheus.CounterVec
	canaryWaitAge        *prometheus.GaugeVec
	safetySignals        *prometheus.CounterVec
	safetyDisableLatency *prometheus.HistogramVec
	rollbackLatency      *prometheus.HistogramVec
	drainOutcomes        *prometheus.CounterVec
}

func NewSkillOrchestratorMetrics(registerer prometheus.Registerer) *SkillOrchestratorMetrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	labels := []string{"stage", "environment"}
	metrics := &SkillOrchestratorMetrics{
		queueDepth:           prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_skill_orchestrator_queue_depth", Help: "Ready or blocked skill jobs by bounded stage, state, and environment."}, []string{"stage", "state", "environment"}),
		configurationMode:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_skill_orchestrator_configuration_mode", Help: "Active content-free orchestrator mode by bounded mode and environment."}, []string{"mode", "environment"}),
		sloTarget:            prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_skill_orchestrator_slo_target", Help: "Approved orchestrator target value by bounded target and environment."}, []string{"target", "environment"}),
		oldestReadyAge:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_skill_orchestrator_oldest_ready_age_seconds", Help: "Age of the oldest ready job by bounded stage and environment."}, labels),
		claimDelay:           prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_orchestrator_claim_delay_seconds", Help: "Delay from ready to claim by bounded stage and environment."}, labels),
		runningAge:           prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_orchestrator_running_age_seconds", Help: "Observed running age by bounded stage and environment."}, labels),
		stageDuration:        prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_orchestrator_stage_duration_seconds", Help: "Stage execution duration by bounded stage, outcome, environment, and failure class."}, []string{"stage", "outcome", "environment", "failure_class"}),
		retries:              prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_orchestrator_retries_total", Help: "Retry decisions by bounded stage, environment, and failure class."}, []string{"stage", "environment", "failure_class"}),
		blockedAge:           prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_orchestrator_blocked_age_seconds", Help: "Blocked job age by bounded stage, environment, and failure class."}, []string{"stage", "environment", "failure_class"}),
		deadLetters:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_orchestrator_dead_letters_total", Help: "Dead-letter decisions by bounded stage, environment, and failure class."}, []string{"stage", "environment", "failure_class"}),
		leaseEvents:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_orchestrator_lease_events_total", Help: "Lease expiry or renewal-failure events by bounded stage and environment."}, []string{"stage", "event", "environment"}),
		reconciliation:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_orchestrator_reconciliation_total", Help: "Reconciliation outcomes by bounded domain and environment."}, []string{"domain", "outcome", "environment"}),
		canaryWaitAge:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "agent_memory_skill_orchestrator_canary_wait_age_seconds", Help: "Current canary wait age by bounded environment."}, []string{"environment"}),
		safetySignals:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_orchestrator_safety_signals_total", Help: "Verified safety signal outcomes by bounded severity and environment."}, []string{"severity", "outcome", "environment"}),
		safetyDisableLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_orchestrator_safety_disable_latency_seconds", Help: "Latency from verified hard signal to allocation disablement by environment."}, []string{"environment"}),
		rollbackLatency:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agent_memory_skill_orchestrator_rollback_latency_seconds", Help: "Latency from verified hard signal to rollback outcome by bounded environment and outcome."}, []string{"environment", "outcome"}),
		drainOutcomes:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_memory_skill_orchestrator_drain_outcomes_total", Help: "Worker drain outcomes by bounded environment and outcome."}, []string{"environment", "outcome"}),
	}
	registerer.MustRegister(metrics.queueDepth, metrics.configurationMode, metrics.sloTarget, metrics.oldestReadyAge, metrics.claimDelay, metrics.runningAge, metrics.stageDuration, metrics.retries, metrics.blockedAge, metrics.deadLetters, metrics.leaseEvents, metrics.reconciliation, metrics.canaryWaitAge, metrics.safetySignals, metrics.safetyDisableLatency, metrics.rollbackLatency, metrics.drainOutcomes)
	return metrics
}

var skillOrchestratorStages = boundedSet("detect", "build", "evaluate", "decide", "start_canary", "analyze_canary", "activate", "observe_safety", "rollback", "reconcile_materialization", "unknown")
var skillOrchestratorEnvironments = boundedSet("local", "development", "staging", "production", "test", "unknown")
var skillOrchestratorOutcomes = boundedSet("success", "failure", "rejected", "blocked", "cancelled", "timeout", "unknown")
var skillOrchestratorFailures = boundedSet("none", "contention", "dependency_unavailable", "insufficient_evidence", "policy_block", "permanent_validation", "safety_rejection", "cancellation", "unknown_internal", "unknown")
var skillOrchestratorStates = boundedSet("queued", "blocked", "running", "retry_wait", "dead_lettered", "unknown")
var skillOrchestratorLeaseEvents = boundedSet("expired", "renewal_failed", "unknown")
var skillOrchestratorReconciliationDomains = boundedSet("lease_recovery", "dependency_readiness", "lifecycle_job_parity", "blocked_rechecks", "safety_rollback_parity", "materialization_drift", "terminal_cleanup", "unknown")
var skillOrchestratorReconciliationOutcomes = boundedSet("scanned", "repaired", "skipped", "blocked", "failed", "unknown")
var skillOrchestratorSeverities = boundedSet("soft", "hard", "unknown")
var skillOrchestratorModes = boundedSet("disabled", "shadow", "manual", "canary", "automatic_low_risk", "unknown")
var skillOrchestratorTargets = boundedSet("ready_queue_stuck_seconds", "lease_failure_count", "canary_stale_seconds", "rollback_failure_seconds", "unknown")

func (m *SkillOrchestratorMetrics) ObserveStage(stage core.SkillOrchestratorStage, environment, outcome string, failure core.SkillJobFailureClass, duration time.Duration) {
	if m == nil {
		return
	}
	m.stageDuration.WithLabelValues(boundedOrchestratorLabel(string(stage), skillOrchestratorStages), boundedOrchestratorLabel(outcome, skillOrchestratorOutcomes), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments), boundedFailureLabel(failure)).Observe(nonNegativeSeconds(duration))
}

func (m *SkillOrchestratorMetrics) ObserveRetry(stage core.SkillOrchestratorStage, environment string, failure core.SkillJobFailureClass, deadLetter bool) {
	if m == nil {
		return
	}
	labels := []string{boundedOrchestratorLabel(string(stage), skillOrchestratorStages), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments), boundedFailureLabel(failure)}
	m.retries.WithLabelValues(labels...).Inc()
	if deadLetter {
		m.deadLetters.WithLabelValues(labels...).Inc()
	}
}

func (m *SkillOrchestratorMetrics) ObserveQueue(stage core.SkillOrchestratorStage, state, environment string, depth int, oldestReady, claimDelay, runningAge, blockedAge time.Duration, failure core.SkillJobFailureClass) {
	if m == nil {
		return
	}
	stageLabel, environmentLabel := boundedOrchestratorLabel(string(stage), skillOrchestratorStages), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)
	m.queueDepth.WithLabelValues(stageLabel, boundedOrchestratorLabel(state, skillOrchestratorStates), environmentLabel).Set(float64(max(depth, 0)))
	if oldestReady >= 0 {
		m.oldestReadyAge.WithLabelValues(stageLabel, environmentLabel).Set(oldestReady.Seconds())
	}
	if claimDelay >= 0 {
		m.claimDelay.WithLabelValues(stageLabel, environmentLabel).Observe(claimDelay.Seconds())
	}
	if runningAge >= 0 {
		m.runningAge.WithLabelValues(stageLabel, environmentLabel).Observe(runningAge.Seconds())
	}
	if blockedAge >= 0 {
		m.blockedAge.WithLabelValues(stageLabel, environmentLabel, boundedFailureLabel(failure)).Observe(blockedAge.Seconds())
	}
}

func (m *SkillOrchestratorMetrics) ResetQueue(environment string) {
	if m == nil {
		return
	}
	environment = boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)
	for stage := range skillOrchestratorStages {
		if stage == "unknown" {
			continue
		}
		m.oldestReadyAge.WithLabelValues(stage, environment).Set(0)
		for state := range skillOrchestratorStates {
			if state != "unknown" {
				m.queueDepth.WithLabelValues(stage, state, environment).Set(0)
			}
		}
	}
}

func (m *SkillOrchestratorMetrics) ObserveLease(stage core.SkillOrchestratorStage, event, environment string) {
	if m == nil {
		return
	}
	m.leaseEvents.WithLabelValues(boundedOrchestratorLabel(string(stage), skillOrchestratorStages), boundedOrchestratorLabel(event, skillOrchestratorLeaseEvents), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)).Inc()
}

func (m *SkillOrchestratorMetrics) ObserveClaim(stage core.SkillOrchestratorStage, environment string, delay time.Duration) {
	if m == nil {
		return
	}
	m.claimDelay.WithLabelValues(boundedOrchestratorLabel(string(stage), skillOrchestratorStages), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)).Observe(nonNegativeSeconds(delay))
}

func (m *SkillOrchestratorMetrics) ObserveReconciliation(domain core.SkillReconciliationDomain, outcome, environment string, count int64) {
	if m == nil || count <= 0 {
		return
	}
	m.reconciliation.WithLabelValues(boundedOrchestratorLabel(string(domain), skillOrchestratorReconciliationDomains), boundedOrchestratorLabel(outcome, skillOrchestratorReconciliationOutcomes), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)).Add(float64(count))
}

func (m *SkillOrchestratorMetrics) ObserveCanary(environment string, waitAge time.Duration) {
	if m == nil {
		return
	}
	m.canaryWaitAge.WithLabelValues(boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)).Set(nonNegativeSeconds(waitAge))
}

func (m *SkillOrchestratorMetrics) ObserveSafety(severity, outcome, environment string, disableLatency, rollbackLatency time.Duration) {
	if m == nil {
		return
	}
	environmentLabel := boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)
	m.safetySignals.WithLabelValues(boundedOrchestratorLabel(severity, skillOrchestratorSeverities), boundedOrchestratorLabel(outcome, skillOrchestratorOutcomes), environmentLabel).Inc()
	if disableLatency >= 0 {
		m.safetyDisableLatency.WithLabelValues(environmentLabel).Observe(disableLatency.Seconds())
	}
	if rollbackLatency >= 0 {
		m.rollbackLatency.WithLabelValues(environmentLabel, boundedOrchestratorLabel(outcome, skillOrchestratorOutcomes)).Observe(rollbackLatency.Seconds())
	}
}

func (m *SkillOrchestratorMetrics) ObserveRollback(environment, outcome string, latency time.Duration) {
	if m == nil {
		return
	}
	m.rollbackLatency.WithLabelValues(boundedOrchestratorLabel(environment, skillOrchestratorEnvironments), boundedOrchestratorLabel(outcome, skillOrchestratorOutcomes)).Observe(nonNegativeSeconds(latency))
}

func (m *SkillOrchestratorMetrics) ObserveDrain(environment, outcome string) {
	if m == nil {
		return
	}
	m.drainOutcomes.WithLabelValues(boundedOrchestratorLabel(environment, skillOrchestratorEnvironments), boundedOrchestratorLabel(outcome, skillOrchestratorOutcomes)).Inc()
}

func (m *SkillOrchestratorMetrics) ObserveConfiguration(mode core.SkillOrchestratorMode, environment string) {
	if m == nil {
		return
	}
	environment = boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)
	active := boundedOrchestratorLabel(string(mode), skillOrchestratorModes)
	for candidate := range skillOrchestratorModes {
		if candidate == "unknown" {
			continue
		}
		value := 0.0
		if candidate == active {
			value = 1
		}
		m.configurationMode.WithLabelValues(candidate, environment).Set(value)
	}
}

func (m *SkillOrchestratorMetrics) ObserveTarget(target, environment string, value float64) {
	if m == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	m.sloTarget.WithLabelValues(boundedOrchestratorLabel(target, skillOrchestratorTargets), boundedOrchestratorLabel(environment, skillOrchestratorEnvironments)).Set(value)
}

func boundedFailureLabel(value core.SkillJobFailureClass) string {
	label := string(value)
	if label == "" {
		label = "none"
	}
	return boundedOrchestratorLabel(label, skillOrchestratorFailures)
}

func boundedOrchestratorLabel(value string, allowed map[string]struct{}) string {
	if _, ok := allowed[value]; ok {
		return value
	}
	return "unknown"
}

func boundedSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func nonNegativeSeconds(value time.Duration) float64 {
	if value < 0 {
		return 0
	}
	return value.Seconds()
}
