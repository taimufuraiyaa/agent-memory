package sqlite

import (
	"context"
	"time"
)

type TokenMetricTotals struct {
	Records        int `json:"records"`
	ReturnedTokens int `json:"returned_tokens"`
	BaselineTokens int `json:"baseline_tokens"`
	SavedTokens    int `json:"saved_tokens"`
}

type TokenMetricGroupTotals struct {
	RunLabel      string `json:"run_label"`
	MemoryEnabled bool   `json:"memory_enabled"`
	TokenMetricTotals
}

func (s *Store) AddTokenMetric(ctx context.Context, workspace, operation string, returnedTokens, baselineTokens int) error {
	return s.AddTokenMetricV2(ctx, workspace, operation, returnedTokens, baselineTokens, "", true)
}

func (s *Store) AddTokenMetricV2(ctx context.Context, workspace, operation string, returnedTokens, baselineTokens int, runLabel string, memoryEnabled bool) error {
	saved := baselineTokens - returnedTokens
	if saved < 0 {
		saved = 0
	}
	enabledInt := 0
	if memoryEnabled {
		enabledInt = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO token_metrics (workspace, operation, returned_tokens, baseline_tokens, saved_tokens, run_label, memory_enabled, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		workspace, operation, returnedTokens, baselineTokens, saved, runLabel, enabledInt, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) AggregateTokenMetrics(ctx context.Context, workspace string) (TokenMetricTotals, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(returned_tokens), 0), COALESCE(SUM(baseline_tokens), 0), COALESCE(SUM(saved_tokens), 0)
FROM token_metrics
WHERE workspace = ?`, workspace)
	var out TokenMetricTotals
	if err := row.Scan(&out.Records, &out.ReturnedTokens, &out.BaselineTokens, &out.SavedTokens); err != nil {
		return TokenMetricTotals{}, err
	}
	return out, nil
}

func (s *Store) AggregateTokenMetricsByGroup(ctx context.Context, workspace string) ([]TokenMetricGroupTotals, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  COALESCE(run_label, ''),
  COALESCE(memory_enabled, 1),
  COUNT(*),
  COALESCE(SUM(returned_tokens), 0),
  COALESCE(SUM(baseline_tokens), 0),
  COALESCE(SUM(saved_tokens), 0)
FROM token_metrics
WHERE workspace = ?
GROUP BY COALESCE(run_label, ''), COALESCE(memory_enabled, 1)
ORDER BY COALESCE(run_label, '') ASC, COALESCE(memory_enabled, 1) DESC
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []TokenMetricGroupTotals{}
	for rows.Next() {
		var label string
		var enabledInt int
		var totals TokenMetricTotals
		if err := rows.Scan(&label, &enabledInt, &totals.Records, &totals.ReturnedTokens, &totals.BaselineTokens, &totals.SavedTokens); err != nil {
			return nil, err
		}
		out = append(out, TokenMetricGroupTotals{
			RunLabel:          label,
			MemoryEnabled:     enabledInt != 0,
			TokenMetricTotals: totals,
		})
	}
	return out, rows.Err()
}
