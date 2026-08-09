package security

import (
	"sort"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
)

type Rule struct {
	ID          string
	Severity    Severity
	Window      time.Duration
	Threshold   int
	Match       func(audit.Event) bool
	SummaryCode string
}

func InitialRules() []Rule {
	return []Rule{
		{ID: "cross_tenant_authorization", Severity: Critical, Window: 5 * time.Minute, Threshold: 1, SummaryCode: "cross_tenant_attempt", Match: func(e audit.Event) bool {
			return e.Outcome == "denied" && (e.ReasonCode == "cross_tenant" || contains(e.RiskSignals, "tenant_mismatch"))
		}},
		{ID: "resource_enumeration", Severity: High, Window: 10 * time.Minute, Threshold: 20, SummaryCode: "resource_probe_burst", Match: func(e audit.Event) bool { return e.Outcome == "denied" && e.TargetID != "" }},
		{ID: "upload_volume_spike", Severity: High, Window: 10 * time.Minute, Threshold: 20, SummaryCode: "upload_job_burst", Match: func(e audit.Event) bool {
			return e.Operation == "source.upload_grant" || e.Operation == "source.upload_complete"
		}},
		{ID: "pipeline_rejection_burst", Severity: High, Window: time.Hour, Threshold: 3, SummaryCode: "unsafe_upload_rejections", Match: func(e audit.Event) bool {
			return contains(e.RiskSignals, "malware") || contains(e.RiskSignals, "invalid_container") || contains(e.RiskSignals, "drm")
		}},
		{ID: "mass_export_or_delete", Severity: High, Window: 10 * time.Minute, Threshold: 5, SummaryCode: "bulk_destructive_activity", Match: func(e audit.Event) bool { return e.Operation == "export.create" || e.Operation == "deletion.request" }},
		{ID: "unapproved_operator_read", Severity: Critical, Window: 5 * time.Minute, Threshold: 1, SummaryCode: "operator_read_without_elevation", Match: func(e audit.Event) bool {
			return e.ActorType == "operator" && e.Operation == "operator.source.read" && e.ReasonCode != "approved_elevation"
		}},
		{ID: "repeated_notice", Severity: High, Window: 30 * 24 * time.Hour, Threshold: 3, SummaryCode: "repeat_rights_notice", Match: func(e audit.Event) bool { return e.Operation == "notice.validated" }},
		{ID: "model_cost_spike", Severity: High, Window: time.Hour, Threshold: 20, SummaryCode: "model_cost_burst", Match: func(e audit.Event) bool {
			return e.Operation == "model.usage" && contains(e.RiskSignals, "cost_above_baseline")
		}},
		{ID: "credential_abuse", Severity: Critical, Window: 10 * time.Minute, Threshold: 3, SummaryCode: "credential_abuse_burst", Match: func(e audit.Event) bool {
			return contains(e.RiskSignals, "credential_abuse") || (e.CredentialRef != "" && e.Outcome == "denied")
		}},
		{ID: "queue_or_reconciliation_failure", Severity: High, Window: 10 * time.Minute, Threshold: 3, SummaryCode: "queue_integrity_failure", Match: func(e audit.Event) bool {
			return contains(e.RiskSignals, "duplicate_storm") || contains(e.RiskSignals, "reconciliation_failure") || contains(e.RiskSignals, "queue_replay")
		}},
	}
}

func Evaluate(events []audit.Event, now time.Time) []Finding {
	findings := []Finding{}
	for _, rule := range InitialRules() {
		matched := []audit.Event{}
		for _, event := range events {
			if !event.OccurredAt.Before(now.Add(-rule.Window)) && rule.Match(event) {
				matched = append(matched, event)
			}
		}
		if len(matched) < rule.Threshold {
			continue
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].OccurredAt.Before(matched[j].OccurredAt) })
		evidence := make([]EvidenceRef, 0, min(len(matched), 50))
		for _, event := range matched {
			if len(evidence) == 50 {
				break
			}
			evidence = append(evidence, EvidenceRef{EventID: event.ID, ReasonCode: event.ReasonCode, OccurredAt: event.OccurredAt})
		}
		findings = append(findings, Finding{TenantID: matched[0].TenantID, RuleID: rule.ID, Severity: rule.Severity, SummaryCode: rule.SummaryCode, State: "open", Evidence: evidence, FirstObservedAt: matched[0].OccurredAt, LastObservedAt: matched[len(matched)-1].OccurredAt})
	}
	return findings
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func severityAtLeast(actual, minimum Severity) bool {
	rank := map[Severity]int{Low: 1, Medium: 2, High: 3, Critical: 4}
	return rank[actual] >= rank[minimum]
}
