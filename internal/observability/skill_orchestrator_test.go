package observability

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillOrchestratorMetricsRegisterRequiredFamiliesWithBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewSkillOrchestratorMetrics(registry)
	secret := "customer-skill-id-and-prompt"
	metrics.ObserveQueue(core.SkillOrchestratorStage(secret), secret, secret, 3, time.Second, time.Second, time.Second, time.Second, core.SkillJobFailureClass(secret))
	metrics.ObserveStage(core.SkillOrchestratorStage(secret), secret, secret, core.SkillJobFailureClass(secret), time.Second)
	metrics.ObserveRetry(core.SkillOrchestratorStage(secret), secret, core.SkillJobFailureClass(secret), true)
	metrics.ObserveLease(core.SkillOrchestratorStage(secret), secret, secret)
	metrics.ObserveReconciliation(core.SkillReconciliationDomain(secret), secret, secret, 1)
	metrics.ObserveCanary(secret, time.Second)
	metrics.ObserveSafety(secret, secret, secret, time.Second, time.Second)
	metrics.ObserveDrain(secret, secret)
	metrics.ObserveConfiguration(core.SkillOrchestratorMode(secret), secret)
	metrics.ObserveTarget(secret, secret, 10)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(&output, family); err != nil {
			t.Fatal(err)
		}
	}
	text := output.String()
	if strings.Contains(text, secret) || strings.Contains(text, "skill_id") || strings.Contains(text, "revision_id") {
		t.Fatalf("orchestrator metrics leaked unbounded content:\n%s", text)
	}
	for _, family := range []string{"queue_depth", "configuration_mode", "slo_target", "oldest_ready_age_seconds", "claim_delay_seconds", "running_age_seconds", "stage_duration_seconds", "retries_total", "blocked_age_seconds", "dead_letters_total", "lease_events_total", "reconciliation_total", "canary_wait_age_seconds", "safety_signals_total", "safety_disable_latency_seconds", "rollback_latency_seconds", "drain_outcomes_total"} {
		if !strings.Contains(text, "agent_memory_skill_orchestrator_"+family) {
			t.Fatalf("missing metric family %s", family)
		}
	}
}

func TestSkillOrchestratorMetricsRejectDuplicateRegistration(t *testing.T) {
	registry := prometheus.NewRegistry()
	NewSkillOrchestratorMetrics(registry)
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate metric registration must fail")
		}
	}()
	NewSkillOrchestratorMetrics(registry)
}
