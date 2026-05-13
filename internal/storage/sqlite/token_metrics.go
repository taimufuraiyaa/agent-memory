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

func (s *Store) AddTokenMetric(ctx context.Context, workspace, operation string, returnedTokens, baselineTokens int) error {
	saved := baselineTokens - returnedTokens
	if saved < 0 {
		saved = 0
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO token_metrics (workspace, operation, returned_tokens, baseline_tokens, saved_tokens, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		workspace, operation, returnedTokens, baselineTokens, saved, time.Now().UTC().Format(time.RFC3339Nano),
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
