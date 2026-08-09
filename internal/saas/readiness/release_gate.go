package readiness

import (
	"context"
	"errors"
	"sort"
	"time"
)

var GateApprovals = map[string][]string{
	"private_beta": {"legal_review", "operations_review", "privacy_review", "product_review", "security_review"},
	"public_beta":  {"beta_readiness", "external_signup", "legal_pages", "security_contact", "status_page", "support_policy"},
	"ga":           {"legal_review", "operations_review", "privacy_review", "product_review", "security_review"},
}

var GateGameDays = map[string][]string{
	"private_beta": {"database_failover", "queue_backlog", "model_provider_outage", "credential_leak", "cross_tenant_attempt", "incomplete_deletion"},
	"public_beta":  {"database_failover", "database_restore", "queue_backlog", "model_provider_outage", "credential_leak", "cross_tenant_attempt", "incomplete_deletion", "source_deletion", "account_deletion", "rights_notice"},
	"ga":           {"database_failover", "database_restore", "queue_backlog", "model_provider_outage", "credential_leak", "cross_tenant_attempt", "incomplete_deletion", "source_deletion", "account_deletion", "rights_notice"},
}

const releaseEvidenceFreshness = 24 * time.Hour

type ReleaseReport struct {
	Gate            string         `json:"gate"`
	Ready           bool           `json:"ready"`
	MetricsCurrent  bool           `json:"metrics_current"`
	Metrics         GateReport     `json:"metrics"`
	Approvals       ApprovalReport `json:"approvals"`
	MissingGameDays []string       `json:"missing_game_days"`
}

func (s *Service) EvaluateRelease(ctx context.Context, gate string, minimumWindow time.Duration, bundle TrustBundle, approvals []SignedApproval, now time.Time) (ReleaseReport, error) {
	report := ReleaseReport{Gate: gate, MissingGameDays: []string{}}
	metrics, metricsOK := GateMetrics[gate]
	approvalControls, approvalsOK := GateApprovals[gate]
	drills, drillsOK := GateGameDays[gate]
	if !metricsOK || !approvalsOK || !drillsOK || minimumWindow <= 0 {
		return report, errors.New("release gate configuration is invalid")
	}
	if now.IsZero() {
		now = s.now().UTC()
	}
	metricReport, err := s.Evaluate(ctx, gate, metrics, minimumWindow)
	if err != nil {
		return report, err
	}
	approvalReport, err := VerifyApprovals(gate, approvalControls, bundle, approvals, now)
	if err != nil {
		return report, err
	}
	missingDrills, err := s.MissingRequiredGameDays(ctx, drills, now.Add(-minimumWindow), now)
	if err != nil {
		return report, err
	}
	report.Metrics = metricReport
	report.MetricsCurrent = !metricReport.WindowEnd.IsZero() && !metricReport.WindowEnd.Before(now.Add(-releaseEvidenceFreshness)) && !metricReport.WindowEnd.After(now.Add(5*time.Minute))
	report.Approvals = approvalReport
	report.MissingGameDays = missingDrills
	report.Ready = metricReport.Ready && report.MetricsCurrent && approvalReport.Ready && len(missingDrills) == 0
	return report, nil
}

func (s *Service) MissingRequiredGameDays(ctx context.Context, required []string, since, until time.Time) ([]string, error) {
	if s == nil || s.pool == nil || len(required) == 0 || until.Before(since) {
		return nil, errors.New("game-day evidence query is invalid")
	}
	allowed := map[string]struct{}{}
	for _, scenario := range RequiredGameDays {
		allowed[scenario] = struct{}{}
	}
	wanted := map[string]struct{}{}
	for _, scenario := range required {
		if _, ok := allowed[scenario]; !ok {
			return nil, errors.New("game-day requirement is unknown")
		}
		wanted[scenario] = struct{}{}
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT scenario FROM saas_game_day_drills WHERE outcome='passed' AND completed_at>=$1 AND completed_at<=$2 AND scenario=ANY($3)`, since.UTC(), until.UTC(), required)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scenario string
		if err := rows.Scan(&scenario); err != nil {
			return nil, err
		}
		delete(wanted, scenario)
	}
	missing := make([]string, 0, len(wanted))
	for scenario := range wanted {
		missing = append(missing, scenario)
	}
	sort.Strings(missing)
	return missing, rows.Err()
}
