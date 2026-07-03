package sqlite

import (
	"context"
	"errors"
	"strings"
	"time"
)

type LLMUsageTotals struct {
	Records          int `json:"records"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type LLMUsageGroupTotals struct {
	RunLabel      string `json:"run_label"`
	MemoryEnabled bool   `json:"memory_enabled"`
	LLMUsageTotals
}

type LLMUsageInsert struct {
	Workspace        string
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	RunLabel         string
	MemoryEnabled    bool
	CreatedAt        time.Time
}

func (s *Store) AddLLMUsageMetric(ctx context.Context, in LLMUsageInsert) error {
	if strings.TrimSpace(in.Workspace) == "" {
		return errors.New("workspace is required")
	}
	if strings.TrimSpace(in.Provider) == "" {
		return errors.New("provider is required")
	}
	if in.PromptTokens < 0 || in.CompletionTokens < 0 || in.TotalTokens < 0 {
		return errors.New("token counts must be non-negative")
	}
	if in.TotalTokens == 0 {
		in.TotalTokens = in.PromptTokens + in.CompletionTokens
	}
	at := in.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	enabledInt := 0
	if in.MemoryEnabled {
		enabledInt = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO llm_usage_metrics (
  workspace, provider, model, prompt_tokens, completion_tokens, total_tokens, run_label, memory_enabled, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, strings.TrimSpace(in.Workspace), strings.TrimSpace(in.Provider), strings.TrimSpace(in.Model),
		in.PromptTokens, in.CompletionTokens, in.TotalTokens,
		strings.TrimSpace(in.RunLabel), enabledInt, at.Format(time.RFC3339Nano))
	return err
}

func (s *Store) AggregateLLMUsageTotals(ctx context.Context, workspace string) (LLMUsageTotals, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return LLMUsageTotals{}, errors.New("workspace is required")
	}
	row := s.db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COALESCE(SUM(prompt_tokens), 0),
  COALESCE(SUM(completion_tokens), 0),
  COALESCE(SUM(total_tokens), 0)
FROM llm_usage_metrics
WHERE workspace = ?`, workspace)
	var out LLMUsageTotals
	if err := row.Scan(&out.Records, &out.PromptTokens, &out.CompletionTokens, &out.TotalTokens); err != nil {
		return LLMUsageTotals{}, err
	}
	return out, nil
}

func (s *Store) AggregateLLMUsageByGroup(ctx context.Context, workspace string) ([]LLMUsageGroupTotals, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  COALESCE(run_label, ''),
  COALESCE(memory_enabled, 1),
  COUNT(*),
  COALESCE(SUM(prompt_tokens), 0),
  COALESCE(SUM(completion_tokens), 0),
  COALESCE(SUM(total_tokens), 0)
FROM llm_usage_metrics
WHERE workspace = ?
GROUP BY COALESCE(run_label, ''), COALESCE(memory_enabled, 1)
ORDER BY COALESCE(run_label, '') ASC, COALESCE(memory_enabled, 1) DESC
`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []LLMUsageGroupTotals{}
	for rows.Next() {
		var label string
		var enabledInt int
		var totals LLMUsageTotals
		if err := rows.Scan(&label, &enabledInt, &totals.Records, &totals.PromptTokens, &totals.CompletionTokens, &totals.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, LLMUsageGroupTotals{
			RunLabel:       label,
			MemoryEnabled:  enabledInt != 0,
			LLMUsageTotals: totals,
		})
	}
	return out, rows.Err()
}
