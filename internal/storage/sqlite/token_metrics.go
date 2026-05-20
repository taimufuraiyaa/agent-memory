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

type TokenMetricOperationTotals struct {
	Operation string `json:"operation"`
	TokenMetricTotals
}

type TokenMetricGroupTotals struct {
	RunLabel      string `json:"run_label"`
	MemoryEnabled bool   `json:"memory_enabled"`
	TokenMetricTotals
	Operations []TokenMetricOperationTotals `json:"operations,omitempty"`
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
  COALESCE(operation, ''),
  COUNT(*),
  COALESCE(SUM(returned_tokens), 0),
  COALESCE(SUM(baseline_tokens), 0),
  COALESCE(SUM(saved_tokens), 0)
FROM token_metrics
WHERE workspace = ?
GROUP BY COALESCE(run_label, ''), COALESCE(memory_enabled, 1), COALESCE(operation, '')
ORDER BY COALESCE(run_label, '') ASC, COALESCE(memory_enabled, 1) DESC, COALESCE(operation, '') ASC
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type groupKey struct {
		label   string
		enabled bool
	}
	groupOrder := make([]groupKey, 0, 8)
	groupMap := make(map[groupKey]*TokenMetricGroupTotals)
	for rows.Next() {
		var label string
		var enabledInt int
		var operation string
		var totals TokenMetricTotals
		if err := rows.Scan(&label, &enabledInt, &operation, &totals.Records, &totals.ReturnedTokens, &totals.BaselineTokens, &totals.SavedTokens); err != nil {
			return nil, err
		}
		key := groupKey{label: label, enabled: enabledInt != 0}
		group := groupMap[key]
		if group == nil {
			group = &TokenMetricGroupTotals{
				RunLabel:      label,
				MemoryEnabled: enabledInt != 0,
			}
			groupMap[key] = group
			groupOrder = append(groupOrder, key)
		}
		group.Records += totals.Records
		group.ReturnedTokens += totals.ReturnedTokens
		group.BaselineTokens += totals.BaselineTokens
		group.SavedTokens += totals.SavedTokens
		group.Operations = append(group.Operations, TokenMetricOperationTotals{
			Operation:         operation,
			TokenMetricTotals: totals,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TokenMetricGroupTotals, 0, len(groupOrder))
	for _, key := range groupOrder {
		out = append(out, *groupMap[key])
	}
	return out, nil
}

func (s *Store) AggregateTokenMetricsByOperation(ctx context.Context, workspace string) ([]TokenMetricOperationTotals, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  COALESCE(operation, ''),
  COUNT(*),
  COALESCE(SUM(returned_tokens), 0),
  COALESCE(SUM(baseline_tokens), 0),
  COALESCE(SUM(saved_tokens), 0)
FROM token_metrics
WHERE workspace = ?
GROUP BY COALESCE(operation, '')
ORDER BY COALESCE(operation, '') ASC
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []TokenMetricOperationTotals{}
	for rows.Next() {
		var item TokenMetricOperationTotals
		if err := rows.Scan(&item.Operation, &item.Records, &item.ReturnedTokens, &item.BaselineTokens, &item.SavedTokens); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
