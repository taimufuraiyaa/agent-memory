package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGraphObservabilityConfigurationIsInstalledAndContentSafe(t *testing.T) {
	root := graphRepositoryRoot(t)
	alerts := readGraphConfiguration(t, filepath.Join(root, "deploy", "saas", "observability", "graph-alerts.yaml"))
	var ruleFile struct {
		Groups []struct {
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Expression  string            `yaml:"expr"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(alerts, &ruleFile); err != nil {
		t.Fatal(err)
	}
	wantedAlerts := map[string]string{
		"AgentMemoryGraphRevisionStale":       "freshness",
		"AgentMemoryGraphQueueAgeHigh":        "queue",
		"AgentMemoryGraphJobFailures":         "failure",
		"AgentMemoryGraphDeadLetter":          "dead-letter",
		"AgentMemoryGraphHourlyCostHigh":      "cost",
		"AgentMemoryGraphStorageCapacityHigh": "capacity",
		"AgentMemoryGraphArtifactRejected":    "artifact-rejection",
	}
	seen := map[string]bool{}
	for _, group := range ruleFile.Groups {
		for _, rule := range group.Rules {
			if _, required := wantedAlerts[rule.Alert]; !required {
				continue
			}
			seen[rule.Alert] = true
			if strings.TrimSpace(rule.Expression) == "" || strings.TrimSpace(rule.Labels["severity"]) == "" || strings.TrimSpace(rule.Labels["owner"]) == "" || !strings.HasPrefix(rule.Annotations["runbook"], "docs/operations/graphrag") {
				t.Fatalf("graph alert %s is missing its expression, route, or runbook", rule.Alert)
			}
		}
	}
	for alert, purpose := range wantedAlerts {
		if !seen[alert] {
			t.Fatalf("graph %s alert %s is not installed", purpose, alert)
		}
	}

	prometheus := string(readGraphConfiguration(t, filepath.Join(root, "deploy", "saas", "observability", "prometheus.yaml")))
	if !strings.Contains(prometheus, "/etc/prometheus/graph-alerts.yaml") || !strings.Contains(prometheus, "agent-memory-graph-worker") || !strings.Contains(prometheus, "graph-worker:9090") {
		t.Fatal("Prometheus does not load graph alerts and scrape the graph worker")
	}

	dashboard := readGraphConfiguration(t, filepath.Join(root, "deploy", "saas", "observability", "graph-dashboard.json"))
	var parsedDashboard map[string]any
	if err := json.Unmarshal(dashboard, &parsedDashboard); err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{
		"agent_memory_graph_queue_age_seconds", "agent_memory_graph_operations_total",
		"agent_memory_graph_revision_age_seconds", "agent_memory_graph_records_total",
		"agent_memory_graph_coalesced_records_total",
		"agent_memory_graph_tokens_total", "agent_memory_graph_cost_microusd_total",
		"agent_memory_graph_storage_bytes", "agent_memory_graph_duration_seconds",
		"agent_memory_graph_fallbacks_total", "agent_memory_graph_cache_total",
		"agent_memory_graph_dead_letters_total", "agent_memory_graph_quality_feedback_total",
	} {
		if !strings.Contains(string(dashboard), metric) {
			t.Fatalf("graph dashboard is missing %s", metric)
		}
	}
	for _, forbidden := range []string{"source_text", "prompt_text", "entity_name", "report_summary", "memory_content"} {
		if strings.Contains(strings.ToLower(string(alerts)+string(dashboard)), forbidden) {
			t.Fatalf("content-bearing graph observability field %q is forbidden", forbidden)
		}
	}
}

func readGraphConfiguration(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func graphRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate graph observability test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
