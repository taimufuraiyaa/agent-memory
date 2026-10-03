package observability

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
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
