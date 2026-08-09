package security

import (
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"testing"
	"time"
)

func TestInitialRulesProduceExplainableEvidence(t *testing.T) {
	now := time.Date(2026, 8, 5, 3, 0, 0, 0, time.UTC)
	events := []audit.Event{{TenantID: "tenant", ID: "event", OccurredAt: now, Outcome: "denied", ReasonCode: "cross_tenant", RiskSignals: []string{"tenant_mismatch"}}}
	findings := Evaluate(events, now)
	if len(findings) != 1 || findings[0].Severity != Critical || len(findings[0].Evidence) != 1 || findings[0].Evidence[0].EventID != "event" {
		t.Fatalf("findings=%+v", findings)
	}
}
