package observability

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"gopkg.in/yaml.v3"
)

func TestSkillLifecycleMetricsUseOnlyBoundedContentFreeLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewSkillLifecycleMetrics(registry)
	secret := "skill-customer-secret-content"
	for _, event := range []string{"propose", "evaluate", "approve", "canary", "acknowledge", "promote", "materialization", "complete", "disable", "rollback"} {
		metrics.Observe(SkillLifecycleObservation{Event: event, Outcome: "success", Duration: 25 * time.Millisecond})
	}
	metrics.Observe(SkillLifecycleObservation{Event: secret, Outcome: secret, Duration: time.Millisecond})
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
	if strings.Contains(text, secret) || strings.Contains(text, "skill_id") || strings.Contains(text, "revision_id") || !strings.Contains(text, `event="unknown"`) {
		t.Fatalf("skill metric labels are not bounded and content-free:\n%s", text)
	}
	for _, event := range []string{"propose", "evaluate", "approve", "canary", "acknowledge", "promote", "materialization", "complete", "disable", "rollback"} {
		if !strings.Contains(text, `event="`+event+`"`) {
			t.Fatalf("metric event %s is not registered", event)
		}
	}
}

func TestSkillLifecycleAlertFixtureIsLoadedAndRouted(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "saas", "observability", "skill-lifecycle-alerts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var rules struct {
		Groups []struct {
			Rules []struct {
				Alert, Expr string
				Labels      map[string]string
				Annotations map[string]string
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"AgentMemorySkillMaterializationFailures": false, "AgentMemorySkillRollbackSpike": false, "AgentMemorySkillEvaluationFailures": false,
		"AgentMemorySkillOrchestratorStuckReadyWork": false, "AgentMemorySkillOrchestratorLeaseChurn": false,
		"AgentMemorySkillOrchestratorDeadLetters": false, "AgentMemorySkillOrchestratorEvaluatorOutage": false,
		"AgentMemorySkillOrchestratorStaleCanary": false, "AgentMemorySkillOrchestratorRollbackFailure": false,
		"AgentMemorySkillOrchestratorReconciliationDrift": false, "AgentMemorySkillOrchestratorUnexpectedActivation": false,
	}
	for _, group := range rules.Groups {
		for _, rule := range group.Rules {
			if strings.Contains(rule.Expr, "skill_id") || strings.Contains(rule.Expr, "revision_id") || strings.Contains(rule.Expr, "workspace_id") || strings.Contains(rule.Expr, "tenant_id") {
				t.Fatalf("alert %s uses a content-bearing or unbounded label", rule.Alert)
			}
			switch rule.Alert {
			case "AgentMemorySkillOrchestratorStuckReadyWork", "AgentMemorySkillOrchestratorLeaseChurn", "AgentMemorySkillOrchestratorStaleCanary", "AgentMemorySkillOrchestratorRollbackFailure":
				if !strings.Contains(rule.Expr, "agent_memory_skill_orchestrator_slo_target") {
					t.Fatalf("alert %s is not bound to an approved configuration target", rule.Alert)
				}
			}
			if _, ok := wanted[rule.Alert]; ok && rule.Expr != "" && rule.Labels["owner"] == "agent-memory-operations" && strings.HasPrefix(rule.Annotations["runbook"], "docs/runbooks/skill-revision-lifecycle.md#") {
				wanted[rule.Alert] = true
			}
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("skill lifecycle alert %s is missing or unrouted", name)
		}
	}
	prometheusConfig, err := os.ReadFile(filepath.Join(root, "deploy", "saas", "observability", "prometheus.yaml"))
	if err != nil || !strings.Contains(string(prometheusConfig), "/etc/prometheus/skill-lifecycle-alerts.yaml") || !strings.Contains(string(prometheusConfig), `targets: ["skill-worker:9090"]`) {
		t.Fatal("Prometheus does not load skill lifecycle alerts")
	}
}
